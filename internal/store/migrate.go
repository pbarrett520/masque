package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded migration files, ordered by their
// numeric filename prefix (e.g. 0001_init.sql → version 1).
func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("reading embedded migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration %q: invalid version prefix", name)
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", name, err)
		}
		ms = append(ms, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 1; got %q at position %d", m.name, i+1)
		}
	}
	return ms, nil
}

// migrate applies all migrations newer than the database's current
// schema version (tracked via PRAGMA user_version). Each migration runs
// in its own transaction together with the version bump, so a failed
// migration leaves the database at its previous version.
func migrate(db *sql.DB) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if current > len(ms) {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", current, len(ms))
	}
	for _, m := range ms[current:] {
		if err := applyMigration(db, m); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migration %s: begin: %w", m.name, err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	// PRAGMA does not support parameter binding; version comes from the
	// validated numeric filename prefix.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return fmt.Errorf("migration %s: setting version: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s: commit: %w", m.name, err)
	}
	return nil
}
