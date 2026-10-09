package chat

import (
	"fmt"
	"strings"
	"testing"

	"masque/internal/provider"
	"masque/internal/starters"
)

// importedCardJSON is a V3 card as the character service would store it.
func importedCardJSON(name, nickname string) string {
	return fmt.Sprintf(`{
		"spec": "chara_card_v3", "spec_version": "3.0",
		"data": {
			"name": %q, "nickname": %q,
			"description": "An imported card for {{user}}.",
			"personality": "terse",
			"scenario": "Testing imports.",
			"first_mes": "*{{char}} nods at {{user}}.*",
			"system_prompt": "{{original}}\nSpeak in haiku.",
			"alternate_greetings": [], "group_only_greetings": [],
			"tags": [], "creator": "", "character_version": "",
			"creator_notes": "", "post_history_instructions": "",
			"mes_example": "", "extensions": {}
		}
	}`, name, nickname)
}

func TestStartChatSeedsStarters(t *testing.T) {
	f := newFixture(t)
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat: %v", err)
	}
	if state.ChatID != 0 {
		t.Fatalf("fresh install should land on the Characters tab: %+v", state)
	}
	names := func() []string {
		chars, err := f.store.ListCharacters()
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(chars))
		for i, c := range chars {
			out[i] = c.Name // library (display) order
		}
		return out
	}
	if got := names(); fmt.Sprint(got) != "[WREN The Narrator Lǎo Zhāng]" {
		t.Errorf("seeded characters = %v", got)
	}
	for _, c := range names() {
		if c == "Ember" {
			t.Error("Ember must not be seeded")
		}
	}
	// Seeding is once-only, even across restarts.
	if _, err := f.svc.StartChat(); err != nil {
		t.Fatal(err)
	}
	if got := names(); len(got) != 3 {
		t.Errorf("reseeded: %v", got)
	}
	// A deleted starter stays deleted after a restart…
	chars, _ := f.store.ListCharacters()
	var narrator int64
	for _, c := range chars {
		if c.Name == "The Narrator" {
			narrator = c.ID
		}
	}
	if err := f.store.SoftDeleteCharacter(narrator); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StartChat(); err != nil {
		t.Fatal(err)
	}
	if got := names(); fmt.Sprint(got) != "[WREN Lǎo Zhāng]" {
		t.Errorf("deleted starter came back on restart: %v", got)
	}
	// …until the user asks for it, and edited ones are left alone.
	for _, c := range chars {
		if c.Name == "WREN" {
			full, _, _ := f.store.GetCharacter(c.ID)
			if err := f.store.UpdateCharacter(c.ID, "WREN (mine)", full.CardJSON, nil, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	restored, err := starters.Restore(f.store)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if fmt.Sprint(restored) != "[The Narrator]" {
		t.Errorf("restored = %v, want only The Narrator", restored)
	}
	if got := names(); fmt.Sprint(got) != "[The Narrator WREN (mine) Lǎo Zhāng]" {
		t.Errorf("after restore = %v", got)
	}
}

func TestStartChatWithNoCharacters(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.StartChat(); err != nil {
		t.Fatal(err)
	}
	// User deletes every starter.
	chars, _ := f.store.ListCharacters()
	for _, c := range chars {
		if err := f.store.DeleteCharacter(c.ID); err != nil {
			t.Fatal(err)
		}
	}
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat after delete: %v", err)
	}
	if state.ChatID != 0 {
		t.Errorf("deleted character resurrected: %+v", state)
	}
	if left, _ := f.store.ListCharacters(); len(left) != 0 {
		t.Errorf("starters reseeded: %+v", left)
	}
}

func TestOpenChatSeedsCardGreeting(t *testing.T) {
	f := newFixture(t)
	if err := f.store.SetSetting("user.display_name", `"Pat"`); err != nil {
		t.Fatal(err)
	}
	char, err := f.store.CreateCharacter("Quillon", importedCardJSON("Quillon", "Quill"), nil)
	if err != nil {
		t.Fatal(err)
	}

	state, err := f.svc.OpenChat(char.ID)
	if err != nil {
		t.Fatalf("OpenChat: %v", err)
	}
	if state.CharacterName != "Quill" {
		t.Errorf("nickname should drive display name: %q", state.CharacterName)
	}
	if len(state.Messages) != 1 || state.Messages[0].Content != "*Quill nods at Pat.*" {
		t.Errorf("greeting = %+v", state.Messages)
	}

	// Reopening resumes the same chat instead of reseeding.
	again, err := f.svc.OpenChat(char.ID)
	if err != nil || again.ChatID != state.ChatID || len(again.Messages) != 1 {
		t.Errorf("reopen: %+v err=%v", again, err)
	}

	if _, err := f.svc.OpenChat(9999); err == nil {
		t.Error("missing character: want error")
	}
}

func TestGenerateUsesCardFields(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "ok"}, {Done: true}}
	char, err := f.store.CreateCharacter("Quillon", importedCardJSON("Quillon", "Quill"), nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.svc.OpenChat(char.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetModel(state.ChatID, "ollama", "fake-model"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Send(state.ChatID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))

	req := <-f.fake.reqs
	if !strings.Contains(req.System, "Speak in haiku.") {
		t.Errorf("card system_prompt missing:\n%s", req.System)
	}
	if !strings.Contains(req.System, "You are Quill.") {
		t.Errorf("{{original}} template with nickname missing:\n%s", req.System)
	}
	if strings.Contains(req.System, "Ember") {
		t.Errorf("hardcoded character leaked into imported chat:\n%s", req.System)
	}
}
