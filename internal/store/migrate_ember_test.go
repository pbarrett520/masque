package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

type dbHandle struct{ *sql.DB }

// emberSeed is the exact card_json the retired seed function wrote, as
// captured before removal; migration 0006 embeds the same literal.
func emberSeed(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ember_seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func seedEmber(t *testing.T, cardJSON string, chats int) (*dbHandle, int64) {
	t.Helper()
	db := openAtVersion(t, 5)
	res, err := db.Exec("INSERT INTO characters (name, card_json, created_at, updated_at) VALUES ('Ember', ?, 1, 1)", cardJSON)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec("INSERT INTO settings (key, value) VALUES ('seed.ember_character_id', ?)", id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < chats; i++ {
		if _, err := db.Exec("INSERT INTO chats (character_id, title) VALUES (?, 'Ember')", id); err != nil {
			t.Fatal(err)
		}
	}
	return &dbHandle{db}, id
}

func TestMigration0006RemovesUntouchedEmberWithoutChats(t *testing.T) {
	h, id := seedEmber(t, emberSeed(t), 0)
	if err := migrate(h.DB); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = h.QueryRow("SELECT count(*) FROM characters WHERE id = ?", id).Scan(&n)
	if n != 0 {
		t.Error("untouched Ember with no chats should be removed")
	}
	if got := setting(t, h.DB, "seed.ember_character_id"); got != "" {
		t.Errorf("seed setting should be gone, got %q", got)
	}
}

func TestMigration0006SoftDeletesUntouchedEmberWithChats(t *testing.T) {
	h, id := seedEmber(t, emberSeed(t), 2)
	if err := migrate(h.DB); err != nil {
		t.Fatal(err)
	}
	var deleted int64
	var name string
	if err := h.QueryRow("SELECT name, coalesce(deleted_at, 0) FROM characters WHERE id = ?", id).Scan(&name, &deleted); err != nil {
		t.Fatalf("Ember row vanished: %v", err)
	}
	if deleted == 0 || name != "Ember" {
		t.Errorf("Ember should be soft-deleted, got deleted_at=%d name=%q", deleted, name)
	}
	var chats int
	_ = h.QueryRow("SELECT count(*) FROM chats WHERE character_id = ?", id).Scan(&chats)
	if chats != 2 {
		t.Errorf("chats lost: %d", chats)
	}
}

func TestMigration0006KeepsEditedEmber(t *testing.T) {
	edited := emberSeed(t)[:len(emberSeed(t))-2] + `,"edited":true}}`
	h, id := seedEmber(t, edited, 1)
	if err := migrate(h.DB); err != nil {
		t.Fatal(err)
	}
	var deleted int64
	if err := h.QueryRow("SELECT coalesce(deleted_at, 0) FROM characters WHERE id = ?", id).Scan(&deleted); err != nil {
		t.Fatalf("edited Ember removed: %v", err)
	}
	if deleted != 0 {
		t.Error("edited Ember must be kept as the user's own character")
	}
	if got := setting(t, h.DB, "seed.ember_character_id"); got != "" {
		t.Errorf("seed setting should be gone, got %q", got)
	}
}

func TestMigration0006NoEmberIsNoop(t *testing.T) {
	db := openAtVersion(t, 5)
	if _, err := db.Exec("INSERT INTO characters (name, card_json, created_at, updated_at) VALUES ('Mine', '{\"name\":\"Mine\"}', 1, 1)"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow("SELECT count(*) FROM characters").Scan(&n)
	if n != 1 {
		t.Errorf("unrelated character touched: %d rows", n)
	}
}
