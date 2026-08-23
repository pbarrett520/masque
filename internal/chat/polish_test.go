package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"masque/internal/provider"
)

// cardWithAlternates has a first_mes plus two alternate greetings.
func cardWithAlternates() string {
	return `{
		"spec": "chara_card_v2", "spec_version": "2.0",
		"data": {
			"name": "Trio", "description": "d", "personality": "p", "scenario": "s",
			"first_mes": "greeting one for {{user}}",
			"alternate_greetings": ["greeting two", "greeting three"],
			"mes_example": "", "system_prompt": "", "post_history_instructions": "",
			"creator_notes": "", "tags": [], "creator": "", "character_version": "",
			"extensions": {}
		}
	}`
}

func (f *fixture) openImported(t *testing.T, cardJSON string) State {
	t.Helper()
	char, err := f.store.CreateCharacter("x", cardJSON, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.svc.OpenChat(char.ID)
	if err != nil {
		t.Fatalf("OpenChat: %v", err)
	}
	if err := f.svc.SetModel(state.ChatID, "ollama", "fake-model"); err != nil {
		t.Fatal(err)
	}
	state, err = f.svc.OpenChatByID(state.ChatID)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func (f *fixture) sendAndWait(t *testing.T, chatID int64, text string) {
	t.Helper()
	if _, err := f.svc.Send(chatID, text); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:done", chatID))
}

func TestAlternateGreetingsAreSwipes(t *testing.T) {
	f := newFixture(t)
	state := f.openImported(t, cardWithAlternates())

	if len(state.Messages) != 1 {
		t.Fatalf("active thread = %+v", state.Messages)
	}
	greeting := state.Messages[0]
	if greeting.SwipeCount != 3 || greeting.SwipeIndex != 1 {
		t.Fatalf("greeting swipes = %d/%d, want 1/3", greeting.SwipeIndex, greeting.SwipeCount)
	}
	if !strings.Contains(greeting.Content, "greeting one") {
		t.Errorf("first greeting active: %q", greeting.Content)
	}

	// Swipe right through the alternates and back.
	state2, err := f.svc.Swipe(state.ChatID, greeting.ID, 1)
	if err != nil {
		t.Fatalf("Swipe: %v", err)
	}
	m := state2.Messages[0]
	if m.Content != "greeting two" || m.SwipeIndex != 2 {
		t.Errorf("after swipe right = %+v", m)
	}
	state3, err := f.svc.Swipe(state.ChatID, m.ID, -1)
	if err != nil {
		t.Fatalf("Swipe back: %v", err)
	}
	if state3.Messages[0].ID != greeting.ID {
		t.Errorf("swipe back = %+v", state3.Messages[0])
	}
	if _, err := f.svc.Swipe(state.ChatID, greeting.ID, -1); err == nil {
		t.Error("swiping left past the first: want error")
	}
}

func TestRegenerateCreatesSwipe(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "take one"}, {Done: true}}
	state := f.openImported(t, importedCardJSON("Solo", ""))
	doneEvent := fmt.Sprintf("chat:%d:done", state.ChatID)

	f.sendAndWait(t, state.ChatID, "hello")

	f.fake.script = []provider.StreamEvent{{Delta: "take two"}, {Done: true}}
	if err := f.svc.Regenerate(state.ChatID); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	f.waitEvent(t, doneEvent)

	fresh, err := f.svc.OpenChatByID(state.ChatID)
	if err != nil {
		t.Fatal(err)
	}
	last := fresh.Messages[len(fresh.Messages)-1]
	if last.Content != "take two" || last.SwipeIndex != 2 || last.SwipeCount != 2 {
		t.Fatalf("after regenerate = %+v", last)
	}

	// Swipe back to the first take.
	back, err := f.svc.Swipe(state.ChatID, last.ID, -1)
	if err != nil {
		t.Fatalf("Swipe: %v", err)
	}
	prev := back.Messages[len(back.Messages)-1]
	if prev.Content != "take one" || prev.SwipeIndex != 1 {
		t.Errorf("swiped back to = %+v", prev)
	}

	// The regeneration prompt must not include the replaced reply.
	<-f.fake.reqs // send request
	regenReq := <-f.fake.reqs
	for _, m := range regenReq.Messages {
		if strings.Contains(m.Content, "take one") {
			t.Errorf("replaced reply leaked into regen prompt: %+v", regenReq.Messages)
		}
	}
}

func TestRegenerateGuards(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Done: true}}
	state := f.openImported(t, cardWithAlternates())

	// Only a greeting: nothing to regenerate.
	if err := f.svc.Regenerate(state.ChatID); err == nil {
		t.Error("greeting-only chat: want error")
	}
	if err := f.svc.Regenerate(9999); err == nil {
		t.Error("missing chat: want error")
	}
}

func TestRegenerateCanceledEmptyRestoresPrevious(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "the original"}, {Done: true}}
	state := f.openImported(t, importedCardJSON("Solo", ""))
	doneEvent := fmt.Sprintf("chat:%d:done", state.ChatID)

	f.sendAndWait(t, state.ChatID, "hello")

	// Regeneration that gets canceled before any delta arrives.
	f.fake.script = nil
	f.fake.holdOpen = true
	if err := f.svc.Regenerate(state.ChatID); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the stream open
	f.svc.Stop(state.ChatID)
	f.waitEvent(t, doneEvent)

	fresh, err := f.svc.OpenChatByID(state.ChatID)
	if err != nil {
		t.Fatal(err)
	}
	last := fresh.Messages[len(fresh.Messages)-1]
	if last.Content != "the original" {
		t.Fatalf("previous reply not restored: %+v", fresh.Messages)
	}
	if last.Role != provider.RoleAssistant {
		t.Errorf("restored message role = %q", last.Role)
	}
}

func TestEditMessage(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "reply"}, {Done: true}}
	state := f.openImported(t, importedCardJSON("Solo", ""))

	f.sendAndWait(t, state.ChatID, "my mesage with a typo")
	fresh, _ := f.svc.OpenChatByID(state.ChatID)
	userMsg := fresh.Messages[len(fresh.Messages)-2]

	if err := f.svc.EditMessage(state.ChatID, userMsg.ID, "my message, fixed"); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	fresh, _ = f.svc.OpenChatByID(state.ChatID)
	if fresh.Messages[len(fresh.Messages)-2].Content != "my message, fixed" {
		t.Errorf("edit not applied: %+v", fresh.Messages)
	}

	if err := f.svc.EditMessage(state.ChatID, userMsg.ID, "   "); err == nil {
		t.Error("blank edit: want error")
	}
	if err := f.svc.EditMessage(state.ChatID, 9999, "x"); err == nil {
		t.Error("missing message: want error")
	}
}

func TestChatListNewAndDelete(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Done: true}}
	state := f.openImported(t, importedCardJSON("Solo", ""))

	second, err := f.svc.NewChat(state.CharacterID)
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	if second.ChatID == state.ChatID {
		t.Fatal("NewChat reused the existing chat")
	}
	if len(second.Messages) != 1 {
		t.Errorf("new chat not seeded with greeting: %+v", second.Messages)
	}

	list, err := f.svc.ListChats()
	if err != nil || len(list) != 2 {
		t.Fatalf("ListChats = %+v err=%v", list, err)
	}

	// StartChat resumes the most recently active chat (the new one).
	resumed, err := f.svc.StartChat()
	if err != nil || resumed.ChatID != second.ChatID {
		t.Errorf("resumed chat %d, want %d (err=%v)", resumed.ChatID, second.ChatID, err)
	}

	if err := f.svc.DeleteChat(second.ChatID); err != nil {
		t.Fatalf("DeleteChat: %v", err)
	}
	list, _ = f.svc.ListChats()
	if len(list) != 1 || list[0].ID != state.ChatID {
		t.Errorf("after delete = %+v", list)
	}
	// Resume falls back to the character's remaining chat.
	resumed, err = f.svc.StartChat()
	if err != nil || resumed.ChatID != state.ChatID {
		t.Errorf("resume after delete: %+v err=%v", resumed, err)
	}
}

func TestPersonaRoundTripAndPromptUse(t *testing.T) {
	f := newFixture(t)
	// Legacy fallback: display_name only.
	if err := f.store.SetSetting("user.display_name", `"OldName"`); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Persona()
	if err != nil || p.Name != "OldName" {
		t.Errorf("legacy fallback = %+v err=%v", p, err)
	}

	if err := f.svc.SetPersona("Pat", "a tired archivist"); err != nil {
		t.Fatalf("SetPersona: %v", err)
	}
	p, err = f.svc.Persona()
	if err != nil || p.Name != "Pat" || p.Description != "a tired archivist" {
		t.Errorf("persona = %+v err=%v", p, err)
	}
	if err := f.svc.SetPersona("  ", ""); err == nil {
		t.Error("blank persona name: want error")
	}

	// The persona flows into the prompt.
	f.fake.script = []provider.StreamEvent{{Done: true}}
	state := f.openImported(t, importedCardJSON("Solo", ""))
	f.sendAndWait(t, state.ChatID, "hi")
	req := <-f.fake.reqs
	if !strings.Contains(req.System, "Pat is: a tired archivist") {
		t.Errorf("persona missing from system prompt:\n%s", req.System)
	}
}
