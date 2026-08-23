// Package character exposes card import and the characters library to
// the frontend as a Wails-bound service (dev spec §7).
package character

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"masque/internal/card"
	"masque/internal/store"
)

// Service is bound to the Wails frontend as character.Service.
type Service struct {
	store *store.Store
}

// NewService returns a Service backed by st.
func NewService(st *store.Store) *Service {
	return &Service{store: st}
}

// View is a character as the frontend renders it.
type View struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	HasAvatar bool   `json:"hasAvatar"`
	// HasLorebook flags cards whose lorebook is preserved but not yet
	// injected (spec §7: badge in dev mode).
	HasLorebook bool   `json:"hasLorebook"`
	Spec        string `json:"spec"`
}

func view(c store.Character) View {
	v := View{ID: c.ID, Name: c.Name, HasAvatar: c.HasAvatar}
	if parsed, err := card.ParseJSON([]byte(c.CardJSON)); err == nil {
		v.HasLorebook = parsed.HasLorebook
		v.Spec = parsed.Spec
	}
	return v
}

// List returns all characters, newest first.
func (s *Service) List() ([]View, error) {
	chars, err := s.store.ListCharacters()
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(chars))
	for _, c := range chars {
		// List rows omit card bodies; fetch per character for the
		// lorebook/spec badges. Libraries are small in M1.
		full, ok, err := s.store.GetCharacter(c.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			views = append(views, view(full))
		}
	}
	return views, nil
}

// Import parses a base64-encoded card file (PNG or JSON, V1/V2/V3) and
// stores it. The frontend sends base64 because Wails bindings marshal
// []byte awkwardly across the bridge.
func (s *Service) Import(dataB64, filename string) (View, error) {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return View{}, fmt.Errorf("decoding upload: %w", err)
	}
	parsed, avatar, err := card.Parse(data)
	if err != nil {
		return View{}, fmt.Errorf("importing %s: %w", filename, err)
	}
	stored, err := s.store.CreateCharacter(parsed.Name, string(parsed.Raw), avatar)
	if err != nil {
		return View{}, err
	}
	return view(stored), nil
}

// CreateForm is the minimal in-app creation form (spec §7: not a full
// card editor).
type CreateForm struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Personality string `json:"personality"`
	Scenario    string `json:"scenario"`
	Greeting    string `json:"greeting"`
	AvatarB64   string `json:"avatarB64"` // optional PNG upload
}

// Create builds a V3 card from the form and stores it.
func (s *Service) Create(form CreateForm) (View, error) {
	name := strings.TrimSpace(form.Name)
	if name == "" {
		return View{}, errors.New("character name is required")
	}
	raw, err := json.Marshal(map[string]any{
		"spec":         "chara_card_v3",
		"spec_version": "3.0",
		"data": map[string]any{
			"name":                      name,
			"description":               form.Description,
			"personality":               form.Personality,
			"scenario":                  form.Scenario,
			"first_mes":                 form.Greeting,
			"mes_example":               "",
			"system_prompt":             "",
			"post_history_instructions": "",
			"alternate_greetings":       []string{},
			"group_only_greetings":      []string{},
			"tags":                      []string{},
			"creator":                   "",
			"character_version":         "",
			"creator_notes":             "",
			"extensions":                map[string]any{},
		},
	})
	if err != nil {
		return View{}, fmt.Errorf("building card: %w", err)
	}
	var avatar []byte
	if form.AvatarB64 != "" {
		avatar, err = base64.StdEncoding.DecodeString(form.AvatarB64)
		if err != nil {
			return View{}, fmt.Errorf("decoding avatar: %w", err)
		}
		if !card.IsPNG(avatar) {
			return View{}, errors.New("avatar must be a PNG image")
		}
	}
	stored, err := s.store.CreateCharacter(name, string(raw), avatar)
	if err != nil {
		return View{}, err
	}
	return view(stored), nil
}

// Delete removes a character and all its chats. The frontend confirms
// first.
func (s *Service) Delete(id int64) error {
	return s.store.DeleteCharacter(id)
}

// Avatar returns the character's avatar as a data URI, or "" when it
// has none. Data URIs work identically under wails dev and production
// builds, unlike a custom asset route.
func (s *Service) Avatar(id int64) (string, error) {
	avatar, err := s.store.GetAvatar(id)
	if err != nil {
		return "", err
	}
	if len(avatar) == 0 {
		return "", nil
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(avatar), nil
}
