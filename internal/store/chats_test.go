package store

import "testing"

func TestCreateAndGetChat(t *testing.T) {
	st := openTestStore(t)

	created, err := st.CreateChat("Test chat", "ollama", "llama3:8b")
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if created.ID == 0 || created.CreatedAt == 0 {
		t.Errorf("chat not fully populated: %+v", created)
	}

	got, ok, err := st.GetChat(created.ID)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if got != created {
		t.Errorf("GetChat = %+v, want %+v", got, created)
	}

	if _, ok, err := st.GetChat(9999); err != nil || ok {
		t.Errorf("GetChat(missing): ok=%v err=%v, want false,nil", ok, err)
	}
}

func TestSetChatModel(t *testing.T) {
	st := openTestStore(t)
	c, err := st.CreateChat("t", "ollama", "old")
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if err := st.SetChatModel(c.ID, "ollama", "new-model"); err != nil {
		t.Fatalf("SetChatModel: %v", err)
	}
	got, _, err := st.GetChat(c.ID)
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if got.Model != "new-model" || got.ProviderID != "ollama" {
		t.Errorf("after SetChatModel: %+v", got)
	}
	if err := st.SetChatModel(9999, "ollama", "m"); err == nil {
		t.Error("SetChatModel on missing chat: want error")
	}
}

func TestAppendAndListMessages(t *testing.T) {
	st := openTestStore(t)
	c, err := st.CreateChat("t", "ollama", "m")
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}

	first, err := st.AppendMessage(c.ID, "assistant", "greetings", 3, false)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := st.AppendMessage(c.ID, "user", "hello", 2, false); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	partial, err := st.AppendMessage(c.ID, "assistant", "cut off mid-", 4, true)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if !partial.Truncated {
		t.Error("truncated flag not returned")
	}

	msgs, err := st.ActiveMessages(c.ID)
	if err != nil {
		t.Fatalf("ActiveMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	if msgs[0].ID != first.ID || msgs[0].Role != "assistant" || msgs[0].Content != "greetings" {
		t.Errorf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[2].Role != "assistant" {
		t.Errorf("order wrong: %+v", msgs)
	}
	if !msgs[2].Truncated || msgs[0].Truncated {
		t.Errorf("truncated flags wrong: %+v", msgs)
	}

	// Messages for another chat stay separate.
	other, err := st.CreateChat("other", "ollama", "m")
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	otherMsgs, err := st.ActiveMessages(other.ID)
	if err != nil {
		t.Fatalf("ActiveMessages: %v", err)
	}
	if len(otherMsgs) != 0 {
		t.Errorf("new chat has %d messages, want 0", len(otherMsgs))
	}
}

func TestAppendMessageRejectsBadRole(t *testing.T) {
	st := openTestStore(t)
	c, err := st.CreateChat("t", "ollama", "m")
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if _, err := st.AppendMessage(c.ID, "narrator", "x", 1, false); err == nil {
		t.Error("role outside CHECK constraint: want error")
	}
}
