package ollama

// This file holds the model-management endpoints (dev spec §8): pull
// with streamed progress, delete, loaded-model status, and version.
// They live on the same Provider so the base URL and HTTP plumbing stay
// in one place; internal/ollamamgr wraps them as the Wails-bound
// manager service.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Version returns the Ollama server version via GET /api/version. It
// doubles as the onboarding detect probe.
func (p *Provider) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/version", nil)
	if err != nil {
		return "", fmt.Errorf("building version request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama not reachable at %s: %w", p.baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama at %s: unexpected status %d", p.baseURL, resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding version response: %w", err)
	}
	return body.Version, nil
}

// PullEvent is one progress update from a streaming pull. During the
// download phase Total/Completed describe the layer currently being
// fetched (the model weights layer dominates). Exactly one terminal
// event is sent: Done=true or Err set.
type PullEvent struct {
	Status    string // e.g. "pulling manifest", "pulling ab1c…", "success"
	Digest    string // layer digest during downloads, "" otherwise
	Total     int64  // bytes in the current layer, 0 if unknown
	Completed int64  // bytes fetched of the current layer
	Done      bool   // true on successful completion
	Err       error  // set on failure or cancellation; terminal
}

// Pull downloads a model via POST /api/pull, streaming progress on the
// returned channel. Same contract as ChatStream: the channel is closed
// after a terminal event and the caller must drain it until close,
// including after canceling ctx. Canceling leaves partial layers in
// Ollama's cache, so a re-pull resumes where it left off.
func (p *Provider) Pull(ctx context.Context, name string) (<-chan PullEvent, error) {
	payload, err := json.Marshal(map[string]any{"model": name, "stream": true})
	if err != nil {
		return nil, fmt.Errorf("encoding pull request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("building pull request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opening ollama pull stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := readError(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("ollama pull: status %d: %s", resp.StatusCode, msg)
	}

	events := make(chan PullEvent)
	go p.readPullStream(ctx, resp.Body, events)
	return events, nil
}

// pullChunk is one streamed line of a /api/pull response.
type pullChunk struct {
	Status    string `json:"status"`
	Digest    string `json:"digest"`
	Total     int64  `json:"total"`
	Completed int64  `json:"completed"`
	Error     string `json:"error"`
}

// readPullStream pumps NDJSON progress lines into events. Like
// readStream, sends are unguarded — consumers drain until close.
func (p *Provider) readPullStream(ctx context.Context, body io.ReadCloser, events chan<- PullEvent) {
	defer close(events)
	defer body.Close() //nolint:errcheck // read-only body

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxLine)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk pullChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			events <- PullEvent{Err: fmt.Errorf("decoding pull line: %w", err)}
			return
		}
		if chunk.Error != "" {
			events <- PullEvent{Err: fmt.Errorf("ollama: %s", chunk.Error)}
			return
		}
		if chunk.Status == "success" {
			events <- PullEvent{Status: chunk.Status, Done: true}
			return
		}
		events <- PullEvent{
			Status:    chunk.Status,
			Digest:    chunk.Digest,
			Total:     chunk.Total,
			Completed: chunk.Completed,
		}
	}
	err := scanner.Err()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	} else if err == nil {
		err = io.ErrUnexpectedEOF
	}
	events <- PullEvent{Err: err}
}

// Delete removes an installed model via DELETE /api/delete.
func (p *Provider) Delete(ctx context.Context, name string) error {
	payload, err := json.Marshal(map[string]string{"model": name})
	if err != nil {
		return fmt.Errorf("encoding delete request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.baseURL+"/api/delete", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building delete request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("deleting model %q: %w", name, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("model %q is not installed", name)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deleting model %q: status %d: %s", name, resp.StatusCode, readError(resp.Body))
	}
	return nil
}

// LoadedModel is one entry from /api/ps: a model currently in memory.
type LoadedModel struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`      // total memory footprint in bytes
	SizeVRAM  int64  `json:"sizeVram"`  // bytes of that in GPU memory
	ExpiresAt string `json:"expiresAt"` // RFC3339, when it unloads
}

// PS lists loaded models via GET /api/ps (dev-mode status bar, M1.7;
// implemented here so the manager surface is complete).
func (p *Provider) PS(ctx context.Context) ([]LoadedModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/ps", nil)
	if err != nil {
		return nil, fmt.Errorf("building ps request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing loaded models: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing loaded models: status %d: %s", resp.StatusCode, readError(resp.Body))
	}
	var body struct {
		Models []struct {
			Name      string `json:"name"`
			Size      int64  `json:"size"`
			SizeVRAM  int64  `json:"size_vram"`
			ExpiresAt string `json:"expires_at"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding ps response: %w", err)
	}
	models := make([]LoadedModel, 0, len(body.Models))
	for _, m := range body.Models {
		models = append(models, LoadedModel{
			Name: m.Name, Size: m.Size, SizeVRAM: m.SizeVRAM, ExpiresAt: m.ExpiresAt,
		})
	}
	return models, nil
}
