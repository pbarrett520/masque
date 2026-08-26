package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Chat is one conversation row.
type Chat struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// Message is one message row. SwipeGroup links regeneration siblings
// (0 = not part of a group); exactly one member of a group is active.
type Message struct {
	ID            int64  `json:"id"`
	ChatID        int64  `json:"chatId"`
	Role          string `json:"role"`
	Content       string `json:"content"`
	SwipeGroup    int64  `json:"swipeGroup"`
	IsActive      bool   `json:"isActive"`
	TokenEstimate int    `json:"tokenEstimate"`
	Truncated     bool   `json:"truncated"`
	CreatedAt     int64  `json:"createdAt"`
}

// CreateChat inserts a chat. character_id and persona_id stay NULL until
// card import (M1.4) and personas (M1.5) land.
func (s *Store) CreateChat(title, providerID, model string) (Chat, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		"INSERT INTO chats (title, provider_id, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		title, providerID, model, now, now,
	)
	if err != nil {
		return Chat{}, fmt.Errorf("creating chat: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Chat{}, fmt.Errorf("creating chat: reading id: %w", err)
	}
	return Chat{ID: id, Title: title, ProviderID: providerID, Model: model, CreatedAt: now, UpdatedAt: now}, nil
}

// GetChat returns the chat with id; the second return is false when it
// does not exist.
func (s *Store) GetChat(id int64) (Chat, bool, error) {
	var c Chat
	var title, providerID, model sql.NullString
	err := s.db.QueryRow(
		"SELECT id, title, provider_id, model, created_at, updated_at FROM chats WHERE id = ?", id,
	).Scan(&c.ID, &title, &providerID, &model, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, false, nil
	}
	if err != nil {
		return Chat{}, false, fmt.Errorf("getting chat %d: %w", id, err)
	}
	c.Title, c.ProviderID, c.Model = title.String, providerID.String, model.String
	return c, true, nil
}

// SetChatModel records the provider/model last used by a chat, for
// resume and mid-chat switching.
func (s *Store) SetChatModel(id int64, providerID, model string) error {
	res, err := s.db.Exec(
		"UPDATE chats SET provider_id = ?, model = ?, updated_at = ? WHERE id = ?",
		providerID, model, time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("updating chat %d model: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("updating chat %d model: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("updating chat %d model: chat does not exist", id)
	}
	return nil
}

// AppendMessage inserts an active, ungrouped message at the end of a
// chat.
func (s *Store) AppendMessage(chatID int64, role, content string, tokenEstimate int, truncated bool) (Message, error) {
	return s.AppendSwipe(chatID, role, content, tokenEstimate, truncated, 0, true)
}

// AppendSwipe inserts a message with explicit swipe-group membership and
// active flag. swipeGroup 0 means no group.
func (s *Store) AppendSwipe(chatID int64, role, content string, tokenEstimate int, truncated bool, swipeGroup int64, active bool) (Message, error) {
	now := time.Now().Unix()
	var group any
	if swipeGroup != 0 {
		group = swipeGroup
	}
	res, err := s.db.Exec(
		"INSERT INTO messages (chat_id, role, content, token_estimate, truncated, swipe_group, is_active, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		chatID, role, content, tokenEstimate, truncated, group, active, now,
	)
	if err != nil {
		return Message{}, fmt.Errorf("appending message to chat %d: %w", chatID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Message{}, fmt.Errorf("appending message to chat %d: reading id: %w", chatID, err)
	}
	if _, err := s.db.Exec("UPDATE chats SET updated_at = ? WHERE id = ?", now, chatID); err != nil {
		return Message{}, fmt.Errorf("touching chat %d: %w", chatID, err)
	}
	return Message{
		ID: id, ChatID: chatID, Role: role, Content: content, SwipeGroup: swipeGroup,
		IsActive: active, TokenEstimate: tokenEstimate, Truncated: truncated, CreatedAt: now,
	}, nil
}

// SetMessagePrompt attaches the context-inspector record (JSON) to a
// message, captured when the message was generated.
func (s *Store) SetMessagePrompt(messageID int64, promptJSON string) error {
	res, err := s.db.Exec("UPDATE messages SET prompt_json = ? WHERE id = ?", promptJSON, messageID)
	if err != nil {
		return fmt.Errorf("setting prompt on message %d: %w", messageID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("setting prompt on message %d: message does not exist", messageID)
	}
	return nil
}

// GetMessagePrompt returns a message's inspector record; the second
// return is false when the message has none (user messages, greetings,
// messages generated before M1.7).
func (s *Store) GetMessagePrompt(messageID int64) (string, bool, error) {
	var v sql.NullString
	err := s.db.QueryRow("SELECT prompt_json FROM messages WHERE id = ?", messageID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("getting prompt of message %d: %w", messageID, err)
	}
	return v.String, v.Valid && v.String != "", nil
}

// SetChatParams stores a chat's sampler overrides as JSON ("" clears).
func (s *Store) SetChatParams(chatID int64, paramsJSON string) error {
	var v any
	if paramsJSON != "" {
		v = paramsJSON
	}
	res, err := s.db.Exec("UPDATE chats SET params_json = ?, updated_at = ? WHERE id = ?", v, time.Now().Unix(), chatID)
	if err != nil {
		return fmt.Errorf("setting params on chat %d: %w", chatID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("setting params on chat %d: chat does not exist", chatID)
	}
	return nil
}

// GetChatParams returns a chat's sampler overrides JSON; the second
// return is false when none are set.
func (s *Store) GetChatParams(chatID int64) (string, bool, error) {
	var v sql.NullString
	err := s.db.QueryRow("SELECT params_json FROM chats WHERE id = ?", chatID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("getting params of chat %d: %w", chatID, err)
	}
	return v.String, v.Valid && v.String != "", nil
}

// SetSwipeGroup stamps a message into a swipe group (used when its
// first sibling is created).
func (s *Store) SetSwipeGroup(messageID, swipeGroup int64) error {
	if _, err := s.db.Exec("UPDATE messages SET swipe_group = ? WHERE id = ?", swipeGroup, messageID); err != nil {
		return fmt.Errorf("setting swipe group on message %d: %w", messageID, err)
	}
	return nil
}

// SwipesInGroup returns every message in a swipe group, oldest first.
func (s *Store) SwipesInGroup(chatID, swipeGroup int64) ([]Message, error) {
	return s.queryMessages(
		"SELECT id, chat_id, role, content, token_estimate, truncated, swipe_group, is_active, created_at "+
			"FROM messages WHERE chat_id = ? AND swipe_group = ? ORDER BY id", chatID, swipeGroup,
	)
}

// ActivateSwipe makes messageID the active member of its swipe group,
// deactivating the rest.
func (s *Store) ActivateSwipe(chatID, swipeGroup, messageID int64) error {
	res, err := s.db.Exec(
		"UPDATE messages SET is_active = (id = ?) WHERE chat_id = ? AND swipe_group = ?",
		messageID, chatID, swipeGroup,
	)
	if err != nil {
		return fmt.Errorf("activating swipe %d: %w", messageID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("activating swipe %d: group %d is empty", messageID, swipeGroup)
	}
	return nil
}

// DeactivateMessage hides a message from the active thread (its swipe
// sibling replaces it).
func (s *Store) DeactivateMessage(messageID int64) error {
	if _, err := s.db.Exec("UPDATE messages SET is_active = 0 WHERE id = ?", messageID); err != nil {
		return fmt.Errorf("deactivating message %d: %w", messageID, err)
	}
	return nil
}

// UpdateMessageContent edits a message in place.
func (s *Store) UpdateMessageContent(messageID int64, content string, tokenEstimate int) error {
	res, err := s.db.Exec(
		"UPDATE messages SET content = ?, token_estimate = ?, truncated = 0 WHERE id = ?",
		content, tokenEstimate, messageID,
	)
	if err != nil {
		return fmt.Errorf("editing message %d: %w", messageID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("editing message %d: message does not exist", messageID)
	}
	return nil
}

// GetMessage returns one message; the second return is false when it
// does not exist.
func (s *Store) GetMessage(messageID int64) (Message, bool, error) {
	msgs, err := s.queryMessages(
		"SELECT id, chat_id, role, content, token_estimate, truncated, swipe_group, is_active, created_at "+
			"FROM messages WHERE id = ?", messageID,
	)
	if err != nil {
		return Message{}, false, err
	}
	if len(msgs) == 0 {
		return Message{}, false, nil
	}
	return msgs[0], true, nil
}

// ActiveMessages returns a chat's active thread, oldest first —
// inactive swipe siblings are excluded.
func (s *Store) ActiveMessages(chatID int64) ([]Message, error) {
	return s.queryMessages(
		"SELECT id, chat_id, role, content, token_estimate, truncated, swipe_group, is_active, created_at "+
			"FROM messages WHERE chat_id = ? AND is_active = 1 ORDER BY id", chatID,
	)
}

func (s *Store) queryMessages(query string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only rows
	var msgs []Message
	for rows.Next() {
		var m Message
		var tokens, group sql.NullInt64
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &tokens, &m.Truncated, &group, &m.IsActive, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		m.TokenEstimate = int(tokens.Int64)
		m.SwipeGroup = group.Int64
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	return msgs, nil
}
