package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ChatListItem is one row of the chat list (spec §9: chat list/resume).
type ChatListItem struct {
	ID            int64  `json:"id"`
	CharacterID   int64  `json:"characterId"`
	CharacterName string `json:"characterName"`
	Title         string `json:"title"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// ListChats returns all chats that belong to a character, most recently
// updated first.
func (s *Store) ListChats() ([]ChatListItem, error) {
	rows, err := s.db.Query(
		"SELECT c.id, c.character_id, ch.name, c.title, c.updated_at " +
			"FROM chats c JOIN characters ch ON ch.id = c.character_id " +
			"ORDER BY c.updated_at DESC, c.id DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only rows
	var items []ChatListItem
	for rows.Next() {
		var it ChatListItem
		var title sql.NullString
		if err := rows.Scan(&it.ID, &it.CharacterID, &it.CharacterName, &title, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning chat list: %w", err)
		}
		it.Title = title.String
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	return items, nil
}

// DeleteChat removes a chat and its messages.
func (s *Store) DeleteChat(chatID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("deleting chat %d: %w", chatID, err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op
	if _, err := tx.Exec("DELETE FROM messages WHERE chat_id = ?", chatID); err != nil {
		return fmt.Errorf("deleting chat %d messages: %w", chatID, err)
	}
	if _, err := tx.Exec("DELETE FROM chats WHERE id = ?", chatID); err != nil {
		return fmt.Errorf("deleting chat %d: %w", chatID, err)
	}
	return tx.Commit()
}

// Persona is the user's identity in chats.
type Persona struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DefaultPersona returns the default persona; the second return is
// false when none has been created yet.
func (s *Store) DefaultPersona() (Persona, bool, error) {
	var p Persona
	err := s.db.QueryRow(
		"SELECT id, name, description FROM personas WHERE is_default = 1 ORDER BY id LIMIT 1",
	).Scan(&p.ID, &p.Name, &p.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return Persona{}, false, nil
	}
	if err != nil {
		return Persona{}, false, fmt.Errorf("reading default persona: %w", err)
	}
	return p, true, nil
}

// SetDefaultPersona creates or updates the default persona.
func (s *Store) SetDefaultPersona(name, description string) error {
	existing, ok, err := s.DefaultPersona()
	if err != nil {
		return err
	}
	if ok {
		if _, err := s.db.Exec(
			"UPDATE personas SET name = ?, description = ? WHERE id = ?",
			name, description, existing.ID,
		); err != nil {
			return fmt.Errorf("updating default persona: %w", err)
		}
		return nil
	}
	if _, err := s.db.Exec(
		"INSERT INTO personas (name, description, is_default) VALUES (?, ?, 1)",
		name, description,
	); err != nil {
		return fmt.Errorf("creating default persona: %w", err)
	}
	return nil
}
