package chat

import (
	"fmt"
	"strings"
	"testing"

	"masque/internal/provider"
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

func TestStartChatSeedsStarterCharacterRow(t *testing.T) {
	f := newFixture(t)
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat: %v", err)
	}
	if state.ChatID == 0 || state.CharacterID == 0 || state.CharacterName != "Ember" {
		t.Fatalf("state = %+v", state)
	}
	chars, err := f.store.ListCharacters()
	if err != nil || len(chars) != 1 || chars[0].Name != "Ember" {
		t.Errorf("characters after seed = %+v err=%v", chars, err)
	}
	// Seeding is once-only, even across service restarts.
	again, err := f.svc.StartChat()
	if err != nil {
		t.Fatal(err)
	}
	if again.ChatID != state.ChatID {
		t.Errorf("resume opened a different chat: %d != %d", again.ChatID, state.ChatID)
	}
	chars, _ = f.store.ListCharacters()
	if len(chars) != 1 {
		t.Errorf("reseeded: %d characters", len(chars))
	}
}

func TestStartChatAdoptsLegacyDevChat(t *testing.T) {
	f := newFixture(t)
	// Simulate an M1.2/M1.3 database: characterless chat + setting.
	legacy, err := f.store.CreateChat("Ember", "ollama", "old-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AppendMessage(legacy.ID, provider.RoleAssistant, "old greeting", 2, false); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetSetting("chat.dev_chat_id", fmt.Sprintf("%d", legacy.ID)); err != nil {
		t.Fatal(err)
	}

	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat: %v", err)
	}
	if state.ChatID != legacy.ID {
		t.Errorf("adopted chat = %d, want legacy %d", state.ChatID, legacy.ID)
	}
	if len(state.Messages) != 1 || state.Messages[0].Content != "old greeting" {
		t.Errorf("legacy history lost: %+v", state.Messages)
	}
	if state.Model != "old-model" {
		t.Errorf("legacy model lost: %q", state.Model)
	}
}

func TestStartChatWithNoCharacters(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.StartChat(); err != nil {
		t.Fatal(err)
	}
	// User deletes the starter character.
	chars, _ := f.store.ListCharacters()
	if err := f.store.DeleteCharacter(chars[0].ID); err != nil {
		t.Fatal(err)
	}
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat after delete: %v", err)
	}
	if state.ChatID != 0 {
		t.Errorf("deleted character resurrected: %+v", state)
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
