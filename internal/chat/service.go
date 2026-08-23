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

	"masque/internal/prompt"
	"masque/internal/provider"
	"masque/internal/provider/ollama"
	"masque/internal/store"
)

// Settings keys the service reads/writes. The frontend shares
// user.display_name (settings screen) and provider.ollama.* (chat setup).
const (
	settingChatID      = "chat.dev_chat_id"
	settingBaseURL     = "provider.ollama.base_url"
	settingModel       = "provider.ollama.model"
	settingDisplayName = "user.display_name"
)

// showTimeout bounds the /api/show metadata call before each generation.
const showTimeout = 5 * time.Second

// EmitFunc delivers a Wails event to the frontend.
type EmitFunc func(event string, args ...any)

// contextWindower is the optional metadata probe (Ollama has it).
type contextWindower interface {
	ContextWindow(ctx context.Context, model string) (int, error)
}

// Service is bound to the Wails frontend as chat.Service.
type Service struct {
	store       *store.Store
	emit        EmitFunc
	newProvider func(baseURL string) provider.Provider // test seam

	mu       sync.Mutex
	inflight map[int64]context.CancelFunc
}

// NewService returns a Service backed by st that emits frontend events
// through emit.
func NewService(st *store.Store, emit EmitFunc) *Service {
	return &Service{
		store:    st,
		emit:     emit,
		inflight: map[int64]context.CancelFunc{},
		newProvider: func(baseURL string) provider.Provider {
			return ollama.New(baseURL)
		},
	}
}

// MessageView is a message as the frontend renders it.
type MessageView struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// State is what the chat screen needs to render.
type State struct {
	ChatID        int64         `json:"chatId"`
	CharacterName string        `json:"characterName"`
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

// StartChat loads the single M1.2 dev chat, creating it (with the
// character's greeting) on first run, and returns the render state.
func (s *Service) StartChat() (State, error) {
	chat, err := s.loadOrCreateChat()
	if err != nil {
		return State{}, err
	}
	msgs, err := s.store.ActiveMessages(chat.ID)
	if err != nil {
		return State{}, err
	}
	state := State{
		ChatID:        chat.ID,
		CharacterName: hardcodedCharacter.Name,
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

// ListModels returns the chat-capable models at the configured endpoint.
func (s *Service) ListModels() ([]provider.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.newProvider(s.baseURL()).ListModels(ctx)
}

// Health probes the configured endpoint; the error string (or "") is
// returned rather than an error so the frontend can render it directly.
func (s *Service) Health() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.newProvider(s.baseURL()).HealthCheck(ctx); err != nil {
		return err.Error()
	}
	return ""
}

// SetModel records the model for a chat and as the default for new ones.
func (s *Service) SetModel(chatID int64, model string) error {
	if model == "" {
		return errors.New("model must not be empty")
	}
	if err := s.store.SetChatModel(chatID, "ollama", model); err != nil {
		return err
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("encoding model setting: %w", err)
	}
	return s.store.SetSetting(settingModel, string(raw))
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

	p := s.newProvider(s.baseURL())
	contextWindow := 0
	if cw, ok := p.(contextWindower); ok {
		showCtx, cancel := context.WithTimeout(ctx, showTimeout)
		if n, err := cw.ContextWindow(showCtx, chat.Model); err == nil {
			contextWindow = n
		}
		cancel()
	}

	built := prompt.Build(prompt.Input{
		Character:     hardcodedCharacter,
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

// loadOrCreateChat resolves the persistent dev chat, seeding a new one
// with the character's greeting (card.first_mes, spec §5). Serialized:
// React StrictMode double-mounts the chat screen, and two concurrent
// first runs must not seed two chats.
func (s *Service) loadOrCreateChat() (store.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if raw, ok, err := s.store.GetSetting(settingChatID); err != nil {
		return store.Chat{}, err
	} else if ok {
		var id int64
		if err := json.Unmarshal([]byte(raw), &id); err == nil {
			if chat, found, err := s.store.GetChat(id); err != nil {
				return store.Chat{}, err
			} else if found {
				return chat, nil
			}
		}
		// Stale or unreadable reference: fall through and recreate.
	}

	chat, err := s.store.CreateChat(hardcodedCharacter.Name, "ollama", s.defaultModel())
	if err != nil {
		return store.Chat{}, err
	}
	greeting := prompt.Substitute(hardcodedCharacter.FirstMes, hardcodedCharacter.Name, s.persona().Name)
	if _, err := s.store.AppendMessage(chat.ID, provider.RoleAssistant, greeting, prompt.EstimateTokens(greeting), false); err != nil {
		return store.Chat{}, err
	}
	if err := s.store.SetSetting(settingChatID, fmt.Sprintf("%d", chat.ID)); err != nil {
		return store.Chat{}, err
	}
	return chat, nil
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

func (s *Service) baseURL() string {
	return s.stringSetting(settingBaseURL) // "" → provider default
}

func (s *Service) defaultModel() string {
	return s.stringSetting(settingModel)
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
