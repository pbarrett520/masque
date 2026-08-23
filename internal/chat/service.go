// Package chat orchestrates conversation turns (dev spec §10): persist
// the user message, build the prompt, stream the reply from the
// provider, forward deltas to the frontend as Wails events, and persist
// the result. Bound to the frontend as chat.Service.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"masque/internal/card"
	"masque/internal/prompt"
	"masque/internal/provider"
	"masque/internal/provider/anthropic"
	"masque/internal/provider/ollama"
	"masque/internal/provider/openai"
	"masque/internal/store"
)

// Settings keys the service reads/writes. The frontend shares
// user.display_name and the provider.* config keys (settings screen).
const (
	settingChatID       = "chat.dev_chat_id" // legacy M1.2 single chat; migrated to Ember on seed
	settingActiveChar   = "chat.active_character_id"
	settingEmberID      = "seed.ember_character_id"
	settingOllamaURL    = "provider.ollama.base_url"
	settingOpenAIURL    = "provider.openai.base_url"
	settingOpenAIKey    = "provider.openai.api_key"
	settingAnthropicURL = "provider.anthropic.base_url"
	settingAnthropicKey = "provider.anthropic.api_key"
	settingProvider     = "provider.default_id"
	settingModel        = "provider.default_model"
	settingDisplayName  = "user.display_name"
)

// showTimeout bounds the /api/show metadata call before each generation.
const showTimeout = 5 * time.Second

// EmitFunc delivers a Wails event to the frontend.
type EmitFunc func(event string, args ...any)

// contextWindower is the optional metadata probe (Ollama has it).
type contextWindower interface {
	ContextWindow(ctx context.Context, model string) (int, error)
}

// staticContextWindow is the fallback budget for providers without a
// metadata probe (spec §5: static table + user override for cloud; the
// user override arrives with dev mode in M1.7). Values are deliberately
// conservative — budgeting short only wastes headroom.
func staticContextWindow(providerID string) int {
	switch providerID {
	case "anthropic":
		return 200_000
	case "openai":
		return 16_384 // covers OpenRouter/LM Studio/llama.cpp defaults
	default:
		return 0 // prompt.Build applies its own default
	}
}

// Service is bound to the Wails frontend as chat.Service.
type Service struct {
	store       *store.Store
	emit        EmitFunc
	providerFor func(id string) (provider.Provider, error) // test seam

	mu       sync.Mutex
	inflight map[int64]context.CancelFunc
}

// NewService returns a Service backed by st that emits frontend events
// through emit.
func NewService(st *store.Store, emit EmitFunc) *Service {
	s := &Service{
		store:    st,
		emit:     emit,
		inflight: map[int64]context.CancelFunc{},
	}
	s.providerFor = s.buildProvider
	return s
}

// buildProvider constructs the provider for id from current settings.
// Providers are stateless (spec §4): keys and base URLs are read fresh
// on every call so settings changes apply to the next request.
func (s *Service) buildProvider(id string) (provider.Provider, error) {
	switch id {
	case "", "ollama":
		return ollama.New(s.stringSetting(settingOllamaURL)), nil
	case "openai":
		return openai.New(s.stringSetting(settingOpenAIURL), s.stringSetting(settingOpenAIKey)), nil
	case "anthropic":
		return anthropic.New(s.stringSetting(settingAnthropicURL), s.stringSetting(settingAnthropicKey)), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", id)
	}
}

// ProviderInfo describes one selectable provider for the frontend.
type ProviderInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// NeedsKey is true when the provider is unusable until an API key
	// is configured in settings.
	NeedsKey bool `json:"needsKey"`
}

// Providers lists the selectable providers in display order.
func (s *Service) Providers() []ProviderInfo {
	return []ProviderInfo{
		{ID: "ollama", Label: "Ollama"},
		{ID: "openai", Label: "OpenAI-compatible", NeedsKey: false},
		{ID: "anthropic", Label: "Anthropic", NeedsKey: s.stringSetting(settingAnthropicKey) == ""},
	}
}

// MessageView is a message as the frontend renders it.
type MessageView struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// State is what the chat screen needs to render. A zero ChatID means
// there is nothing to resume — the frontend shows the characters screen.
type State struct {
	ChatID        int64         `json:"chatId"`
	CharacterID   int64         `json:"characterId"`
	CharacterName string        `json:"characterName"`
	ProviderID    string        `json:"providerId"`
	Model         string        `json:"model"`
	Messages      []MessageView `json:"messages"`
}

// DonePayload accompanies the chat:{id}:done event.
type DonePayload struct {
	MessageID int64           `json:"messageId"`
	Content   string          `json:"content"`
	Truncated bool            `json:"truncated"`
	Usage     *provider.Usage `json:"usage"`
}

// StartChat resumes the last active character's chat on app launch,
// seeding the starter character (Ember) on first run. A zero-ChatID
// State means no character to resume — show the characters screen.
func (s *Service) StartChat() (State, error) {
	if err := s.ensureStarterCharacter(); err != nil {
		return State{}, err
	}
	id := s.int64Setting(settingActiveChar)
	if id == 0 {
		id = s.int64Setting(settingEmberID)
	}
	if id == 0 {
		return State{}, nil
	}
	if _, ok, err := s.store.GetCharacter(id); err != nil {
		return State{}, err
	} else if !ok {
		return State{}, nil // deleted since last run
	}
	return s.OpenChat(id)
}

// OpenChat opens (resuming or creating) the chat for a character and
// makes it the active one. New chats are seeded with the card's
// greeting (first_mes, spec §5).
func (s *Service) OpenChat(characterID int64) (State, error) {
	char, ok, err := s.store.GetCharacter(characterID)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, fmt.Errorf("character %d does not exist", characterID)
	}
	parsed, err := card.ParseJSON([]byte(char.CardJSON))
	if err != nil {
		return State{}, fmt.Errorf("character %q has an unreadable card: %w", char.Name, err)
	}

	// Serialized: StrictMode double-mounts must not create two chats.
	s.mu.Lock()
	chat, found, err := s.store.LatestChatForCharacter(characterID)
	if err == nil && !found {
		chat, err = s.store.CreateChatForCharacter(characterID, parsed.DisplayName(), s.defaultProvider(), s.defaultModel())
		if err == nil && parsed.FirstMes != "" {
			greeting := prompt.Substitute(parsed.FirstMes, parsed.DisplayName(), s.persona().Name)
			_, err = s.store.AppendMessage(chat.ID, provider.RoleAssistant, greeting, prompt.EstimateTokens(greeting), false)
		}
	}
	s.mu.Unlock()
	if err != nil {
		return State{}, err
	}
	if err := s.store.SetSetting(settingActiveChar, fmt.Sprintf("%d", characterID)); err != nil {
		return State{}, err
	}

	msgs, err := s.store.ActiveMessages(chat.ID)
	if err != nil {
		return State{}, err
	}
	providerID := chat.ProviderID
	if providerID == "" {
		providerID = "ollama"
	}
	state := State{
		ChatID:        chat.ID,
		CharacterID:   characterID,
		CharacterName: parsed.DisplayName(),
		ProviderID:    providerID,
		Model:         chat.Model,
		Messages:      make([]MessageView, 0, len(msgs)),
	}
	for _, m := range msgs {
		state.Messages = append(state.Messages, MessageView{
			ID: m.ID, Role: m.Role, Content: m.Content, Truncated: m.Truncated,
		})
	}
	return state, nil
}

// ListModels returns the models offered by providerID's endpoint.
func (s *Service) ListModels(providerID string) ([]provider.ModelInfo, error) {
	p, err := s.providerFor(providerID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.ListModels(ctx)
}

// Health probes providerID's endpoint; the error string (or "") is
// returned rather than an error so the frontend can render it directly.
func (s *Service) Health(providerID string) string {
	p, err := s.providerFor(providerID)
	if err != nil {
		return err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		return err.Error()
	}
	return ""
}

// SetModel records the provider+model for a chat and as the default for
// new chats. This is the mid-chat switch (spec §12, M1.3): the next
// Send simply uses the chat's new provider.
func (s *Service) SetModel(chatID int64, providerID, model string) error {
	if model == "" {
		return errors.New("model must not be empty")
	}
	if _, err := s.providerFor(providerID); err != nil {
		return err
	}
	if providerID == "" {
		providerID = "ollama"
	}
	if err := s.store.SetChatModel(chatID, providerID, model); err != nil {
		return err
	}
	for key, value := range map[string]string{settingProvider: providerID, settingModel: model} {
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encoding %s setting: %w", key, err)
		}
		if err := s.store.SetSetting(key, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

// Send persists text as the user's message and starts generating the
// reply in the background. It returns the persisted user message; the
// reply arrives via chat:{id}:delta / done / error events.
func (s *Service) Send(chatID int64, text string) (MessageView, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return MessageView{}, errors.New("message is empty")
	}
	chat, ok, err := s.store.GetChat(chatID)
	if err != nil {
		return MessageView{}, err
	}
	if !ok {
		return MessageView{}, fmt.Errorf("chat %d does not exist", chatID)
	}
	if chat.Model == "" {
		return MessageView{}, errors.New("no model selected")
	}

	// Reserve the in-flight slot before persisting so a double-send
	// can't race (one generation per chat, spec §10).
	genCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if _, busy := s.inflight[chatID]; busy {
		s.mu.Unlock()
		cancel()
		return MessageView{}, errors.New("a reply is already being generated for this chat")
	}
	s.inflight[chatID] = cancel
	s.mu.Unlock()

	userMsg, err := s.store.AppendMessage(chatID, provider.RoleUser, text, prompt.EstimateTokens(text), false)
	if err != nil {
		s.clearInflight(chatID)
		return MessageView{}, err
	}

	go s.generate(genCtx, chat)

	return MessageView{ID: userMsg.ID, Role: userMsg.Role, Content: userMsg.Content}, nil
}

// Stop cancels the in-flight generation for a chat, if any. The partial
// reply is persisted, marked truncated, by the generation goroutine.
func (s *Service) Stop(chatID int64) {
	s.mu.Lock()
	cancel, ok := s.inflight[chatID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func (s *Service) clearInflight(chatID int64) {
	s.mu.Lock()
	if cancel, ok := s.inflight[chatID]; ok {
		cancel()
		delete(s.inflight, chatID)
	}
	s.mu.Unlock()
}

// generate runs one turn: build the prompt, stream the reply, persist
// the outcome, and emit events. It owns the in-flight slot.
func (s *Service) generate(ctx context.Context, chat store.Chat) {
	defer s.clearInflight(chat.ID)

	fail := func(err error) {
		s.emit(fmt.Sprintf("chat:%d:error", chat.ID), err.Error())
	}

	history, err := s.store.ActiveMessages(chat.ID)
	if err != nil {
		fail(err)
		return
	}
	msgs := make([]provider.Message, 0, len(history))
	for _, m := range history {
		msgs = append(msgs, provider.Message{Role: m.Role, Content: m.Content})
	}

	p, err := s.providerFor(chat.ProviderID)
	if err != nil {
		fail(err)
		return
	}
	contextWindow := staticContextWindow(chat.ProviderID)
	if cw, ok := p.(contextWindower); ok {
		showCtx, cancel := context.WithTimeout(ctx, showTimeout)
		if n, err := cw.ContextWindow(showCtx, chat.Model); err == nil {
			contextWindow = n
		}
		cancel()
	}

	character, err := s.characterForChat(chat.ID)
	if err != nil {
		fail(err)
		return
	}
	built := prompt.Build(prompt.Input{
		Character:     character,
		Persona:       s.persona(),
		History:       msgs,
		ContextWindow: contextWindow,
	})

	events, err := p.ChatStream(ctx, provider.ChatRequest{
		Model:    chat.Model,
		Messages: built.Messages,
		System:   built.System,
	})
	if err != nil {
		fail(err)
		return
	}

	var sb strings.Builder
	deltaEvent := fmt.Sprintf("chat:%d:delta", chat.ID)
	for ev := range events {
		if ev.Delta != "" {
			sb.WriteString(ev.Delta)
			s.emit(deltaEvent, ev.Delta)
		}
		switch {
		case ev.Done:
			s.finish(chat.ID, sb.String(), false, ev.Usage)
		case ev.Err != nil:
			canceled := errors.Is(ev.Err, context.Canceled)
			if sb.Len() > 0 {
				// Persist what streamed before the cut, marked truncated.
				s.finish(chat.ID, sb.String(), true, nil)
			} else if canceled {
				s.finish(chat.ID, "", true, nil)
			}
			if !canceled {
				fail(ev.Err)
			}
		}
	}
}

// finish persists the assistant reply (unless empty) and emits the done
// event.
func (s *Service) finish(chatID int64, content string, truncated bool, usage *provider.Usage) {
	payload := DonePayload{Content: content, Truncated: truncated, Usage: usage}
	if content != "" {
		tokens := prompt.EstimateTokens(content)
		if usage != nil && usage.CompletionTokens > 0 {
			tokens = usage.CompletionTokens
		}
		msg, err := s.store.AppendMessage(chatID, provider.RoleAssistant, content, tokens, truncated)
		if err != nil {
			s.emit(fmt.Sprintf("chat:%d:error", chatID), err.Error())
			return
		}
		payload.MessageID = msg.ID
	}
	s.emit(fmt.Sprintf("chat:%d:done", chatID), payload)
}

// ensureStarterCharacter seeds Ember as a real character row on first
// run (so the characters screen is never empty) and adopts the legacy
// M1.2 dev chat into her. Runs once; deleting Ember afterwards is
// respected. Serialized against StrictMode double-mounts.
func (s *Service) ensureStarterCharacter() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok, err := s.store.GetSetting(settingEmberID); err != nil {
		return err
	} else if ok {
		return nil
	}
	char, err := s.store.CreateCharacter(hardcodedCharacter.Name, starterCardJSON(), nil)
	if err != nil {
		return fmt.Errorf("seeding starter character: %w", err)
	}
	// Adopt the M1.2-era dev chat, which predates character rows.
	if legacyID := s.int64Setting(settingChatID); legacyID != 0 {
		if owner, err := s.store.ChatCharacterID(legacyID); err == nil && owner == 0 {
			if _, found, err := s.store.GetChat(legacyID); err == nil && found {
				if err := s.store.LinkChatCharacter(legacyID, char.ID); err != nil {
					return err
				}
			}
		}
	}
	return s.store.SetSetting(settingEmberID, fmt.Sprintf("%d", char.ID))
}

// characterForChat loads the prompt view of a chat's character. Chats
// without one (pre-seed edge case) fall back to the built-in card.
func (s *Service) characterForChat(chatID int64) (prompt.Character, error) {
	charID, err := s.store.ChatCharacterID(chatID)
	if err != nil {
		return prompt.Character{}, err
	}
	if charID == 0 {
		return hardcodedCharacter, nil
	}
	char, ok, err := s.store.GetCharacter(charID)
	if err != nil {
		return prompt.Character{}, err
	}
	if !ok {
		return prompt.Character{}, fmt.Errorf("character %d for chat %d is missing", charID, chatID)
	}
	parsed, err := card.ParseJSON([]byte(char.CardJSON))
	if err != nil {
		return prompt.Character{}, fmt.Errorf("character %q has an unreadable card: %w", char.Name, err)
	}
	return prompt.Character{
		Name:         parsed.DisplayName(),
		Description:  parsed.Description,
		Personality:  parsed.Personality,
		Scenario:     parsed.Scenario,
		SystemPrompt: parsed.SystemPrompt,
		FirstMes:     parsed.FirstMes,
	}, nil
}

// persona builds the M1.2 persona from the display-name setting;
// personas proper land in M1.5.
func (s *Service) persona() prompt.Persona {
	name := s.stringSetting(settingDisplayName)
	if name == "" {
		name = "User"
	}
	return prompt.Persona{Name: name}
}

func (s *Service) defaultProvider() string {
	if id := s.stringSetting(settingProvider); id != "" {
		return id
	}
	return "ollama"
}

func (s *Service) defaultModel() string {
	return s.stringSetting(settingModel)
}

// int64Setting reads a JSON-number (or numeric-string) setting,
// returning 0 when unset or malformed.
func (s *Service) int64Setting(key string) int64 {
	raw, ok, err := s.store.GetSetting(key)
	if err != nil || !ok {
		return 0
	}
	var v int64
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return 0
	}
	return v
}

// stringSetting reads a JSON-string setting, returning "" when unset or
// malformed.
func (s *Service) stringSetting(key string) string {
	raw, ok, err := s.store.GetSetting(key)
	if err != nil || !ok {
		return ""
	}
	var v string
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return ""
	}
	return v
}
