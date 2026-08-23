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

// Message is one message row.
type Message struct {
	ID            int64  `json:"id"`
	ChatID        int64  `json:"chatId"`
	Role          string `json:"role"`
	Content       string `json:"content"`
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

// AppendMessage inserts a message at the end of a chat.
func (s *Store) AppendMessage(chatID int64, role, content string, tokenEstimate int, truncated bool) (Message, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		"INSERT INTO messages (chat_id, role, content, token_estimate, truncated, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		chatID, role, content, tokenEstimate, truncated, now,
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
		ID: id, ChatID: chatID, Role: role, Content: content,
		TokenEstimate: tokenEstimate, Truncated: truncated, CreatedAt: now,
	}, nil
}

// ActiveMessages returns a chat's active messages, oldest first. (All
// messages are active until swipes land in M1.5.)
func (s *Store) ActiveMessages(chatID int64) ([]Message, error) {
	rows, err := s.db.Query(
		"SELECT id, chat_id, role, content, token_estimate, truncated, created_at "+
			"FROM messages WHERE chat_id = ? AND is_active = 1 ORDER BY id", chatID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing messages for chat %d: %w", chatID, err)
	}
	defer rows.Close() //nolint:errcheck // read-only rows
	var msgs []Message
	for rows.Next() {
		var m Message
		var tokens sql.NullInt64
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &tokens, &m.Truncated, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning message for chat %d: %w", chatID, err)
		}
		m.TokenEstimate = int(tokens.Int64)
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing messages for chat %d: %w", chatID, err)
	}
	return msgs, nil
}
