package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Character is one character row. Avatar is loaded separately
// (GetAvatar) — lists stay light.
type Character struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CardJSON  string `json:"-"` // canonical V1/V2/V3 JSON, verbatim
	HasAvatar bool   `json:"hasAvatar"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// DeletedAt is non-zero once the character was deleted: hidden from
	// the library, kept for its chats (migration 0005).
	DeletedAt int64 `json:"deletedAt"`
}

// CreateCharacter inserts a character. avatar may be nil.
func (s *Store) CreateCharacter(name, cardJSON string, avatar []byte) (Character, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		"INSERT INTO characters (name, card_json, avatar, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		name, cardJSON, avatar, now, now,
	)
	if err != nil {
		return Character{}, fmt.Errorf("creating character %q: %w", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Character{}, fmt.Errorf("creating character %q: reading id: %w", name, err)
	}
	return Character{
		ID: id, Name: name, CardJSON: cardJSON,
		HasAvatar: len(avatar) > 0, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ListCharacters returns all live (not deleted) characters, newest
// first, without card bodies or avatars.
func (s *Store) ListCharacters() ([]Character, error) {
	rows, err := s.db.Query(
		"SELECT id, name, avatar IS NOT NULL AND length(avatar) > 0, created_at, updated_at " +
			"FROM characters WHERE deleted_at IS NULL ORDER BY id DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("listing characters: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only rows
	var chars []Character
	for rows.Next() {
		var c Character
		if err := rows.Scan(&c.ID, &c.Name, &c.HasAvatar, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning character: %w", err)
		}
		chars = append(chars, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing characters: %w", err)
	}
	return chars, nil
}

// GetCharacter returns the character with id including its card JSON,
// deleted or not (check DeletedAt); the second return is false when it
// does not exist.
func (s *Store) GetCharacter(id int64) (Character, bool, error) {
	var c Character
	var avatarLen, deletedAt sql.NullInt64
	err := s.db.QueryRow(
		"SELECT id, name, card_json, length(avatar), created_at, updated_at, deleted_at FROM characters WHERE id = ?", id,
	).Scan(&c.ID, &c.Name, &c.CardJSON, &avatarLen, &c.CreatedAt, &c.UpdatedAt, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Character{}, false, nil
	}
	if err != nil {
		return Character{}, false, fmt.Errorf("getting character %d: %w", id, err)
	}
	c.HasAvatar = avatarLen.Int64 > 0
	c.DeletedAt = deletedAt.Int64
	return c, true, nil
}

// UpdateCharacter replaces the name and card JSON. avatar: nil keeps
// the current image, a non-nil slice replaces it, and clearAvatar
// removes it.
func (s *Store) UpdateCharacter(id int64, name, cardJSON string, avatar []byte, clearAvatar bool) error {
	now := time.Now().Unix()
	var res sql.Result
	var err error
	switch {
	case clearAvatar:
		res, err = s.db.Exec("UPDATE characters SET name = ?, card_json = ?, avatar = NULL, updated_at = ? WHERE id = ?", name, cardJSON, now, id)
	case avatar != nil:
		res, err = s.db.Exec("UPDATE characters SET name = ?, card_json = ?, avatar = ?, updated_at = ? WHERE id = ?", name, cardJSON, avatar, now, id)
	default:
		res, err = s.db.Exec("UPDATE characters SET name = ?, card_json = ?, updated_at = ? WHERE id = ?", name, cardJSON, now, id)
	}
	if err != nil {
		return fmt.Errorf("updating character %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("character %d does not exist", id)
	}
	return nil
}

// ChatCount returns how many chats belong to a character.
func (s *Store) ChatCount(characterID int64) (int, error) {
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM chats WHERE character_id = ?", characterID).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting chats for character %d: %w", characterID, err)
	}
	return n, nil
}

// SoftDeleteCharacter hides a character from the library while keeping
// its row and chats (spec: never delete conversations as a side
// effect). Chats of a deleted character stay readable but are not
// continued; the chat service enforces that.
func (s *Store) SoftDeleteCharacter(id int64) error {
	res, err := s.db.Exec("UPDATE characters SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		time.Now().Unix(), time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("deleting character %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("character %d does not exist", id)
	}
	return nil
}

// GetAvatar returns the character's avatar PNG, or nil if it has none.
func (s *Store) GetAvatar(id int64) ([]byte, error) {
	var avatar []byte
	err := s.db.QueryRow("SELECT avatar FROM characters WHERE id = ?", id).Scan(&avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting avatar for character %d: %w", id, err)
	}
	return avatar, nil
}

// DeleteCharacter hard-deletes a character and its chats (messages
// first — no ON DELETE CASCADE in the shipped schema). The app uses
// SoftDeleteCharacter; this remains for tests and future purge tooling.
func (s *Store) DeleteCharacter(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("deleting character %d: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op
	if _, err := tx.Exec(
		"DELETE FROM messages WHERE chat_id IN (SELECT id FROM chats WHERE character_id = ?)", id,
	); err != nil {
		return fmt.Errorf("deleting character %d messages: %w", id, err)
	}
	if _, err := tx.Exec("DELETE FROM chats WHERE character_id = ?", id); err != nil {
		return fmt.Errorf("deleting character %d chats: %w", id, err)
	}
	if _, err := tx.Exec("DELETE FROM characters WHERE id = ?", id); err != nil {
		return fmt.Errorf("deleting character %d: %w", id, err)
	}
	return tx.Commit()
}

// LatestChatForCharacter returns the most recent chat for a character;
// the second return is false when none exists.
func (s *Store) LatestChatForCharacter(characterID int64) (Chat, bool, error) {
	var c Chat
	var title, providerID, model sql.NullString
	err := s.db.QueryRow(
		"SELECT id, title, provider_id, model, created_at, updated_at FROM chats "+
			"WHERE character_id = ? ORDER BY updated_at DESC, id DESC LIMIT 1", characterID,
	).Scan(&c.ID, &title, &providerID, &model, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, false, nil
	}
	if err != nil {
		return Chat{}, false, fmt.Errorf("finding chat for character %d: %w", characterID, err)
	}
	c.Title, c.ProviderID, c.Model = title.String, providerID.String, model.String
	return c, true, nil
}

// CreateChatForCharacter inserts a chat linked to a character.
func (s *Store) CreateChatForCharacter(characterID int64, title, providerID, model string) (Chat, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		"INSERT INTO chats (character_id, title, provider_id, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		characterID, title, providerID, model, now, now,
	)
	if err != nil {
		return Chat{}, fmt.Errorf("creating chat for character %d: %w", characterID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Chat{}, fmt.Errorf("creating chat for character %d: reading id: %w", characterID, err)
	}
	return Chat{ID: id, Title: title, ProviderID: providerID, Model: model, CreatedAt: now, UpdatedAt: now}, nil
}

// ChatCharacterID returns the character a chat belongs to (0 when the
// chat predates characters or does not exist).
func (s *Store) ChatCharacterID(chatID int64) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRow("SELECT character_id FROM chats WHERE id = ?", chatID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading chat %d character: %w", chatID, err)
	}
	return id.Int64, nil
}

// LinkChatCharacter attaches a legacy chat to a character.
func (s *Store) LinkChatCharacter(chatID, characterID int64) error {
	if _, err := s.db.Exec(
		"UPDATE chats SET character_id = ?, updated_at = ? WHERE id = ?",
		characterID, time.Now().Unix(), chatID,
	); err != nil {
		return fmt.Errorf("linking chat %d to character %d: %w", chatID, characterID, err)
	}
	return nil
}
