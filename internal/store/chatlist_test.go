package store

import "testing"

func TestSwipeLifecycle(t *testing.T) {
	st := openTestStore(t)
	c, err := st.CreateChat("t", "ollama", "m")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(c.ID, "user", "prompt", 2, false); err != nil {
		t.Fatal(err)
	}
	first, err := st.AppendMessage(c.ID, "assistant", "take one", 3, false)
	if err != nil {
		t.Fatal(err)
	}

	// Regeneration: stamp the original into a group keyed by its id,
	// deactivate it, add an active sibling.
	if err := st.SetSwipeGroup(first.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeactivateMessage(first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := st.AppendSwipe(c.ID, "assistant", "take two", 3, false, first.ID, true)
	if err != nil {
		t.Fatal(err)
	}

	active, err := st.ActiveMessages(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[1].ID != second.ID || active[1].Content != "take two" {
		t.Fatalf("active thread = %+v", active)
	}
	if active[1].SwipeGroup != first.ID {
		t.Errorf("swipe group not scanned: %+v", active[1])
	}

	swipes, err := st.SwipesInGroup(c.ID, first.ID)
	if err != nil || len(swipes) != 2 {
		t.Fatalf("swipes = %+v err=%v", swipes, err)
	}
	if swipes[0].ID != first.ID || swipes[1].ID != second.ID {
		t.Errorf("swipe order = %+v", swipes)
	}

	// Swipe back to the original.
	if err := st.ActivateSwipe(c.ID, first.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	active, _ = st.ActiveMessages(c.ID)
	if len(active) != 2 || active[1].ID != first.ID {
		t.Fatalf("after swipe back: %+v", active)
	}

	if err := st.ActivateSwipe(c.ID, 999, first.ID); err == nil {
		t.Error("empty group: want error")
	}
}

func TestUpdateMessageContent(t *testing.T) {
	st := openTestStore(t)
	c, err := st.CreateChat("t", "ollama", "m")
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.AppendSwipe(c.ID, "assistant", "typo", 1, true, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateMessageContent(m.ID, "fixed", 2); err != nil {
		t.Fatalf("UpdateMessageContent: %v", err)
	}
	got, ok, err := st.GetMessage(m.ID)
	if err != nil || !ok {
		t.Fatalf("GetMessage: ok=%v err=%v", ok, err)
	}
	if got.Content != "fixed" || got.TokenEstimate != 2 || got.Truncated {
		t.Errorf("after edit = %+v (truncated flag should clear)", got)
	}
	if err := st.UpdateMessageContent(9999, "x", 1); err == nil {
		t.Error("missing message: want error")
	}
}

func TestListAndDeleteChats(t *testing.T) {
	st := openTestStore(t)
	char, err := st.CreateCharacter("Host", `{"name":"Host"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateChatForCharacter(char.ID, "first", "ollama", "m")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateChatForCharacter(char.ID, "second", "ollama", "m")
	if err != nil {
		t.Fatal(err)
	}
	// Make the first chat the most recently updated. (Timestamps are
	// unix seconds, so a same-second AppendMessage bump would tie.)
	if _, err := st.db.Exec("UPDATE chats SET updated_at = updated_at + 60 WHERE id = ?", first.ID); err != nil {
		t.Fatal(err)
	}
	// A characterless chat must not appear in the list.
	if _, err := st.CreateChat("orphan", "ollama", "m"); err != nil {
		t.Fatal(err)
	}

	items, err := st.ListChats()
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("list = %+v", items)
	}
	if items[0].ID != first.ID || items[0].CharacterName != "Host" {
		t.Errorf("most recent first: %+v", items)
	}

	if err := st.DeleteChat(first.ID); err != nil {
		t.Fatalf("DeleteChat: %v", err)
	}
	if msgs, _ := st.ActiveMessages(first.ID); len(msgs) != 0 {
		t.Error("messages survived chat delete")
	}
	items, _ = st.ListChats()
	if len(items) != 1 || items[0].ID != second.ID {
		t.Errorf("after delete: %+v", items)
	}
}

func TestDefaultPersona(t *testing.T) {
	st := openTestStore(t)
	if _, ok, err := st.DefaultPersona(); err != nil || ok {
		t.Errorf("fresh db: ok=%v err=%v", ok, err)
	}
	if err := st.SetDefaultPersona("Pat", "a tired archivist"); err != nil {
		t.Fatalf("SetDefaultPersona: %v", err)
	}
	p, ok, err := st.DefaultPersona()
	if err != nil || !ok || p.Name != "Pat" || p.Description != "a tired archivist" {
		t.Errorf("persona = %+v ok=%v err=%v", p, ok, err)
	}
	// Update in place, not a second row.
	if err := st.SetDefaultPersona("Patrick", "an archivist"); err != nil {
		t.Fatal(err)
	}
	p2, _, err := st.DefaultPersona()
	if err != nil || p2.ID != p.ID || p2.Name != "Patrick" {
		t.Errorf("after update = %+v err=%v", p2, err)
	}
}
