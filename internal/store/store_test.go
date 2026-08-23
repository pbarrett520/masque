package store

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "masque.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrateFreshDatabase(t *testing.T) {
	s := openTestStore(t)

	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Errorf("schema version = %d, want 1", v)
	}

	for _, table := range []string{"characters", "personas", "chats", "messages", "settings"} {
		var name string
		err := s.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masque.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open (round %d): %v", i+1, err)
		}
		v, err := s.SchemaVersion()
		if err != nil {
			t.Fatal(err)
		}
		if v != 1 {
			t.Errorf("round %d: schema version = %d, want 1", i+1, v)
		}
		_ = s.Close()
	}
}

func TestMigrateRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masque.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	if _, err := Open(path); err == nil {
		t.Error("expected error opening database with a newer schema version")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := openTestStore(t)

	if _, ok, err := s.GetSetting("missing"); err != nil || ok {
		t.Errorf("GetSetting(missing) = ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	if err := s.SetSetting("appearance.theme", `"dark"`); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetSetting("appearance.theme")
	if err != nil || !ok || got != `"dark"` {
		t.Errorf("GetSetting = %q ok=%v err=%v, want %q ok=true", got, ok, err, `"dark"`)
	}

	// Overwrite.
	if err := s.SetSetting("appearance.theme", `"light"`); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.GetSetting("appearance.theme")
	if got != `"light"` {
		t.Errorf("after overwrite got %q, want %q", got, `"light"`)
	}

	if err := s.DeleteSetting("appearance.theme"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetSetting("appearance.theme"); ok {
		t.Error("setting still present after delete")
	}
	if err := s.DeleteSetting("appearance.theme"); err != nil {
		t.Errorf("deleting a missing key should not error: %v", err)
	}
}

func TestSettingsPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masque.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("persona.name", `{"name":"Pat"}`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	got, ok, err := s2.GetSetting("persona.name")
	if err != nil || !ok {
		t.Fatalf("GetSetting after reopen: ok=%v err=%v", ok, err)
	}
	if want := `{"name":"Pat"}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
