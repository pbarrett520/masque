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
	"masque/internal/devlog"
	"masque/internal/presets"
	"masque/internal/prompt"
	"masque/internal/provider"
	"masque/internal/provider/anthropic"
	"masque/internal/provider/ollama"
	"masque/internal/provider/openai"
	"masque/internal/starters"
	"masque/internal/store"
)

// Settings keys the service reads/writes. The frontend shares
// user.display_name and the provider.* config keys (settings screen).
const (
	settingChatID      = "chat.dev_chat_id" // legacy M1.2 single chat; migrated to Ember on seed
	settingActiveChar  = "chat.active_character_id"
	settingActiveChat  = "chat.active_chat_id"
	settingOllamaURL   = "provider.ollama.base_url"
	settingProvider    = "provider.default_id"
	settingModel       = "provider.default_model"
	settingDisplayName = "user.display_name"

	// Dev-mode endpoint config (§9). Timeout bounds a whole generation;
	// streaming=false asks providers for unstreamed completions. The
	// context-window overrides replace the static cloud table entries.
	settingTimeoutSecs  = "dev.request_timeout_secs"
	settingStreaming    = "dev.streaming"
	settingOpenAICtxWin = "provider.openai.context_window"
	settingClaudeCtxWin = "provider.anthropic.context_window"
)

// showTimeout bounds the /api/show metadata call before each generation.
const showTimeout = 5 * time.Second

// Per-provider settings keys. Cloud presets (internal/presets) and the
// Ollama entry share the naming: provider.<id>.<field>.
func keySetting(providerID string) string       { return "provider." + providerID + ".api_key" }
func urlSetting(providerID string) string       { return "provider." + providerID + ".base_url" }
func lastModelSetting(providerID string) string { return "provider." + providerID + ".last_model" }

// EmitFunc delivers a Wails event to the frontend.
type EmitFunc func(event string, args ...any)

// contextWindower is the optional metadata probe (Ollama has it).
type contextWindower interface {
	ContextWindow(ctx context.Context, model string) (int, error)
}

// staticContextWindow is the fallback budget for providers without a
// metadata probe (spec §5: static table + user override for cloud).
// The dev-mode override settings win when set; the table values are
// deliberately conservative — budgeting short only wastes headroom.
func (s *Service) staticContextWindow(providerID string) int {
	preset, ok := s.presets.Find(providerID)
	if !ok {
		return 0 // Ollama: prompt.Build applies its own default
	}
	switch preset.Format {
	case presets.FormatAnthropic:
		if n := s.int64Setting(settingClaudeCtxWin); n > 0 {
			return int(n)
		}
		return 200_000
	default:
		if n := s.int64Setting(settingOpenAICtxWin); n > 0 {
			return int(n)
		}
		return 16_384 // covers OpenRouter/LM Studio/llama.cpp defaults
	}
}

// Service is bound to the Wails frontend as chat.Service.
type Service struct {
	store       *store.Store
	emit        EmitFunc
	log         *devlog.Log                                // nil disables request logging
	presets     *presets.Registry                          // cloud provider presets
	providerFor func(id string) (provider.Provider, error) // test seam

	mu       sync.Mutex
	inflight map[int64]context.CancelFunc
}

// NewService returns a Service backed by st that emits frontend events
// through emit. log may be nil to disable dev-mode request logging.
func NewService(st *store.Store, emit EmitFunc, log *devlog.Log) *Service {
	s := &Service{
		store:    st,
		emit:     emit,
		log:      log,
		presets:  presets.MustLoad(),
		inflight: map[int64]context.CancelFunc{},
	}
	s.providerFor = s.buildProvider
	return s
}

// buildProvider constructs the provider for id from current settings.
// Providers are stateless (spec §4): keys and base URLs are read fresh
// on every call so settings changes apply to the next request. Cloud
// ids are preset ids: the preset supplies the format and base URL, the
// settings supply the key (and, for custom presets, the base URL; a
// provider.<id>.base_url setting overrides any preset's URL).
func (s *Service) buildProvider(id string) (provider.Provider, error) {
	if id == "" || id == "ollama" {
		return ollama.New(s.stringSetting(settingOllamaURL)), nil
	}
	preset, ok := s.presets.Find(id)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", id)
	}
	baseURL := s.stringSetting(urlSetting(id))
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	if baseURL == "" {
		return nil, fmt.Errorf("%s: set a base URL in Settings first", preset.Label)
	}
	key := s.stringSetting(keySetting(id))
	switch preset.Format {
	case presets.FormatAnthropic:
		return anthropic.New(baseURL, key), nil
	default:
		return openai.New(baseURL, key), nil
	}
}

// ProviderInfo describes one selectable provider for the frontend.
type ProviderInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Format is "ollama", or the preset's API format.
	Format string `json:"format"`
	// Custom presets take a user-supplied base URL and an optional key.
	Custom bool `json:"custom"`
	// KeyURL is where the user creates an API key ("" for local).
	KeyURL string `json:"keyUrl"`
	// BaseURL is the effective endpoint: the preset's, or the user's
	// override/custom URL.
	BaseURL string `json:"baseUrl"`
	// Configured is true when the provider can be used as set up now:
	// Ollama always, cloud presets once a key is saved, custom presets
	// once a base URL is saved.
	Configured bool `json:"configured"`
	// NeedsKey is true when the provider is unusable until an API key
	// is saved.
	NeedsKey bool `json:"needsKey"`
}

// Providers lists every selectable provider: Ollama first, then the
// cloud presets in registry order. The frontend filters to Configured
// ones for the chat picker and shows all in Settings.
func (s *Service) Providers() []ProviderInfo {
	out := []ProviderInfo{{ID: "ollama", Label: "Ollama", Format: "ollama", Configured: true}}
	for _, p := range s.presets.Presets {
		info := ProviderInfo{ID: p.ID, Label: p.Label, Format: p.Format, Custom: p.Custom, KeyURL: p.KeyURL}
		info.BaseURL = s.stringSetting(urlSetting(p.ID))
		if info.BaseURL == "" {
			info.BaseURL = p.BaseURL
		}
		hasKey := s.stringSetting(keySetting(p.ID)) != ""
		if p.Custom {
			info.Configured = info.BaseURL != ""
		} else {
			info.Configured = hasKey
			info.NeedsKey = !hasKey
		}
		out = append(out, info)
	}
	return out
}

// Catalog lists what the model picker shows for providerID: the
// endpoint's chat-capable models, the preset's recommended ones, and
// the model to preselect (dev spec §9, "normie mode just works"). A
// successful listing doubles as the key check; failures are reported
// in the catalog rather than as an error so the pinned models remain
// pickable when a host has no list endpoint.
func (s *Service) Catalog(providerID string) (presets.Catalog, error) {
	if providerID == "" {
		providerID = "ollama"
	}
	p, err := s.providerFor(providerID)
	if err != nil {
		return presets.Catalog{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	listed, listErr := p.ListModels(ctx)
	last := s.stringSetting(lastModelSetting(providerID))

	preset, ok := s.presets.Find(providerID)
	if !ok {
		// Ollama: the provider already filters to chat-capable models
		// and there is no curated roster here (that is the starter
		// list in ollamamgr). Recommended stays empty.
		c := presets.Catalog{ProviderID: providerID, Recommended: []presets.Model{}, All: []presets.Model{}}
		if listErr != nil {
			c.ListError = listErr.Error()
			return c, nil
		}
		c.Listed = true
		for _, m := range listed {
			c.All = append(c.All, presets.Model{ID: m.ID, Label: m.ID})
			if m.ID == last {
				c.Default = last
			}
		}
		if c.Default == "" && len(c.All) > 0 {
			c.Default = c.All[0].ID
		}
		return c, nil
	}
	return s.presets.BuildCatalog(preset, listed, listErr, last), nil
}

// MessageView is a message as the frontend renders it. SwipeCount > 1
// means the message has regeneration siblings to swipe between;
// SwipeIndex is 1-based.
type MessageView struct {
	ID         int64  `json:"id"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated"`
	SwipeIndex int    `json:"swipeIndex"`
	SwipeCount int    `json:"swipeCount"`
}

// State is what the chat screen needs to render. A zero ChatID means
// there is nothing to resume — the frontend shows the characters screen.
type State struct {
	ChatID        int64  `json:"chatId"`
	CharacterID   int64  `json:"characterId"`
	CharacterName string `json:"characterName"`
	// CharacterDeleted means the chat is read-only: its character was
	// deleted, history stays, nothing new can be generated.
	CharacterDeleted bool          `json:"characterDeleted"`
	ProviderID       string        `json:"providerId"`
	Model            string        `json:"model"`
	Messages         []MessageView `json:"messages"`
}

// DonePayload accompanies the chat:{id}:done event.
type DonePayload struct {
	MessageID int64           `json:"messageId"`
	Content   string          `json:"content"`
	Truncated bool            `json:"truncated"`
	Usage     *provider.Usage `json:"usage"`
}

// StartChat resumes the last active chat on app launch, seeding the
// starter character (Ember) on first run. A zero-ChatID State means
// nothing to resume — show the characters screen.
func (s *Service) StartChat() (State, error) {
	if err := s.ensureStarterCharacter(); err != nil {
		return State{}, err
	}
	if chatID := s.int64Setting(settingActiveChat); chatID != 0 {
		if state, err := s.OpenChatByID(chatID); err == nil {
			return state, nil
		}
		// Deleted since last run: fall back to the character path.
	}
	id := s.int64Setting(settingActiveChar)
	if id == 0 {
		return State{}, nil
	}
	if c, ok, err := s.store.GetCharacter(id); err != nil {
		return State{}, err
	} else if !ok || c.DeletedAt != 0 {
		return State{}, nil // deleted since last run
	}
	return s.OpenChat(id)
}

// OpenChat opens the most recent chat for a character (creating the
// first one if none exists) and makes it the active one.
func (s *Service) OpenChat(characterID int64) (State, error) {
	char, parsed, err := s.loadCharacter(characterID)
	if err != nil {
		return State{}, err
	}
	// Serialized: StrictMode double-mounts must not create two chats.
	s.mu.Lock()
	chat, found, err := s.store.LatestChatForCharacter(characterID)
	if err == nil && !found {
		chat, err = s.createChat(char.ID, parsed)
	}
	s.mu.Unlock()
	if err != nil {
		return State{}, err
	}
	return s.activate(chat, characterID, parsed.DisplayName())
}

// NewChat starts a fresh chat with a character, seeded with the card's
// greeting, regardless of existing chats.
func (s *Service) NewChat(characterID int64) (State, error) {
	char, parsed, err := s.loadCharacter(characterID)
	if err != nil {
		return State{}, err
	}
	chat, err := s.createChat(char.ID, parsed)
	if err != nil {
		return State{}, err
	}
	return s.activate(chat, characterID, parsed.DisplayName())
}

// OpenChatByID resumes a specific chat from the chat list.
func (s *Service) OpenChatByID(chatID int64) (State, error) {
	chat, ok, err := s.store.GetChat(chatID)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, fmt.Errorf("chat %d does not exist", chatID)
	}
	characterID, err := s.store.ChatCharacterID(chatID)
	if err != nil {
		return State{}, err
	}
	if characterID == 0 {
		return State{}, fmt.Errorf("chat %d has no character", chatID)
	}
	// Resuming must work for deleted characters too (read-only chat),
	// so read the row directly rather than through loadCharacter.
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
	return s.activate(chat, characterID, parsed.DisplayName())
}

// ListChats returns every chat for the chat list, most recent first.
func (s *Service) ListChats() ([]store.ChatListItem, error) {
	return s.store.ListChats()
}

// DeleteChat removes a chat and its messages.
func (s *Service) DeleteChat(chatID int64) error {
	s.Stop(chatID)
	return s.store.DeleteChat(chatID)
}

// errDeletedCharacter is returned for any action that would generate
// new turns for a deleted character's chat.
var errDeletedCharacter = errors.New("this character was deleted; the chat can be read but not continued")

// chatContinuable fails when the chat's character was deleted.
func (s *Service) chatContinuable(chatID int64) error {
	charID, err := s.store.ChatCharacterID(chatID)
	if err != nil || charID == 0 {
		return err
	}
	c, ok, err := s.store.GetCharacter(charID)
	if err != nil {
		return err
	}
	if ok && c.DeletedAt != 0 {
		return errDeletedCharacter
	}
	return nil
}

// loadCharacter fetches a character row and its parsed card.
func (s *Service) loadCharacter(characterID int64) (store.Character, card.Card, error) {
	char, ok, err := s.store.GetCharacter(characterID)
	if err != nil {
		return store.Character{}, card.Card{}, err
	}
	if !ok {
		return store.Character{}, card.Card{}, fmt.Errorf("character %d does not exist", characterID)
	}
	if char.DeletedAt != 0 {
		return store.Character{}, card.Card{}, errDeletedCharacter
	}
	parsed, err := card.ParseJSON([]byte(char.CardJSON))
	if err != nil {
		return store.Character{}, card.Card{}, fmt.Errorf("character %q has an unreadable card: %w", char.Name, err)
	}
	return char, parsed, nil
}

// createChat inserts a chat seeded with the card's greeting. The
// greeting and any alternate_greetings form a swipe group (V2 spec:
// alternates MUST be offered as swipes on the first message).
func (s *Service) createChat(characterID int64, parsed card.Card) (store.Chat, error) {
	chat, err := s.store.CreateChatForCharacter(characterID, parsed.DisplayName(), s.defaultProvider(), s.defaultModel())
	if err != nil {
		return store.Chat{}, err
	}
	if parsed.FirstMes == "" {
		return chat, nil
	}
	sub := func(text string) string {
		return prompt.Substitute(text, parsed.DisplayName(), s.persona().Name)
	}
	greeting := sub(parsed.FirstMes)
	first, err := s.store.AppendMessage(chat.ID, provider.RoleAssistant, greeting, prompt.EstimateTokens(greeting), false)
	if err != nil {
		return store.Chat{}, err
	}
	if len(parsed.AlternateGreetings) > 0 {
		if err := s.store.SetSwipeGroup(first.ID, first.ID); err != nil {
			return store.Chat{}, err
		}
		for _, alt := range parsed.AlternateGreetings {
			if strings.TrimSpace(alt) == "" {
				continue
			}
			text := sub(alt)
			if _, err := s.store.AppendSwipe(chat.ID, provider.RoleAssistant, text, prompt.EstimateTokens(text), false, first.ID, false); err != nil {
				return store.Chat{}, err
			}
		}
	}
	return chat, nil
}

// activate marks a chat as the resume target and builds its State.
func (s *Service) activate(chat store.Chat, characterID int64, characterName string) (State, error) {
	if err := s.store.SetSetting(settingActiveChar, fmt.Sprintf("%d", characterID)); err != nil {
		return State{}, err
	}
	if err := s.store.SetSetting(settingActiveChat, fmt.Sprintf("%d", chat.ID)); err != nil {
		return State{}, err
	}
	return s.stateForChat(chat, characterID, characterName)
}

// stateForChat assembles the render state, including swipe positions
// for grouped messages.
func (s *Service) stateForChat(chat store.Chat, characterID int64, characterName string) (State, error) {
	msgs, err := s.store.ActiveMessages(chat.ID)
	if err != nil {
		return State{}, err
	}
	providerID := chat.ProviderID
	if providerID == "" {
		providerID = "ollama"
	}
	state := State{
		ChatID:           chat.ID,
		CharacterID:      characterID,
		CharacterName:    characterName,
		CharacterDeleted: s.chatContinuable(chat.ID) != nil,
		ProviderID:       providerID,
		Model:            chat.Model,
		Messages:         make([]MessageView, 0, len(msgs)),
	}
	for _, m := range msgs {
		mv := MessageView{ID: m.ID, Role: m.Role, Content: m.Content, Truncated: m.Truncated}
		if m.SwipeGroup != 0 {
			swipes, err := s.store.SwipesInGroup(chat.ID, m.SwipeGroup)
			if err != nil {
				return State{}, err
			}
			mv.SwipeCount = len(swipes)
			for i, sw := range swipes {
				if sw.ID == m.ID {
					mv.SwipeIndex = i + 1
				}
			}
		}
		state.Messages = append(state.Messages, mv)
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
	for key, value := range map[string]string{
		settingProvider:              providerID,
		settingModel:                 model,
		lastModelSetting(providerID): model, // restored by Catalog next time
	} {
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
	if err := s.chatContinuable(chatID); err != nil {
		return MessageView{}, err
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

	go s.generate(genCtx, chat, 0)

	return MessageView{ID: userMsg.ID, Role: userMsg.Role, Content: userMsg.Content}, nil
}

// Regenerate replaces the last assistant reply with a fresh one: the
// old reply becomes an inactive swipe sibling (spec §9: regenerate
// creates a swipe). An in-flight generation is canceled first
// (spec §10: regenerate cancels then restarts).
func (s *Service) Regenerate(chatID int64) error {
	chat, ok, err := s.store.GetChat(chatID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("chat %d does not exist", chatID)
	}
	if err := s.chatContinuable(chatID); err != nil {
		return err
	}
	if chat.Model == "" {
		return errors.New("no model selected")
	}

	// Cancel any in-flight generation and wait for its slot to clear.
	s.Stop(chatID)
	deadline := time.Now().Add(3 * time.Second)
	for s.isBusy(chatID) {
		if time.Now().After(deadline) {
			return errors.New("previous generation is still shutting down; try again")
		}
		time.Sleep(25 * time.Millisecond)
	}

	msgs, err := s.store.ActiveMessages(chatID)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return errors.New("nothing to regenerate")
	}
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant {
		return errors.New("the last message is not an assistant reply")
	}
	hasUserTurn := false
	for _, m := range msgs[:len(msgs)-1] {
		if m.Role == provider.RoleUser {
			hasUserTurn = true
			break
		}
	}
	if !hasUserTurn {
		return errors.New("greetings can't be regenerated — swipe between them instead")
	}

	group := last.SwipeGroup
	if group == 0 {
		group = last.ID
		if err := s.store.SetSwipeGroup(last.ID, group); err != nil {
			return err
		}
	}

	genCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if _, busy := s.inflight[chatID]; busy {
		s.mu.Unlock()
		cancel()
		return errors.New("a reply is already being generated for this chat")
	}
	s.inflight[chatID] = cancel
	s.mu.Unlock()

	if err := s.store.DeactivateMessage(last.ID); err != nil {
		s.clearInflight(chatID)
		return err
	}

	go s.generate(genCtx, chat, group)
	return nil
}

// Swipe activates the previous (direction -1) or next (+1) sibling of
// a swiped message and returns the refreshed chat state.
func (s *Service) Swipe(chatID, messageID int64, direction int) (State, error) {
	if direction != 1 && direction != -1 {
		return State{}, errors.New("direction must be -1 or 1")
	}
	if s.isBusy(chatID) {
		return State{}, errors.New("wait for the current reply to finish")
	}
	msg, ok, err := s.store.GetMessage(messageID)
	if err != nil {
		return State{}, err
	}
	if !ok || msg.ChatID != chatID {
		return State{}, fmt.Errorf("message %d not found in chat %d", messageID, chatID)
	}
	if msg.SwipeGroup == 0 {
		return State{}, errors.New("message has no swipes")
	}
	swipes, err := s.store.SwipesInGroup(chatID, msg.SwipeGroup)
	if err != nil {
		return State{}, err
	}
	current := -1
	for i, sw := range swipes {
		if sw.ID == messageID {
			current = i
		}
	}
	target := current + direction
	if current < 0 || target < 0 || target >= len(swipes) {
		return State{}, errors.New("no more swipes in that direction")
	}
	if err := s.store.ActivateSwipe(chatID, msg.SwipeGroup, swipes[target].ID); err != nil {
		return State{}, err
	}
	return s.stateForChatID(chatID)
}

// EditMessage rewrites a message's content in place (spec §9:
// edit-any-message).
func (s *Service) EditMessage(chatID, messageID int64, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("message can't be empty")
	}
	if s.isBusy(chatID) {
		return errors.New("wait for the current reply to finish")
	}
	msg, ok, err := s.store.GetMessage(messageID)
	if err != nil {
		return err
	}
	if !ok || msg.ChatID != chatID {
		return fmt.Errorf("message %d not found in chat %d", messageID, chatID)
	}
	return s.store.UpdateMessageContent(messageID, content, prompt.EstimateTokens(content))
}

// Params returns a chat's sampler overrides (dev-mode sampler panel,
// §9). All-nil means no overrides: the model's own defaults apply.
func (s *Service) Params(chatID int64) (provider.SamplerParams, error) {
	raw, ok, err := s.store.GetChatParams(chatID)
	if err != nil || !ok {
		return provider.SamplerParams{}, err
	}
	var p provider.SamplerParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return provider.SamplerParams{}, fmt.Errorf("chat %d has unreadable params: %w", chatID, err)
	}
	return p, nil
}

// SetParams stores a chat's sampler overrides; an all-nil params clears
// them. Applies from the next generation.
func (s *Service) SetParams(chatID int64, params provider.SamplerParams) error {
	if _, ok, err := s.store.GetChat(chatID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("chat %d does not exist", chatID)
	}
	empty := params.Temperature == nil && params.TopP == nil && params.TopK == nil &&
		params.MinP == nil && params.RepeatPenalty == nil && params.MaxTokens == nil &&
		len(params.Stop) == 0
	if empty {
		return s.store.SetChatParams(chatID, "")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encoding params: %w", err)
	}
	return s.store.SetChatParams(chatID, string(raw))
}

// Inspection is the context inspector record (§9): what exactly was
// sent to the provider for one assistant message, captured at
// generation time and persisted with the message.
type Inspection struct {
	ProviderID      string               `json:"providerId"`
	Model           string               `json:"model"`
	CreatedAt       int64                `json:"createdAt"`
	ContextWindow   int                  `json:"contextWindow"`
	ReservedOutput  int                  `json:"reservedOutput"`
	SystemTokens    int                  `json:"systemTokens"`
	HistoryTokens   int                  `json:"historyTokens"`
	DroppedMessages int                  `json:"droppedMessages"`
	Segments        []prompt.Segment     `json:"segments"`
	RequestURL      string               `json:"requestUrl"`
	RawRequest      json.RawMessage      `json:"rawRequest"`
	ParamReport     provider.ParamReport `json:"paramReport"`
	NoStream        bool                 `json:"noStream"`
}

// Inspect returns the inspector record of a message. Messages without
// one (user messages, greetings, replies generated before M1.7) return
// a descriptive error the frontend can show as-is.
func (s *Service) Inspect(chatID, messageID int64) (Inspection, error) {
	msg, ok, err := s.store.GetMessage(messageID)
	if err != nil {
		return Inspection{}, err
	}
	if !ok || msg.ChatID != chatID {
		return Inspection{}, fmt.Errorf("message %d not found in chat %d", messageID, chatID)
	}
	raw, ok, err := s.store.GetMessagePrompt(messageID)
	if err != nil {
		return Inspection{}, err
	}
	if !ok {
		return Inspection{}, errors.New("no prompt record for this message — only generated replies from this version onward carry one")
	}
	var insp Inspection
	if err := json.Unmarshal([]byte(raw), &insp); err != nil {
		return Inspection{}, fmt.Errorf("message %d has an unreadable prompt record: %w", messageID, err)
	}
	return insp, nil
}

// PersonaView is the default persona as the settings screen edits it.
type PersonaView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Persona returns the default persona, falling back to the legacy
// display-name setting for databases from before personas existed.
func (s *Service) Persona() (PersonaView, error) {
	p, ok, err := s.store.DefaultPersona()
	if err != nil {
		return PersonaView{}, err
	}
	if ok {
		return PersonaView{Name: p.Name, Description: p.Description}, nil
	}
	return PersonaView{Name: s.stringSetting(settingDisplayName)}, nil
}

// SetPersona creates or updates the default persona.
func (s *Service) SetPersona(name, description string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("persona name can't be empty")
	}
	return s.store.SetDefaultPersona(name, strings.TrimSpace(description))
}

func (s *Service) isBusy(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, busy := s.inflight[chatID]
	return busy
}

// stateForChatID rebuilds State from just a chat id.
func (s *Service) stateForChatID(chatID int64) (State, error) {
	chat, ok, err := s.store.GetChat(chatID)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, fmt.Errorf("chat %d does not exist", chatID)
	}
	characterID, err := s.store.ChatCharacterID(chatID)
	if err != nil {
		return State{}, err
	}
	name := ""
	if characterID != 0 {
		if _, parsed, err := s.loadCharacter(characterID); err == nil {
			name = parsed.DisplayName()
		}
	}
	return s.stateForChat(chat, characterID, name)
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
// the outcome, and emit events. It owns the in-flight slot. A non-zero
// swipeGroup means this is a regeneration whose result joins that group.
func (s *Service) generate(ctx context.Context, chat store.Chat, swipeGroup int64) {
	defer s.clearInflight(chat.ID)

	// Dev-mode request timeout bounds the whole generation (§9).
	if secs := s.int64Setting(settingTimeoutSecs); secs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(secs)*time.Second)
		defer cancel()
	}

	fail := func(err error) {
		// A failed regeneration must not leave its swipe group with no
		// active member — bring the previous reply back.
		s.restoreSwipe(chat.ID, swipeGroup)
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
	contextWindow := s.staticContextWindow(chat.ProviderID)
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

	params, err := s.Params(chat.ID)
	if err != nil {
		fail(err)
		return
	}
	req := provider.ChatRequest{
		Model:       chat.Model,
		Messages:    built.Messages,
		System:      built.System,
		PostHistory: built.PostHistory,
		Params:      params,
		NoStream:    s.streamingDisabled(),
	}

	// Capture the inspector record before sending: segment breakdown
	// from the builder plus the wire request from the provider.
	insp := Inspection{
		ProviderID:      chat.ProviderID,
		Model:           chat.Model,
		CreatedAt:       time.Now().Unix(),
		ContextWindow:   built.ContextWindow,
		ReservedOutput:  built.ReservedOutput,
		SystemTokens:    built.SystemTokens,
		HistoryTokens:   built.HistoryTokens,
		DroppedMessages: built.DroppedMessages,
		Segments:        built.Segments,
		NoStream:        req.NoStream,
	}
	if insp.ProviderID == "" {
		insp.ProviderID = "ollama"
	}
	if d, ok := p.(provider.RequestDescriber); ok {
		if desc, err := d.DescribeRequest(req); err == nil {
			insp.RequestURL = desc.URL
			insp.RawRequest = desc.Body
			insp.ParamReport = desc.Report
		}
	}
	promptJSON := ""
	if raw, err := json.Marshal(insp); err == nil {
		promptJSON = string(raw)
	}
	started := time.Now()
	logResult := func(status, errMsg, response string, usage *provider.Usage) {
		if s.log == nil {
			return
		}
		s.log.Add(devlog.Entry{
			Time:       started.Unix(),
			ProviderID: insp.ProviderID,
			Model:      chat.Model,
			URL:        insp.RequestURL,
			Request:    insp.RawRequest,
			Status:     status,
			Error:      errMsg,
			Response:   response,
			DurationMs: time.Since(started).Milliseconds(),
			Usage:      usage,
		})
	}

	events, err := p.ChatStream(ctx, req)
	if err != nil {
		logResult("error", err.Error(), "", nil)
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
			logResult("ok", "", sb.String(), ev.Usage)
			s.finish(chat.ID, sb.String(), false, ev.Usage, swipeGroup, promptJSON)
		case ev.Err != nil:
			canceled := errors.Is(ev.Err, context.Canceled)
			if canceled {
				logResult("canceled", ev.Err.Error(), sb.String(), nil)
			} else {
				logResult("error", ev.Err.Error(), sb.String(), nil)
			}
			if sb.Len() > 0 || canceled {
				// Persist any partial, marked truncated; an empty
				// cancel just restores the previous swipe.
				s.finish(chat.ID, sb.String(), true, nil, swipeGroup, promptJSON)
			} else {
				s.restoreSwipe(chat.ID, swipeGroup)
			}
			if !canceled {
				s.emit(fmt.Sprintf("chat:%d:error", chat.ID), ev.Err.Error())
			}
		}
	}
}

// streamingDisabled reads the dev-mode streaming toggle; unset means
// streaming stays on.
func (s *Service) streamingDisabled() bool {
	raw, ok, err := s.store.GetSetting(settingStreaming)
	if err != nil || !ok {
		return false
	}
	var v bool
	if json.Unmarshal([]byte(raw), &v) != nil {
		return false
	}
	return !v
}

// finish persists the assistant reply (with its inspector record) and
// emits the done event. An empty reply persists nothing; if it was a
// regeneration, the previous swipe is reactivated so the group is never
// left headless.
func (s *Service) finish(chatID int64, content string, truncated bool, usage *provider.Usage, swipeGroup int64, promptJSON string) {
	payload := DonePayload{Content: content, Truncated: truncated, Usage: usage}
	if content != "" {
		tokens := prompt.EstimateTokens(content)
		if usage != nil && usage.CompletionTokens > 0 {
			tokens = usage.CompletionTokens
		}
		msg, err := s.store.AppendSwipe(chatID, provider.RoleAssistant, content, tokens, truncated, swipeGroup, true)
		if err != nil {
			s.emit(fmt.Sprintf("chat:%d:error", chatID), err.Error())
			return
		}
		if promptJSON != "" {
			if err := s.store.SetMessagePrompt(msg.ID, promptJSON); err != nil {
				// Inspector data is best-effort; the reply itself is safe.
				s.emit(fmt.Sprintf("chat:%d:error", chatID), err.Error())
			}
		}
		payload.MessageID = msg.ID
	} else {
		s.restoreSwipe(chatID, swipeGroup)
	}
	s.emit(fmt.Sprintf("chat:%d:done", chatID), payload)
}

// restoreSwipe reactivates the newest member of a swipe group if none
// is active (a regeneration was canceled or failed before producing
// anything).
func (s *Service) restoreSwipe(chatID, swipeGroup int64) {
	if swipeGroup == 0 {
		return
	}
	swipes, err := s.store.SwipesInGroup(chatID, swipeGroup)
	if err != nil || len(swipes) == 0 {
		return
	}
	for _, sw := range swipes {
		if sw.IsActive {
			return
		}
	}
	if err := s.store.ActivateSwipe(chatID, swipeGroup, swipes[len(swipes)-1].ID); err != nil {
		s.emit(fmt.Sprintf("chat:%d:error", chatID), err.Error())
	}
}

// ensureStarterCharacter seeds Ember as a real character row on first
// run (so the characters screen is never empty) and adopts the legacy
// M1.2 dev chat into her. Runs once; deleting Ember afterwards is
// respected. Serialized against StrictMode double-mounts.
func (s *Service) ensureStarterCharacter() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return starters.Seed(s.store)
}

// characterForChat loads the prompt view of a chat's character. Chats
// without one (pre-seed edge case) fall back to the built-in card.
func (s *Service) characterForChat(chatID int64) (prompt.Character, error) {
	charID, err := s.store.ChatCharacterID(chatID)
	if err != nil {
		return prompt.Character{}, err
	}
	if charID == 0 {
		return prompt.Character{}, fmt.Errorf("chat %d has no character", chatID)
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
		MesExample:   parsed.MesExample,
		PostHistory:  parsed.PostHistoryInstructions,
	}, nil
}

// persona builds the prompt persona from the default persona row,
// falling back to the legacy display-name setting, then to "User".
func (s *Service) persona() prompt.Persona {
	if p, ok, err := s.store.DefaultPersona(); err == nil && ok {
		return prompt.Persona{Name: p.Name, Description: p.Description}
	}
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
