package chat

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"masque/internal/provider"
)

// openWithModel creates a character from cardJSON, opens its chat and
// selects the fake model.
func openWithModel(t *testing.T, f *fixture, name, cardJSON string) (int64, State) {
	t.Helper()
	char, err := f.store.CreateCharacter(name, cardJSON, nil)
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
	return char.ID, state
}

func TestEditedCardReachesNextReplyOnly(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "ok"}, {Done: true}}
	charID, state := openWithModel(t, f, "Quillon", importedCardJSON("Quillon", "Quill"))
	done := fmt.Sprintf("chat:%d:done", state.ChatID)

	if _, err := f.svc.Send(state.ChatID, "first"); err != nil {
		t.Fatal(err)
	}
	f.waitEvent(t, done)
	req := <-f.fake.reqs
	if !strings.Contains(req.System, "Speak in haiku.") {
		t.Fatalf("original card not in prompt: %s", req.System)
	}
	before, _ := f.svc.OpenChatByID(state.ChatID)

	// Edit the card: new system prompt, new first message, new name.
	edited := strings.NewReplacer(
		"Speak in haiku.", "Speak in limericks.",
		"*{{char}} nods at {{user}}.*", "*{{char}} waves at {{user}}.*",
		`"name": "Quillon"`, `"name": "Quillon Prime"`,
	).Replace(importedCardJSON("Quillon", "Quill"))
	if err := f.store.UpdateCharacter(charID, "Quillon Prime", edited, nil, false); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Send(state.ChatID, "second"); err != nil {
		t.Fatal(err)
	}
	f.waitEvent(t, done)
	req = <-f.fake.reqs
	if !strings.Contains(req.System, "Speak in limericks.") || strings.Contains(req.System, "haiku") {
		t.Errorf("next reply should use the edited card:\n%s", req.System)
	}

	// History is untouched: the greeting and earlier turns are the old
	// text, and only the two new turns were appended.
	after, _ := f.svc.OpenChatByID(state.ChatID)
	if len(after.Messages) != len(before.Messages)+2 {
		t.Fatalf("messages: %d → %d", len(before.Messages), len(after.Messages))
	}
	for i, m := range before.Messages {
		if after.Messages[i].Content != m.Content {
			t.Errorf("message %d rewritten: %q → %q", i, m.Content, after.Messages[i].Content)
		}
	}
	if !strings.Contains(after.Messages[0].Content, "nods") {
		t.Errorf("existing chat's greeting changed: %q", after.Messages[0].Content)
	}
	// A new chat gets the new greeting.
	fresh, err := f.svc.NewChat(charID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fresh.Messages[0].Content, "waves") {
		t.Errorf("new chat should use the edited greeting: %q", fresh.Messages[0].Content)
	}
}

func TestDeletedCharacterChatsAreReadOnly(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "ok"}, {Done: true}}
	charID, state := openWithModel(t, f, "Quillon", importedCardJSON("Quillon", "Quill"))
	done := fmt.Sprintf("chat:%d:done", state.ChatID)
	if _, err := f.svc.Send(state.ChatID, "before delete"); err != nil {
		t.Fatal(err)
	}
	f.waitEvent(t, done)
	<-f.fake.reqs
	countBefore := len(mustState(t, f, state.ChatID).Messages)

	if err := f.store.SoftDeleteCharacter(charID); err != nil {
		t.Fatal(err)
	}

	// Still listed, flagged, and readable with history intact.
	chats, err := f.svc.ListChats()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range chats {
		if c.ID == state.ChatID {
			found = true
			if !c.CharacterDeleted || c.CharacterName != "Quill" && c.CharacterName != "Quillon" {
				t.Errorf("chat list entry = %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("chat vanished from the list after character delete")
	}
	st := mustState(t, f, state.ChatID)
	if !st.CharacterDeleted || len(st.Messages) != countBefore {
		t.Errorf("read-only state = deleted:%v messages:%d (want %d)", st.CharacterDeleted, len(st.Messages), countBefore)
	}

	// Can't be continued in any way.
	if _, err := f.svc.Send(state.ChatID, "after delete"); !errors.Is(err, errDeletedCharacter) {
		t.Errorf("Send after delete: %v", err)
	}
	if err := f.svc.Regenerate(state.ChatID); !errors.Is(err, errDeletedCharacter) {
		t.Errorf("Regenerate after delete: %v", err)
	}
	if _, err := f.svc.NewChat(charID); !errors.Is(err, errDeletedCharacter) {
		t.Errorf("NewChat after delete: %v", err)
	}
	if _, err := f.svc.OpenChat(charID); !errors.Is(err, errDeletedCharacter) {
		t.Errorf("OpenChat after delete: %v", err)
	}
	if len(mustState(t, f, state.ChatID).Messages) != countBefore {
		t.Error("refused actions must not append messages")
	}
	// Resume on launch: the chat is still the active one and opens read-only.
	resumed, err := f.svc.StartChat()
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ChatID != state.ChatID || !resumed.CharacterDeleted {
		t.Errorf("StartChat after delete = %+v", resumed)
	}
	// Deleting the chat itself still works (the user's choice).
	if err := f.svc.DeleteChat(state.ChatID); err != nil {
		t.Errorf("DeleteChat: %v", err)
	}
}

func mustState(t *testing.T, f *fixture, chatID int64) State {
	t.Helper()
	st, err := f.svc.OpenChatByID(chatID)
	if err != nil {
		t.Fatalf("OpenChatByID: %v", err)
	}
	return st
}
