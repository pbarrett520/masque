package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// openAtVersion applies only the first n migrations so a test can seed
// legacy rows before the rest run.
func openAtVersion(t *testing.T, n int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms[:n] {
		if err := applyMigration(db, m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func setting(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var v sql.NullString
	if err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&v); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return v.String
}

func TestMigration0004MovesCustomEndpointToCustomPreset(t *testing.T) {
	db := openAtVersion(t, 3)
	for k, v := range map[string]string{
		"provider.openai.base_url": `"https://openrouter.ai/api/v1"`,
		"provider.openai.api_key":  `"sk-or-legacy"`,
		"provider.default_id":      `"openai"`,
	} {
		if _, err := db.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", k, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO chats (title, provider_id, model) VALUES ('legacy', 'openai', 'some/model')"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := setting(t, db, "provider.custom-openai.base_url"); got != `"https://openrouter.ai/api/v1"` {
		t.Errorf("custom base_url = %s", got)
	}
	if got := setting(t, db, "provider.custom-openai.api_key"); got != `"sk-or-legacy"` {
		t.Errorf("custom api_key = %s", got)
	}
	if got := setting(t, db, "provider.default_id"); got != `"custom-openai"` {
		t.Errorf("default_id = %s", got)
	}
	if got := setting(t, db, "provider.openai.api_key"); got != "" {
		t.Errorf("legacy key still present: %s", got)
	}
	var pid string
	if err := db.QueryRow("SELECT provider_id FROM chats WHERE title = 'legacy'").Scan(&pid); err != nil || pid != "custom-openai" {
		t.Errorf("chat provider_id = %q (%v)", pid, err)
	}
}

func TestMigration0004LeavesRealOpenAIAlone(t *testing.T) {
	db := openAtVersion(t, 3)
	if _, err := db.Exec("INSERT INTO settings (key, value) VALUES ('provider.openai.api_key', '\"sk-real\"')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO chats (title, provider_id, model) VALUES ('real', 'openai', 'gpt-4o')"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := setting(t, db, "provider.openai.api_key"); got != `"sk-real"` {
		t.Errorf("openai key = %s, want untouched", got)
	}
	if got := setting(t, db, "provider.custom-openai.base_url"); got != "" {
		t.Errorf("custom preset created without reason: %s", got)
	}
	var pid string
	if err := db.QueryRow("SELECT provider_id FROM chats WHERE title = 'real'").Scan(&pid); err != nil || pid != "openai" {
		t.Errorf("chat provider_id = %q (%v)", pid, err)
	}
}
