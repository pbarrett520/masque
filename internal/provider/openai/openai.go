// Package openai implements provider.Provider against the OpenAI-compat
// chat API (dev spec §4): base URL + key + /chat/completions with SSE.
// One implementation covers OpenRouter, LM Studio, vLLM, llama.cpp
// server, Ollama's /v1 layer, and OpenAI itself.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"masque/internal/provider"
)

// DefaultBaseURL is used when no endpoint is configured. Compat servers
// are addressed by their full prefix including /v1 (e.g.
// https://openrouter.ai/api/v1, http://localhost:1234/v1).
const DefaultBaseURL = "https://api.openai.com/v1"

// maxLine bounds one SSE line; generous because some servers batch
// large deltas or attach full usage/metadata blocks.
const maxLine = 4 * 1024 * 1024

var _ provider.Provider = (*Provider)(nil)

// Provider talks to one OpenAI-compatible endpoint.
type Provider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// New returns a Provider for baseURL (DefaultBaseURL if empty). apiKey
// may be empty for local servers that don't authenticate. The HTTP
// client has no global timeout — streams are long-lived — so callers
// bound every request with their context.
func New(baseURL, apiKey string) *Provider {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Provider{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{},
	}
}

// ID implements provider.Provider.
func (p *Provider) ID() string { return "openai" }

func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("building %s request: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	return req, nil
}

// HealthCheck implements provider.Provider via GET /models, which every
// compat server exposes and which exercises auth.
func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := p.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("endpoint not reachable at %s: %w", p.baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("endpoint %s: status %d: %s", p.baseURL, resp.StatusCode, readError(resp.Body))
	}
	return nil
}

// ListModels implements provider.Provider via GET /models. Compat
// servers report only ids — no size/family/quant metadata.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	req, err := p.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing models: status %d: %s", resp.StatusCode, readError(resp.Body))
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding models: %w", err)
	}
	models := make([]provider.ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		models = append(models, provider.ModelInfo{ID: m.ID})
	}
	return models, nil
}

// chatBody is the /chat/completions request shape.
type chatBody struct {
	Model         string             `json:"model"`
	Messages      []provider.Message `json:"messages"`
	Stream        bool               `json:"stream"`
	StreamOptions map[string]any     `json:"stream_options,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	MaxTokens     *int               `json:"max_tokens,omitempty"`
	Stop          []string           `json:"stop,omitempty"`
}

// buildChatBody maps a provider-agnostic request onto /chat/completions.
// top_k, min_p, and repeat_penalty are not part of the OpenAI API and
// strict servers reject unknown params, so they are dropped and recorded
// (inspector contract, §4).
func buildChatBody(req provider.ChatRequest) (chatBody, provider.ParamReport) {
	messages := make([]provider.Message, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, provider.Message{Role: provider.RoleSystem, Content: req.System})
	}
	messages = append(messages, req.Messages...)

	body := chatBody{
		Model:    req.Model,
		Messages: messages,
		Stream:   !req.NoStream,
	}
	if body.Stream {
		// Ask servers that support it to attach token usage to the
		// final chunk; others ignore the field. Rejected alongside
		// stream:false, so only sent when streaming.
		body.StreamOptions = map[string]any{"include_usage": true}
	}
	report := provider.ParamReport{Sent: map[string]any{}, Dropped: []string{}}
	if v := req.Params.Temperature; v != nil {
		body.Temperature = v
		report.Sent["temperature"] = *v
	}
	if v := req.Params.TopP; v != nil {
		body.TopP = v
		report.Sent["top_p"] = *v
	}
	if v := req.Params.MaxTokens; v != nil {
		body.MaxTokens = v
		report.Sent["max_tokens"] = *v
	}
	if len(req.Params.Stop) > 0 {
		body.Stop = req.Params.Stop
		report.Sent["stop"] = req.Params.Stop
	}
	if req.Params.TopK != nil {
		report.Dropped = append(report.Dropped, "top_k")
	}
	if req.Params.MinP != nil {
		report.Dropped = append(report.Dropped, "min_p")
	}
	if req.Params.RepeatPenalty != nil {
		report.Dropped = append(report.Dropped, "repeat_penalty")
	}
	return body, report
}

// chunk is one SSE data payload of a streamed completion.
type chunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

type apiError struct {
	Message string `json:"message"`
}

// DescribeRequest implements provider.RequestDescriber: the exact
// /chat/completions request ChatStream would send, for the context
// inspector. The API key travels in a header, never in the body.
func (p *Provider) DescribeRequest(req provider.ChatRequest) (provider.RequestDescription, error) {
	body, report := buildChatBody(req)
	raw, err := json.Marshal(body)
	if err != nil {
		return provider.RequestDescription{}, fmt.Errorf("encoding chat request: %w", err)
	}
	return provider.RequestDescription{URL: p.baseURL + "/chat/completions", Body: raw, Report: report}, nil
}

// ChatStream implements provider.Provider via POST /chat/completions.
// With req.NoStream the completion is requested unstreamed and arrives
// on the channel as a single delta followed by Done.
func (p *Provider) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	body, _ := buildChatBody(req)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding chat request: %w", err)
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if !req.NoStream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("opening chat stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := readError(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("chat completion: status %d: %s", resp.StatusCode, msg)
	}

	events := make(chan provider.StreamEvent)
	if req.NoStream {
		go p.readOnce(ctx, resp.Body, events)
	} else {
		go p.readStream(ctx, resp.Body, events)
	}
	return events, nil
}

// completion is a non-streamed /chat/completions response.
type completion struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

// readOnce delivers a non-streamed completion as one delta plus Done.
func (p *Provider) readOnce(ctx context.Context, body io.ReadCloser, events chan<- provider.StreamEvent) {
	defer close(events)
	defer body.Close() //nolint:errcheck // read-only body

	raw, err := io.ReadAll(io.LimitReader(body, maxLine))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		events <- provider.StreamEvent{Err: err}
		return
	}
	var c completion
	if err := json.Unmarshal(raw, &c); err != nil {
		events <- provider.StreamEvent{Err: fmt.Errorf("decoding completion: %w", err)}
		return
	}
	if c.Error != nil {
		events <- provider.StreamEvent{Err: fmt.Errorf("provider: %s", c.Error.Message)}
		return
	}
	if len(c.Choices) == 0 {
		events <- provider.StreamEvent{Err: fmt.Errorf("completion has no choices")}
		return
	}
	var usage *provider.Usage
	if c.Usage != nil {
		usage = &provider.Usage{
			PromptTokens:     c.Usage.PromptTokens,
			CompletionTokens: c.Usage.CompletionTokens,
		}
	}
	if text := c.Choices[0].Message.Content; text != "" {
		events <- provider.StreamEvent{Delta: text}
	}
	events <- provider.StreamEvent{Done: true, Usage: usage}
}

// readStream pumps SSE lines from body into events, closing the channel
// after the terminal event. Sends are unguarded: the Provider contract
// requires consumers to drain the channel until close.
func (p *Provider) readStream(ctx context.Context, body io.ReadCloser, events chan<- provider.StreamEvent) {
	defer close(events)
	defer body.Close() //nolint:errcheck // read-only body

	var usage *provider.Usage
	sawFinish := false

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxLine)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		// SSE framing: only data lines matter; comments, event names,
		// and blank keep-alives are skipped.
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if string(data) == "[DONE]" {
			events <- provider.StreamEvent{Done: true, Usage: usage}
			return
		}
		var c chunk
		if err := json.Unmarshal(data, &c); err != nil {
			events <- provider.StreamEvent{Err: fmt.Errorf("decoding stream chunk: %w", err)}
			return
		}
		if c.Error != nil {
			events <- provider.StreamEvent{Err: fmt.Errorf("provider: %s", c.Error.Message)}
			return
		}
		if c.Usage != nil {
			usage = &provider.Usage{
				PromptTokens:     c.Usage.PromptTokens,
				CompletionTokens: c.Usage.CompletionTokens,
			}
		}
		for _, choice := range c.Choices {
			// Reasoning/thinking deltas arrive with empty content on
			// some servers (e.g. Ollama's /v1); only content is chat.
			if choice.Delta.Content != "" {
				events <- provider.StreamEvent{Delta: choice.Delta.Content}
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				sawFinish = true
			}
		}
	}
	// Stream ended without [DONE]. Some servers close right after the
	// finish_reason chunk; treat that as success.
	err := scanner.Err()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	} else if err == nil {
		if sawFinish {
			events <- provider.StreamEvent{Done: true, Usage: usage}
			return
		}
		err = io.ErrUnexpectedEOF
	}
	events <- provider.StreamEvent{Err: err}
}

// readError extracts a short message from a non-200 body:
// {"error": {"message": ...}} per the OpenAI error shape, with fallbacks
// for servers that return plain text or a bare error string.
func readError(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil || len(raw) == 0 {
		return "no error detail"
	}
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && len(e.Error) > 0 {
		var obj apiError
		if json.Unmarshal(e.Error, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
		var s string
		if json.Unmarshal(e.Error, &s) == nil && s != "" {
			return s
		}
	}
	return string(bytes.TrimSpace(raw))
}
