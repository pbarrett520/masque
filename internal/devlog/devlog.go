// Package devlog is the dev-mode log view (dev spec §9): an in-memory
// ring of the last 200 provider requests and responses, for debugging.
// Entries hold the provider-agnostic request description — bodies never
// contain credentials (keys travel in HTTP headers, which are not
// logged). Nothing is persisted; the log dies with the process.
package devlog

import (
	"encoding/json"
	"sync"

	"masque/internal/provider"
)

// Capacity is how many entries the ring keeps.
const Capacity = 200

// maxResponse bounds the stored response text per entry.
const maxResponse = 8 * 1024

// Entry is one provider round-trip.
type Entry struct {
	ID         int64           `json:"id"`
	Time       int64           `json:"time"` // unix seconds, request start
	ProviderID string          `json:"providerId"`
	Model      string          `json:"model"`
	URL        string          `json:"url"`
	Request    json.RawMessage `json:"request"` // wire body, no credentials
	Status     string          `json:"status"`  // "ok", "error", "canceled"
	Error      string          `json:"error"`   // set when Status != "ok"
	Response   string          `json:"response"`
	Truncated  bool            `json:"truncated"` // response text was capped
	DurationMs int64           `json:"durationMs"`
	Usage      *provider.Usage `json:"usage"`
}

// Log is a fixed-size ring of entries. The zero value is unusable; use
// New.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	nextID  int64
}

// New returns an empty log.
func New() *Log {
	return &Log{}
}

// Add appends an entry, evicting the oldest past capacity, and returns
// the assigned id. The response text is capped at 8KB.
func (l *Log) Add(e Entry) int64 {
	if len(e.Response) > maxResponse {
		e.Response = e.Response[:maxResponse]
		e.Truncated = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++
	e.ID = l.nextID
	l.entries = append(l.entries, e)
	if len(l.entries) > Capacity {
		l.entries = l.entries[len(l.entries)-Capacity:]
	}
	return e.ID
}

// Entries returns the log newest-first.
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	for i, e := range l.entries {
		out[len(out)-1-i] = e
	}
	return out
}

// Clear empties the log.
func (l *Log) Clear() {
	l.mu.Lock()
	l.entries = nil
	l.mu.Unlock()
}

// Service exposes the log to the frontend, bound as devlog.Service.
type Service struct {
	log *Log
}

// NewService returns a Service over log.
func NewService(log *Log) *Service {
	return &Service{log: log}
}

// Entries returns the log newest-first.
func (s *Service) Entries() []Entry {
	return s.log.Entries()
}

// Clear empties the log.
func (s *Service) Clear() {
	s.log.Clear()
}
