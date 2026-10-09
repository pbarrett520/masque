// Package character exposes card import, the characters library, and
// the card view/edit flow to the frontend as a Wails-bound service (dev
// spec §7).
package character

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"masque/internal/card"
	"masque/internal/starters"
	"masque/internal/store"
)

// Service is bound to the Wails frontend as character.Service.
type Service struct {
	store *store.Store

	mu  sync.Mutex
	ctx context.Context // Wails runtime context, for native dialogs; nil in tests
}

// NewService returns a Service backed by st.
func NewService(st *store.Store) *Service {
	return &Service{store: st}
}

// SetContext records the Wails runtime context once the app has
// started; SaveExport needs it for the native save dialog.
func (s *Service) SetContext(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

// View is a character as the library grid renders it.
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

// List returns all live characters, newest first.
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

// CardForm is every editable card field: the core set the creation
// form always shows plus the advanced ones. It is also the shape the
// edit form is pre-filled from (Detail.Form), so create, view and edit
// share one definition. Fields coming later (a recommended model per
// character, lorebook links) will be added here and stored under the
// card's extensions.masque object.
type CardForm struct {
	Name        string `json:"name"`
	Nickname    string `json:"nickname"`
	Description string `json:"description"`
	Personality string `json:"personality"`
	Scenario    string `json:"scenario"`
	// Greeting is the card's first_mes; AlternateGreetings seed the
	// first message's swipes.
	Greeting                string   `json:"greeting"`
	AlternateGreetings      []string `json:"alternateGreetings"`
	MesExample              string   `json:"mesExample"`
	SystemPrompt            string   `json:"systemPrompt"`
	PostHistoryInstructions string   `json:"postHistoryInstructions"`
	CreatorNotes            string   `json:"creatorNotes"`
	Tags                    []string `json:"tags"`
	// AvatarB64 is an optional PNG upload: "" keeps the current avatar
	// on update (none on create); RemoveAvatar clears it.
	AvatarB64    string `json:"avatarB64"`
	RemoveAvatar bool   `json:"removeAvatar"`
}

// CreateForm is kept as the M1.4 name of the creation payload.
type CreateForm = CardForm

// Detail is the full card for the detail and edit views.
type Detail struct {
	View
	Form      CardForm `json:"form"`
	ChatCount int      `json:"chatCount"`
	// Extra lists non-empty data fields the form doesn't cover (e.g.
	// creator, character_version, character_book), so the detail view
	// can show that they exist and are preserved.
	Extra     []string `json:"extra"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
}

// formFields are the data keys CardForm owns; everything else in the
// data object is carried through untouched by applyForm.
var formFields = map[string]bool{
	"name": true, "nickname": true, "description": true, "personality": true,
	"scenario": true, "first_mes": true, "alternate_greetings": true,
	"mes_example": true, "system_prompt": true, "post_history_instructions": true,
	"creator_notes": true, "tags": true,
}

// cardData splits a card into its V3 data object (V1: the top level).
func cardData(raw []byte) (map[string]any, error) {
	var env struct {
		Spec string          `json:"spec"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("reading card: %w", err)
	}
	body := raw
	if env.Spec != "" {
		body = env.Data
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("reading card data: %w", err)
	}
	if data == nil {
		data = map[string]any{}
	}
	return data, nil
}

func stringField(data map[string]any, key string) string {
	v, _ := data[key].(string)
	return v
}

func stringList(data map[string]any, key string) []string {
	items, _ := data[key].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// formFromCard pre-fills a CardForm from the stored card.
func formFromCard(raw []byte) (CardForm, []string, error) {
	data, err := cardData(raw)
	if err != nil {
		return CardForm{}, nil, err
	}
	f := CardForm{
		Name:                    stringField(data, "name"),
		Nickname:                stringField(data, "nickname"),
		Description:             stringField(data, "description"),
		Personality:             stringField(data, "personality"),
		Scenario:                stringField(data, "scenario"),
		Greeting:                stringField(data, "first_mes"),
		AlternateGreetings:      stringList(data, "alternate_greetings"),
		MesExample:              stringField(data, "mes_example"),
		SystemPrompt:            stringField(data, "system_prompt"),
		PostHistoryInstructions: stringField(data, "post_history_instructions"),
		CreatorNotes:            stringField(data, "creator_notes"),
		Tags:                    stringList(data, "tags"),
	}
	var extra []string
	for key, v := range data {
		if formFields[key] || isEmptyValue(v) {
			continue
		}
		extra = append(extra, key)
	}
	sortStrings(extra)
	return f, extra, nil
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

// applyForm writes the form's fields over the card's data object,
// leaving every other key (extensions, character_book, creator,
// character_version, group_only_greetings, …) exactly as stored, and
// returns the card as V3 JSON. raw may be nil to build a fresh card.
func applyForm(raw []byte, f CardForm) ([]byte, error) {
	data := map[string]any{}
	if len(raw) > 0 {
		var err error
		if data, err = cardData(raw); err != nil {
			return nil, err
		}
	}
	nonNil := func(ss []string) []string {
		if ss == nil {
			return []string{}
		}
		return ss
	}
	data["name"] = strings.TrimSpace(f.Name)
	data["nickname"] = f.Nickname
	data["description"] = f.Description
	data["personality"] = f.Personality
	data["scenario"] = f.Scenario
	data["first_mes"] = f.Greeting
	data["alternate_greetings"] = nonNil(f.AlternateGreetings)
	data["mes_example"] = f.MesExample
	data["system_prompt"] = f.SystemPrompt
	data["post_history_instructions"] = f.PostHistoryInstructions
	data["creator_notes"] = f.CreatorNotes
	data["tags"] = nonNil(f.Tags)
	// Required V3 keys a fresh card needs.
	for key, def := range map[string]any{
		"creator": "", "character_version": "",
		"group_only_greetings": []string{}, "extensions": map[string]any{},
	} {
		if _, ok := data[key]; !ok {
			data[key] = def
		}
	}
	return json.Marshal(map[string]any{
		"spec":         "chara_card_v3",
		"spec_version": "3.0",
		"data":         data,
	})
}

// decodeAvatar validates an optional base64 PNG upload.
func decodeAvatar(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, nil
	}
	avatar, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decoding avatar: %w", err)
	}
	if !card.IsPNG(avatar) {
		return nil, errors.New("avatar must be a PNG image")
	}
	return avatar, nil
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

// Create builds a V3 card from the form and stores it.
func (s *Service) Create(form CardForm) (View, error) {
	name := strings.TrimSpace(form.Name)
	if name == "" {
		return View{}, errors.New("character name is required")
	}
	raw, err := applyForm(nil, form)
	if err != nil {
		return View{}, fmt.Errorf("building card: %w", err)
	}
	avatar, err := decodeAvatar(form.AvatarB64)
	if err != nil {
		return View{}, err
	}
	stored, err := s.store.CreateCharacter(name, string(raw), avatar)
	if err != nil {
		return View{}, err
	}
	return view(stored), nil
}

// Get returns the full card for the detail and edit views.
func (s *Service) Get(id int64) (Detail, error) {
	c, ok, err := s.store.GetCharacter(id)
	if err != nil {
		return Detail{}, err
	}
	if !ok || c.DeletedAt != 0 {
		return Detail{}, fmt.Errorf("character %d does not exist", id)
	}
	form, extra, err := formFromCard([]byte(c.CardJSON))
	if err != nil {
		return Detail{}, fmt.Errorf("character %q has an unreadable card: %w", c.Name, err)
	}
	count, err := s.store.ChatCount(id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{View: view(c), Form: form, ChatCount: count, Extra: extra, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}, nil
}

// Update writes the form over the stored card (same validation as
// Create). Existing chats pick the change up on their next reply —
// the prompt re-reads the card every turn — while their history,
// including the first message, is untouched.
func (s *Service) Update(id int64, form CardForm) (View, error) {
	name := strings.TrimSpace(form.Name)
	if name == "" {
		return View{}, errors.New("character name is required")
	}
	c, ok, err := s.store.GetCharacter(id)
	if err != nil {
		return View{}, err
	}
	if !ok || c.DeletedAt != 0 {
		return View{}, fmt.Errorf("character %d does not exist", id)
	}
	raw, err := applyForm([]byte(c.CardJSON), form)
	if err != nil {
		return View{}, fmt.Errorf("building card: %w", err)
	}
	avatar, err := decodeAvatar(form.AvatarB64)
	if err != nil {
		return View{}, err
	}
	if err := s.store.UpdateCharacter(id, name, string(raw), avatar, form.RemoveAvatar); err != nil {
		return View{}, err
	}
	updated, _, err := s.store.GetCharacter(id)
	if err != nil {
		return View{}, err
	}
	return view(updated), nil
}

var copySuffix = regexp.MustCompile(` \(copy(?: \d+)?\)$`)

// Duplicate copies a character as "Name (copy)" (numbered if that name
// is taken), card and avatar included.
func (s *Service) Duplicate(id int64) (View, error) {
	c, ok, err := s.store.GetCharacter(id)
	if err != nil {
		return View{}, err
	}
	if !ok || c.DeletedAt != 0 {
		return View{}, fmt.Errorf("character %d does not exist", id)
	}
	existing, err := s.store.ListCharacters()
	if err != nil {
		return View{}, err
	}
	taken := map[string]bool{}
	for _, e := range existing {
		taken[e.Name] = true
	}
	base := copySuffix.ReplaceAllString(c.Name, "")
	name := base + " (copy)"
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s (copy %d)", base, n)
	}
	data, err := cardData([]byte(c.CardJSON))
	if err != nil {
		return View{}, err
	}
	data["name"] = name
	raw, err := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
	if err != nil {
		return View{}, fmt.Errorf("building card: %w", err)
	}
	avatar, err := s.store.GetAvatar(id)
	if err != nil {
		return View{}, err
	}
	stored, err := s.store.CreateCharacter(name, string(raw), avatar)
	if err != nil {
		return View{}, err
	}
	return view(stored), nil
}

// ExportJSON returns the card as V3 JSON text, the same format Import
// accepts, so a card round-trips unchanged.
func (s *Service) ExportJSON(id int64) (string, error) {
	c, ok, err := s.store.GetCharacter(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("character %d does not exist", id)
	}
	parsed, err := card.ParseJSON([]byte(c.CardJSON))
	if err != nil {
		return "", fmt.Errorf("character %q has an unreadable card: %w", c.Name, err)
	}
	out, err := card.ExportJSON(parsed)
	if err != nil {
		return "", err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out, "", "  "); err != nil {
		return string(out), nil
	}
	return pretty.String(), nil
}

// SaveExport opens the native save dialog and writes the card as JSON.
// Returns the chosen path, or "" if the user cancelled.
func (s *Service) SaveExport(id int64) (string, error) {
	text, err := s.ExportJSON(id)
	if err != nil {
		return "", err
	}
	c, _, err := s.store.GetCharacter(id)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil {
		return "", errors.New("save dialog unavailable before the app has started")
	}
	path, err := runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{
		Title:           "Export character",
		DefaultFilename: safeFilename(c.Name) + ".json",
		Filters:         []runtime.FileFilter{{DisplayName: "Character card (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if path == "" {
		return "", nil
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

var unsafeChars = regexp.MustCompile(`[^\w .-]+`)

func safeFilename(name string) string {
	out := strings.TrimSpace(unsafeChars.ReplaceAllString(name, "_"))
	if out == "" {
		return "character"
	}
	return out
}

// ChatCount reports how many chats use the character, for the delete
// confirmation.
func (s *Service) ChatCount(id int64) (int, error) {
	return s.store.ChatCount(id)
}

// Delete hides a character from the library. Its chats are kept: they
// stay readable, labelled as a deleted character, but can't continue.
// The frontend confirms first, stating the chat count.
func (s *Service) Delete(id int64) error {
	return s.store.SoftDeleteCharacter(id)
}

// RestoreStarters re-adds any bundled starter character the user has
// deleted, leaving edited ones alone. Returns the names added.
func (s *Service) RestoreStarters() ([]string, error) {
	names, err := starters.Restore(s.store)
	if err != nil {
		return nil, err
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
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
