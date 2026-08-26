// Package ollama implements provider.Provider against Ollama's native
// HTTP API (dev spec §4): /api/chat streaming JSON lines rather than the
// OpenAI-compat layer, because native exposes keep_alive and model
// options cleanly.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"masque/internal/provider"
)

// DefaultBaseURL is where a stock Ollama install listens.
const DefaultBaseURL = "http://localhost:11434"

// maxLine bounds one streamed JSON line; the final /api/chat line carries
// the whole message metadata and can be large.
const maxLine = 4 * 1024 * 1024

var _ provider.Provider = (*Provider)(nil)

// Provider talks to one Ollama endpoint.
type Provider struct {
	baseURL string
	client  *http.Client
}

// New returns a Provider for baseURL (DefaultBaseURL if empty). The
// HTTP client has no global timeout — streams are long-lived — so
// callers bound every request with their context.
func New(baseURL string) *Provider {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Provider{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{},
	}
}

// ID implements provider.Provider.
func (p *Provider) ID() string { return "ollama" }

// BaseURL reports the endpoint this provider talks to (shown by the
// manager UI when the endpoint is unreachable).
func (p *Provider) BaseURL() string { return p.baseURL }

// HealthCheck implements provider.Provider via GET /api/version.
func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/version", nil)
	if err != nil {
		return fmt.Errorf("building version request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", p.baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama at %s: unexpected status %d", p.baseURL, resp.StatusCode)
	}
	return nil
}

// ListModels implements provider.Provider via GET /api/tags, hiding
// models that can't chat (e.g. embedding-only).
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return p.listModels(ctx, false)
}

// ListAllModels returns every installed model, chat-capable or not —
// the dev-mode model manager (§9) shows the complete list.
func (p *Provider) ListAllModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return p.listModels(ctx, true)
}

func (p *Provider) listModels(ctx context.Context, includeAll bool) ([]provider.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("building tags request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing ollama models: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing ollama models: status %d: %s", resp.StatusCode, readError(resp.Body))
	}
	var body struct {
		Models []struct {
			Name         string   `json:"name"`
			Size         int64    `json:"size"`
			ModifiedAt   string   `json:"modified_at"`
			Capabilities []string `json:"capabilities"`
			Details      struct {
				Family            string `json:"family"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding ollama models: %w", err)
	}
	models := make([]provider.ModelInfo, 0, len(body.Models))
	for _, m := range body.Models {
		// Newer Ollama reports capabilities; hide models that can't chat
		// (e.g. embedding-only). Older versions omit the field — keep those.
		if !includeAll && len(m.Capabilities) > 0 && !slices.Contains(m.Capabilities, "completion") {
			continue
		}
		models = append(models, provider.ModelInfo{
			ID:         m.Name,
			Size:       m.Size,
			Family:     m.Details.Family,
			Quant:      m.Details.QuantizationLevel,
			ModifiedAt: m.ModifiedAt,
		})
	}
	return models, nil
}

// ContextWindow returns model's context length via POST /api/show, for
// PromptBuilder token budgeting. Ollama reports it as the
// "<architecture>.context_length" key of model_info.
func (p *Provider) ContextWindow(ctx context.Context, model string) (int, error) {
	payload, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return 0, fmt.Errorf("encoding show request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/show", bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("building show request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("showing model %q: %w", model, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("showing model %q: status %d: %s", model, resp.StatusCode, readError(resp.Body))
	}
	var body struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decoding show response for %q: %w", model, err)
	}
	for key, val := range body.ModelInfo {
		if strings.HasSuffix(key, ".context_length") {
			if n, ok := val.(float64); ok && n > 0 {
				return int(n), nil
			}
		}
	}
	return 0, fmt.Errorf("model %q: no context_length in model_info", model)
}

// chatBody is the native /api/chat request shape.
type chatBody struct {
	Model    string             `json:"model"`
	Messages []provider.Message `json:"messages"`
	Stream   bool               `json:"stream"`
	Options  map[string]any     `json:"options,omitempty"`
}

// buildChatBody maps a provider-agnostic request onto /api/chat. Ollama
// supports every sampler param Masque exposes, so the report's Dropped
// list is always empty here; it exists for interface parity with cloud
// providers.
func buildChatBody(req provider.ChatRequest) (chatBody, provider.ParamReport) {
	messages := make([]provider.Message, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, provider.Message{Role: provider.RoleSystem, Content: req.System})
	}
	messages = append(messages, req.Messages...)

	options := map[string]any{}
	if v := req.Params.Temperature; v != nil {
		options["temperature"] = *v
	}
	if v := req.Params.TopP; v != nil {
		options["top_p"] = *v
	}
	if v := req.Params.TopK; v != nil {
		options["top_k"] = *v
	}
	if v := req.Params.MinP; v != nil {
		options["min_p"] = *v
	}
	if v := req.Params.RepeatPenalty; v != nil {
		options["repeat_penalty"] = *v
	}
	if v := req.Params.MaxTokens; v != nil {
		options["num_predict"] = *v
	}
	if len(req.Params.Stop) > 0 {
		options["stop"] = req.Params.Stop
	}
	report := provider.ParamReport{Sent: options, Dropped: []string{}}
	if len(options) == 0 {
		options = nil
	}
	return chatBody{Model: req.Model, Messages: messages, Stream: !req.NoStream, Options: options}, report
}

// DescribeRequest implements provider.RequestDescriber: the exact
// /api/chat request ChatStream would send, for the context inspector.
func (p *Provider) DescribeRequest(req provider.ChatRequest) (provider.RequestDescription, error) {
	body, report := buildChatBody(req)
	raw, err := json.Marshal(body)
	if err != nil {
		return provider.RequestDescription{}, fmt.Errorf("encoding chat request: %w", err)
	}
	return provider.RequestDescription{URL: p.baseURL + "/api/chat", Body: raw, Report: report}, nil
}

// chatChunk is one streamed line of a /api/chat response.
type chatChunk struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done            bool   `json:"done"`
	Error           string `json:"error"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

// ChatStream implements provider.Provider via POST /api/chat.
func (p *Provider) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	body, _ := buildChatBody(req)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding chat request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("building chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("opening ollama chat stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := readError(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("ollama chat: status %d: %s", resp.StatusCode, msg)
	}

	events := make(chan provider.StreamEvent)
	go p.readStream(ctx, resp.Body, events)
	return events, nil
}

// readStream pumps NDJSON lines from body into events, closing the
// channel after the terminal event. Sends are unguarded: the Provider
// contract requires consumers to drain the channel until close, which
// makes delivery of the terminal event deterministic (a ctx-guarded
// select could drop it in the cancellation race).
func (p *Provider) readStream(ctx context.Context, body io.ReadCloser, events chan<- provider.StreamEvent) {
	defer close(events)
	defer body.Close() //nolint:errcheck // read-only body

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxLine)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk chatChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			events <- provider.StreamEvent{Err: fmt.Errorf("decoding stream line: %w", err)}
			return
		}
		if chunk.Error != "" {
			events <- provider.StreamEvent{Err: fmt.Errorf("ollama: %s", chunk.Error)}
			return
		}
		if chunk.Done {
			var usage *provider.Usage
			if chunk.PromptEvalCount > 0 || chunk.EvalCount > 0 {
				usage = &provider.Usage{
					PromptTokens:     chunk.PromptEvalCount,
					CompletionTokens: chunk.EvalCount,
				}
			}
			events <- provider.StreamEvent{Delta: chunk.Message.Content, Done: true, Usage: usage}
			return
		}
		if chunk.Message.Content != "" {
			events <- provider.StreamEvent{Delta: chunk.Message.Content}
		}
	}
	// Reaching here means the stream ended without a done marker:
	// cancellation, network drop, or a truncated response.
	err := scanner.Err()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	} else if err == nil {
		err = io.ErrUnexpectedEOF
	}
	events <- provider.StreamEvent{Err: err}
}

// readError extracts a short error message from a non-200 body, which
// Ollama sends as {"error": "..."}.
func readError(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil || len(raw) == 0 {
		return "no error detail"
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return string(bytes.TrimSpace(raw))
}
