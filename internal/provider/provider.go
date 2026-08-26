// Package provider defines the interface between Masque's chat core and
// inference backends (dev spec §4). Implementations are stateless: keys
// and base URLs are injected at construction from settings, and every
// call carries its own context.
package provider

import (
	"context"
	"encoding/json"
)

// Message roles, matching the CHECK constraint on messages.role.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Provider is one inference backend (ollama, openai-compat, anthropic).
type Provider interface {
	// ID identifies the implementation, e.g. "ollama".
	ID() string
	// ListModels returns the models available at the endpoint.
	ListModels(ctx context.Context) ([]ModelInfo, error)
	// ChatStream opens a streaming chat completion. The returned channel
	// is closed after a terminal event: one with Done=true, or one with
	// Err set (context cancellation surfaces as Err=ctx.Err()). The
	// caller must drain the channel until it is closed, including after
	// canceling ctx — sends are blocking, so an abandoned channel leaks
	// the stream goroutine.
	ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
	// HealthCheck probes the endpoint; nil means reachable.
	HealthCheck(ctx context.Context) error
}

// ModelInfo describes one available model.
type ModelInfo struct {
	ID         string `json:"id"`         // name used in ChatRequest.Model
	Size       int64  `json:"size"`       // bytes on disk, 0 if unknown
	Family     string `json:"family"`     // e.g. "llama", "" if unknown
	Quant      string `json:"quant"`      // e.g. "Q4_K_M", "" if unknown
	ModifiedAt string `json:"modifiedAt"` // RFC3339, "" if unknown
}

// Message is one turn of conversation history.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is a provider-agnostic chat completion request.
type ChatRequest struct {
	Model    string
	Messages []Message // history; System is carried separately
	System   string
	Params   SamplerParams
	// NoStream asks for a non-streamed completion (dev-mode endpoint
	// config, §9): the provider sends stream:false and the ChatStream
	// channel delivers the whole reply as one delta followed by Done.
	NoStream bool
}

// RequestDescription is the wire request a provider would send for a
// ChatRequest: the exact URL and JSON body, plus which sampler params
// were sent vs dropped. This is what the dev-mode context inspector
// renders (§9); bodies never contain credentials (keys travel in
// headers), so the description is safe to persist and display.
type RequestDescription struct {
	URL    string          `json:"url"`
	Body   json.RawMessage `json:"body"`
	Report ParamReport     `json:"report"`
}

// RequestDescriber is implemented by providers that can describe their
// wire request without sending it. All Masque providers implement it;
// it is optional on the interface so third-party additions degrade to
// "no inspector detail" instead of breaking.
type RequestDescriber interface {
	DescribeRequest(req ChatRequest) (RequestDescription, error)
}

// SamplerParams are the user-tunable generation parameters. Pointer
// fields distinguish "unset, use the model's default" from an explicit
// zero. Parameters a provider does not support are dropped from the
// request; the ParamReport records which (inspector contract, §4).
type SamplerParams struct {
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"topP,omitempty"`
	TopK          *int     `json:"topK,omitempty"`
	MinP          *float64 `json:"minP,omitempty"`
	RepeatPenalty *float64 `json:"repeatPenalty,omitempty"`
	MaxTokens     *int     `json:"maxTokens,omitempty"`
	Stop          []string `json:"stop,omitempty"`
}

// ParamReport records which sampler parameters were actually sent to a
// provider and which were dropped as unsupported. The dev-mode context
// inspector (M1.7) renders this; providers produce it from M1.2 so the
// seam exists.
type ParamReport struct {
	Sent    map[string]any `json:"sent"`
	Dropped []string       `json:"dropped"`
}

// StreamEvent is one event on a chat stream.
type StreamEvent struct {
	Delta string // text to append; may be empty on terminal events
	Done  bool   // true on successful completion
	Err   error  // set on failure or cancellation; terminal
	Usage *Usage // filled on Done when the backend reports it
}

// Usage is token accounting reported by the backend.
type Usage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
}
