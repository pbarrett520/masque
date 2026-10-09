package character

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"masque/internal/card"
)

func mustCreate(t *testing.T, svc *Service, form CardForm) View {
	t.Helper()
	v, err := svc.Create(form)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return v
}

func TestUpdatePersistsAndPreservesUnknownFields(t *testing.T) {
	svc := newService(t)
	// Import a V2 card with a lorebook and extensions: editing must not
	// drop anything the form doesn't cover.
	imported, err := svc.Import(cardFixture(t, "v2.json"), "ashfall.json")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	before, err := svc.Get(imported.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if before.Form.Name != "Ashfall" || before.Form.Description == "" {
		t.Fatalf("pre-filled form = %+v", before.Form)
	}
	if !before.HasLorebook || len(before.Extra) == 0 {
		t.Errorf("detail should flag preserved lorebook/extra fields: %+v", before)
	}

	form := before.Form
	form.Name = "Ashfall the Second"
	form.Description = "Edited description"
	form.SystemPrompt = "Edited system prompt"
	form.Tags = []string{"edited"}
	if _, err := svc.Update(imported.ID, form); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Reopen the store (simulating an app restart) and read it back.
	after, err := svc.Get(imported.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if after.Name != "Ashfall the Second" || after.Form.Description != "Edited description" || after.Form.SystemPrompt != "Edited system prompt" {
		t.Errorf("edits not persisted: %+v", after.Form)
	}
	if !after.HasLorebook || after.Spec != "chara_card_v3" {
		t.Errorf("lorebook lost or spec wrong after edit: %+v", after.View)
	}
	raw, err := svc.ExportJSON(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.Data["character_book"]; !ok {
		t.Error("character_book dropped by edit")
	}
	if ext, ok := env.Data["extensions"].(map[string]any); !ok || len(ext) == 0 {
		t.Errorf("extensions dropped by edit: %v", env.Data["extensions"])
	}
	if env.Data["name"] != "Ashfall the Second" {
		t.Errorf("exported name = %v", env.Data["name"])
	}

	// Same validation as create.
	form.Name = "   "
	if _, err := svc.Update(imported.ID, form); err == nil {
		t.Error("blank name accepted on update")
	}
	if _, err := svc.Update(9999, before.Form); err == nil {
		t.Error("update of a missing character succeeded")
	}
}

func TestUpdateAvatarKeepReplaceRemove(t *testing.T) {
	svc := newService(t)
	imported, err := svc.Import(cardFixture(t, "v3.png"), "card.png")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(imported.ID)
	// Empty AvatarB64 keeps the image.
	if v, err := svc.Update(imported.ID, d.Form); err != nil || !v.HasAvatar {
		t.Errorf("avatar should be kept: %+v %v", v, err)
	}
	form := d.Form
	form.AvatarB64 = "bm90IGEgcG5n" // "not a png"
	if _, err := svc.Update(imported.ID, form); err == nil {
		t.Error("non-PNG avatar accepted")
	}
	form = d.Form
	form.RemoveAvatar = true
	if v, err := svc.Update(imported.ID, form); err != nil || v.HasAvatar {
		t.Errorf("avatar should be removed: %+v %v", v, err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	svc := newService(t)
	created := mustCreate(t, svc, CardForm{
		Name: "Round Trip", Nickname: "Trip", Description: "desc", Personality: "pers",
		Scenario: "scen", Greeting: "hi {{user}}", AlternateGreetings: []string{"hey", "yo"},
		MesExample: "<START>\n{{user}}: a\n{{char}}: b", SystemPrompt: "sys",
		PostHistoryInstructions: "phi", CreatorNotes: "notes", Tags: []string{"a", "b"},
	})
	text, err := svc.ExportJSON(created.ID)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	if _, err := card.ParseJSON([]byte(text)); err != nil {
		t.Fatalf("export is not a parseable card: %v", err)
	}
	reimported, err := svc.Import(base64Of(text), "round-trip.json")
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	a, _ := svc.Get(created.ID)
	b, _ := svc.Get(reimported.ID)
	if !reflect.DeepEqual(a.Form, b.Form) {
		t.Errorf("round trip changed fields:\n%+v\n%+v", a.Form, b.Form)
	}
	ta, _ := svc.ExportJSON(created.ID)
	tb, _ := svc.ExportJSON(reimported.ID)
	if ta != tb {
		t.Errorf("round trip changed JSON:\n%s\n%s", ta, tb)
	}
}

func TestDuplicateNamesCopies(t *testing.T) {
	svc := newService(t)
	orig := mustCreate(t, svc, CardForm{Name: "Ember", Description: "d", Tags: []string{"t"}})
	c1, err := svc.Duplicate(orig.ID)
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if c1.Name != "Ember (copy)" {
		t.Errorf("first copy = %q", c1.Name)
	}
	c2, _ := svc.Duplicate(orig.ID)
	if c2.Name != "Ember (copy 2)" {
		t.Errorf("second copy = %q", c2.Name)
	}
	c3, _ := svc.Duplicate(c1.ID) // copying a copy doesn't stack suffixes
	if c3.Name != "Ember (copy 3)" {
		t.Errorf("copy of copy = %q", c3.Name)
	}
	d, _ := svc.Get(c1.ID)
	if d.Form.Description != "d" || !reflect.DeepEqual(d.Form.Tags, []string{"t"}) {
		t.Errorf("copy lost fields: %+v", d.Form)
	}
}

func TestDeleteIsSoftAndKeepsChats(t *testing.T) {
	svc := newService(t)
	v := mustCreate(t, svc, CardForm{Name: "Gone"})
	for i := 0; i < 3; i++ {
		if _, err := svc.store.CreateChatForCharacter(v.ID, "chat", "ollama", "m"); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := svc.ChatCount(v.ID); n != 3 {
		t.Errorf("ChatCount = %d, want 3", n)
	}
	if err := svc.Delete(v.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ := svc.List()
	for _, c := range list {
		if c.ID == v.ID {
			t.Error("deleted character still listed")
		}
	}
	if _, err := svc.Get(v.ID); err == nil {
		t.Error("Get of a deleted character should fail")
	}
	if _, err := svc.Update(v.ID, CardForm{Name: "x"}); err == nil {
		t.Error("Update of a deleted character should fail")
	}
	// Row, card, avatar and chats all survive.
	row, ok, _ := svc.store.GetCharacter(v.ID)
	if !ok || row.DeletedAt == 0 || row.CardJSON == "" {
		t.Errorf("row should remain with deleted_at set: %+v ok=%v", row, ok)
	}
	if n, _ := svc.ChatCount(v.ID); n != 3 {
		t.Errorf("chats deleted as a side effect: %d left", n)
	}
	chats, _ := svc.store.ListChats()
	flagged := 0
	for _, c := range chats {
		if c.CharacterID == v.ID && c.CharacterDeleted && c.CharacterName == "Gone" {
			flagged++
		}
	}
	if flagged != 3 {
		t.Errorf("chat list should show 3 flagged chats, got %d", flagged)
	}
	if err := svc.Delete(v.ID); err == nil {
		t.Error("double delete should fail")
	}
	// A deleted character can still be exported (its chats show it).
	if text, err := svc.ExportJSON(v.ID); err != nil || !strings.Contains(text, "Gone") {
		t.Errorf("export of deleted character: %v", err)
	}
}

func TestSafeFilename(t *testing.T) {
	for in, want := range map[string]string{"Ember": "Ember", "A/B: c?": "A_B_ c_", "  ": "character", "Ms. O'Neil": "Ms. O_Neil"} {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
