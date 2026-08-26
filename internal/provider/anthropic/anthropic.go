// Package anthropic implements provider.Provider against the Anthropic
// Messages API (dev spec §4): its own auth header, message shape, and
// SSE event framing.
package anthropic

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

// DefaultBaseURL is the Anthropic API endpoint.
const DefaultBaseURL = "https://api.anthropic.com"

// apiVersion is the required anthropic-version header value.
const apiVersion = "2023-06-01"

// defaultMaxTokens is used when the request sets no cap: max_tokens is a
// required field on /v1/messages.
const defaultMaxTokens = 4096

// maxLine bounds one SSE line.
const maxLine = 4 * 1024 * 1024

var _ provider.Provider = (*Provider)(nil)

// Provider talks to the Anthropic API (or a compatible proxy at another
// base URL).
type Provider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// New returns a Provider for baseURL (DefaultBaseURL if empty). The
// HTTP client has no global timeout — streams are long-lived — so
// callers bound every request with their context.
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
func (p *Provider) ID() string { return "anthropic" }

func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("building %s request: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	return req, nil
}

// HealthCheck implements provider.Provider via GET /v1/models, which
// exercises auth.
func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := p.newRequest(ctx, http.MethodGet, "/v1/models?limit=1", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic not reachable at %s: %w", p.baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, readError(resp.Body))
	}
	return nil
}

// ListModels implements provider.Provider via GET /v1/models.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	req, err := p.newRequest(ctx, http.MethodGet, "/v1/models?limit=100", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing anthropic models: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing anthropic models: status %d: %s", resp.StatusCode, readError(resp.Body))
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding anthropic models: %w", err)
	}
	models := make([]provider.ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		models = append(models, provider.ModelInfo{ID: m.ID})
	}
	return models, nil
}

// samplingRemoved reports whether model rejects temperature/top_p/top_k
// with a 400 (the Claude 5 family and Opus 4.7+ removed manual sampling
// params in favor of adaptive thinking).
func samplingRemoved(model string) bool {
	for _, prefix := range []string{
		"claude-fable-5", "claude-mythos", "claude-opus-5",
		"claude-sonnet-5", "claude-opus-4-7", "claude-opus-4-8",
	} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// chatBody is the /v1/messages request shape.
type chatBody struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	System        string             `json:"system,omitempty"`
	Messages      []provider.Message `json:"messages"`
	Stream        bool               `json:"stream"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
}

// normalizeMessages adapts arbitrary chat history to the Messages API
// contract: roles must alternate and the first message must be "user".
// RP chats open with the character's greeting, so a placeholder user
// turn is prepended when needed, and consecutive same-role messages
// (e.g. after a stopped empty reply) are merged.
func normalizeMessages(in []provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(in)+1)
	for _, m := range in {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Content += "\n\n" + m.Content
			continue
		}
		out = append(out, m)
	}
	if len(out) > 0 && out[0].Role != provider.RoleUser {
		out = append([]provider.Message{{Role: provider.RoleUser, Content: "[Start of conversation.]"}}, out...)
	}
	return out
}

// buildChatBody maps a provider-agnostic request onto /v1/messages.
// min_p and repeat_penalty don't exist on this API; on models that
// removed manual sampling, temperature/top_p/top_k are dropped too
// (they'd be rejected with a 400). Everything dropped is recorded
// (inspector contract, §4).
func buildChatBody(req provider.ChatRequest) (chatBody, provider.ParamReport) {
	body := chatBody{
		Model:     req.Model,
		MaxTokens: defaultMaxTokens,
		System:    req.System,
		Messages:  normalizeMessages(req.Messages),
		Stream:    !req.NoStream,
	}
	report := provider.ParamReport{Sent: map[string]any{}, Dropped: []string{}}
	noSampling := samplingRemoved(req.Model)
	sample := func(name string, set func(), value any) {
		if noSampling {
			report.Dropped = append(report.Dropped, name)
			return
		}
		set()
		report.Sent[name] = value
	}
	if v := req.Params.Temperature; v != nil {
		sample("temperature", func() { body.Temperature = v }, *v)
	}
	if v := req.Params.TopP; v != nil {
		sample("top_p", func() { body.TopP = v }, *v)
	}
	if v := req.Params.TopK; v != nil {
		sample("top_k", func() { body.TopK = v }, *v)
	}
	if v := req.Params.MaxTokens; v != nil {
		body.MaxTokens = *v
		report.Sent["max_tokens"] = *v
	}
	if len(req.Params.Stop) > 0 {
		body.StopSequences = req.Params.Stop
		report.Sent["stop_sequences"] = req.Params.Stop
	}
	if req.Params.MinP != nil {
		report.Dropped = append(report.Dropped, "min_p")
	}
	if req.Params.RepeatPenalty != nil {
		report.Dropped = append(report.Dropped, "repeat_penalty")
	}
	return body, report
}

// event is one SSE data payload; Type discriminates the union.
type event struct {
	Type  string `json:"type"`
	Delta struct {
		Type       string `json:"type"` // text_delta, thinking_delta, ...
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"` // on message_delta
	} `json:"delta"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"` // on message_start
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"` // on message_delta
	Error *apiError `json:"error"` // on error events
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// DescribeRequest implements provider.RequestDescriber: the exact
// /v1/messages request ChatStream would send, for the context
// inspector. The API key travels in a header, never in the body.
func (p *Provider) DescribeRequest(req provider.ChatRequest) (provider.RequestDescription, error) {
	body, report := buildChatBody(req)
	raw, err := json.Marshal(body)
	if err != nil {
		return provider.RequestDescription{}, fmt.Errorf("encoding chat request: %w", err)
	}
	return provider.RequestDescription{URL: p.baseURL + "/v1/messages", Body: raw, Report: report}, nil
}

// ChatStream implements provider.Provider via POST /v1/messages. With
// req.NoStream the completion is requested unstreamed and arrives on
// the channel as a single delta followed by Done.
func (p *Provider) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	body, _ := buildChatBody(req)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding chat request: %w", err)
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, "/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if !req.NoStream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("opening anthropic chat stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := readError(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("anthropic chat: status %d: %s", resp.StatusCode, msg)
	}

	events := make(chan provider.StreamEvent)
	if req.NoStream {
		go p.readOnce(ctx, resp.Body, events)
	} else {
		go p.readStream(ctx, resp.Body, events)
	}
	return events, nil
}

// message is a non-streamed /v1/messages response.
type message struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

// readOnce delivers a non-streamed message as one delta plus Done.
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
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		events <- provider.StreamEvent{Err: fmt.Errorf("decoding message: %w", err)}
		return
	}
	if m.Error != nil {
		events <- provider.StreamEvent{Err: fmt.Errorf("anthropic: %s", m.Error.Message)}
		return
	}
	if m.StopReason == "refusal" {
		events <- provider.StreamEvent{Err: fmt.Errorf("anthropic: the model declined to respond (refusal)")}
		return
	}
	var text strings.Builder
	for _, block := range m.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if text.Len() > 0 {
		events <- provider.StreamEvent{Delta: text.String()}
	}
	events <- provider.StreamEvent{
		Done: true,
		Usage: &provider.Usage{
			PromptTokens:     m.Usage.InputTokens,
			CompletionTokens: m.Usage.OutputTokens,
		},
	}
}

// readStream pumps SSE events from body into events, closing the channel
// after the terminal event. Sends are unguarded: the Provider contract
// requires consumers to drain the channel until close.
func (p *Provider) readStream(ctx context.Context, body io.ReadCloser, events chan<- provider.StreamEvent) {
	defer close(events)
	defer body.Close() //nolint:errcheck // read-only body

	usage := provider.Usage{}
	stopReason := ""

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxLine)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		// The type discriminator is inside the data JSON, so "event:"
		// lines, pings, and blank keep-alives can all be skipped.
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		var ev event
		if err := json.Unmarshal(bytes.TrimSpace(data), &ev); err != nil {
			events <- provider.StreamEvent{Err: fmt.Errorf("decoding stream event: %w", err)}
			return
		}
		switch ev.Type {
		case "message_start":
			usage.PromptTokens = ev.Message.Usage.InputTokens
		case "content_block_delta":
			// Only text is chat; thinking deltas and other block kinds
			// are not surfaced.
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				events <- provider.StreamEvent{Delta: ev.Delta.Text}
			}
		case "message_delta":
			usage.CompletionTokens = ev.Usage.OutputTokens
			stopReason = ev.Delta.StopReason
		case "message_stop":
			if stopReason == "refusal" {
				events <- provider.StreamEvent{Err: fmt.Errorf("anthropic: the model declined to respond (refusal)")}
				return
			}
			events <- provider.StreamEvent{Done: true, Usage: &usage}
			return
		case "error":
			msg := "unknown stream error"
			if ev.Error != nil {
				msg = ev.Error.Message
			}
			events <- provider.StreamEvent{Err: fmt.Errorf("anthropic: %s", msg)}
			return
		}
	}
	err := scanner.Err()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	} else if err == nil {
		err = io.ErrUnexpectedEOF
	}
	events <- provider.StreamEvent{Err: err}
}

// readError extracts a short message from a non-200 body:
// {"type":"error","error":{"type":...,"message":...}}.
func readError(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil || len(raw) == 0 {
		return "no error detail"
	}
	var e struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return string(bytes.TrimSpace(raw))
}
