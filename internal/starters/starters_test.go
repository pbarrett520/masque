package starters

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"masque/internal/card"
	"masque/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestBundledCardsAreValidImportableV3(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d starters", len(all))
	}
	wantOrder := []string{"wren", "narrator", "laozhang"}
	for i, s := range all {
		if s.ID != wantOrder[i] {
			t.Errorf("order[%d] = %s, want %s", i, s.ID, wantOrder[i])
		}
		parsed, avatar, err := card.Parse(s.Card) // the importer's own entry point
		if err != nil {
			t.Fatalf("%s: importer rejects the card: %v", s.ID, err)
		}
		if avatar != nil {
			t.Errorf("%s: JSON card must not carry an avatar", s.ID)
		}
		if parsed.Spec != "chara_card_v3" || parsed.FirstMes == "" || parsed.MesExample == "" || parsed.SystemPrompt == "" || parsed.PostHistoryInstructions == "" {
			t.Errorf("%s: incomplete card: spec=%q", s.ID, parsed.Spec)
		}
		if !strings.Contains(parsed.SystemPrompt, "{{original}}") {
			t.Errorf("%s: system prompt should splice the default template", s.ID)
		}
		for _, banned := range []string{"never", "Never", "don't", "Don't", "do not", "Do not"} {
			if strings.Contains(parsed.SystemPrompt, banned) {
				t.Errorf("%s: system prompt uses a negative instruction %q (positive rules only)", s.ID, banned)
			}
		}
		if !card.IsPNG(s.Avatar) {
			t.Errorf("%s: avatar is not a PNG", s.ID)
		}
		// The V3 required keys the importer/exporter rely on.
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(s.Card, &env); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"tags", "creator", "character_version", "creator_notes", "alternate_greetings", "group_only_greetings", "extensions"} {
			if _, ok := env.Data[key]; !ok {
				t.Errorf("%s: missing %s", s.ID, key)
			}
		}
	}
	// Lǎo Zhāng's opening is specified verbatim.
	lz := all[2]
	parsed, _ := card.ParseJSON(lz.Card)
	if !strings.HasPrefix(parsed.FirstMes, "欢迎光临！我是老张。来，坐，喝杯茶吧。\nHuānyíng guānglín!") || !strings.HasSuffix(parsed.FirstMes, "3. 还不错 (hái búcuò, pretty good)") {
		t.Errorf("Lǎo Zhāng greeting drifted:\n%s", parsed.FirstMes)
	}
	if parsed.DisplayName() != "老张" {
		t.Errorf("{{char}} for Lǎo Zhāng = %q", parsed.DisplayName())
	}
}

func TestExportMatchesBundledCard(t *testing.T) {
	// Export of a seeded starter must round-trip: parse → ExportJSON →
	// parse again yields the same fields, so a user can take it anywhere.
	all, _ := All()
	for _, s := range all {
		a, err := card.ParseJSON(s.Card)
		if err != nil {
			t.Fatal(err)
		}
		out, err := card.ExportJSON(a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := card.ParseJSON(out)
		if err != nil {
			t.Fatalf("%s: export not importable: %v", s.ID, err)
		}
		a.Raw, b.Raw = nil, nil
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: round trip changed fields:\n%+v\n%+v", s.ID, a, b)
		}
		if MarkerOf(out) != s.ID {
			t.Errorf("%s: marker lost on export", s.ID)
		}
	}
}

func TestSeedOnceAndRestore(t *testing.T) {
	st := openStore(t)
	if err := Seed(st); err != nil {
		t.Fatal(err)
	}
	chars, _ := st.ListCharacters()
	if len(chars) != 3 {
		t.Fatalf("seeded %d", len(chars))
	}
	for _, c := range chars {
		if !c.HasAvatar {
			t.Errorf("%s has no avatar", c.Name)
		}
	}
	// Second seed is a no-op; delete one; seed still a no-op.
	if err := Seed(st); err != nil {
		t.Fatal(err)
	}
	if err := st.SoftDeleteCharacter(chars[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := Seed(st); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.ListCharacters(); len(left) != 2 {
		t.Errorf("Seed re-added a deleted starter: %d", len(left))
	}
	names, err := Restore(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != chars[0].Name {
		t.Errorf("Restore = %v", names)
	}
	// Display order after seeding: WREN, The Narrator, Lǎo Zhāng.
	st2 := openStore(t)
	_ = Seed(st2)
	listed, _ := st2.ListCharacters()
	if got := listed[0].Name + "|" + listed[1].Name + "|" + listed[2].Name; got != "WREN|The Narrator|Lǎo Zhāng" {
		t.Errorf("display order = %s", got)
	}
	if names, _ := Restore(st); len(names) != 0 {
		t.Errorf("second Restore added %v", names)
	}
}

func TestMarkerOf(t *testing.T) {
	if MarkerOf([]byte(`{"name":"x","extensions":{"masque":{"starter":"v1"}}}`)) != "v1" {
		t.Error("V1 marker")
	}
	if MarkerOf([]byte(`{"spec":"chara_card_v3","data":{"name":"x","extensions":{}}}`)) != "" {
		t.Error("no marker should be empty")
	}
	if MarkerOf([]byte(`garbage`)) != "" {
		t.Error("garbage should be empty")
	}
}
