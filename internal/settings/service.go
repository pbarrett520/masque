// Package settings exposes the settings table to the frontend as a
// Wails-bound service. Values are arbitrary JSON, stored as text.
package settings

import (
	"encoding/json"
	"fmt"

	"masque/internal/store"
)

// Service is bound to the Wails frontend as settings.Service.
type Service struct {
	store *store.Store
}

// NewService returns a Service backed by st.
func NewService(st *store.Store) *Service {
	return &Service{store: st}
}

// Get returns the value stored under key, or nil if unset. The value is
// whatever JSON-serializable value Set stored (string, number, bool,
// object, array).
func (s *Service) Get(key string) (any, error) {
	raw, ok, err := s.store.GetSetting(key)
	if err != nil || !ok {
		return nil, err
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("setting %q holds invalid JSON: %w", key, err)
	}
	return v, nil
}

// Set stores value under key. Passing nil (null) deletes the key.
func (s *Service) Set(key string, value any) error {
	if value == nil {
		return s.store.DeleteSetting(key)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding setting %q: %w", key, err)
	}
	return s.store.SetSetting(key, string(raw))
}

// DBPath returns the path of the database file, shown on the settings
// screen so users can find their data.
func (s *Service) DBPath() string {
	return s.store.Path()
}
