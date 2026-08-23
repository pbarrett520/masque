// Package store owns all SQLite access for Masque. One database file per
// install (dev spec §6); higher layers never touch database/sql directly.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // pure-Go sqlite driver
)

// Store is the single gateway to the Masque database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database at path and applies any
// pending migrations.
func Open(path string) (*Store, error) {
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	// The webview frontend funnels through bound Go methods, but a single
	// connection sidesteps table-lock races between concurrent bindings.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating database %s: %w", path, err)
	}
	return &Store{db: db, path: path}, nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// SchemaVersion returns the database's current schema version.
func (s *Store) SchemaVersion() (int, error) {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	return v, nil
}

// GetSetting returns the raw JSON value for key. The second return is
// false when the key is not set.
func (s *Store) GetSetting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("getting setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting stores the raw JSON value under key, replacing any previous
// value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	if err != nil {
		return fmt.Errorf("setting %q: %w", key, err)
	}
	return nil
}

// DeleteSetting removes key. Deleting a missing key is not an error.
func (s *Store) DeleteSetting(key string) error {
	if _, err := s.db.Exec("DELETE FROM settings WHERE key = ?", key); err != nil {
		return fmt.Errorf("deleting setting %q: %w", key, err)
	}
	return nil
}
