package store

import "testing"

func TestCharacterCRUD(t *testing.T) {
	st := openTestStore(t)

	c, err := st.CreateCharacter("Ashfall", `{"name":"Ashfall"}`, []byte{0x89, 'P'})
	if err != nil {
		t.Fatalf("CreateCharacter: %v", err)
	}
	if c.ID == 0 || !c.HasAvatar {
		t.Errorf("created = %+v", c)
	}
	noAvatar, err := st.CreateCharacter("Willow", `{"name":"Willow"}`, nil)
	if err != nil {
		t.Fatalf("CreateCharacter: %v", err)
	}

	list, err := st.ListCharacters()
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d characters, want 2", len(list))
	}
	if list[0].ID != noAvatar.ID {
		t.Error("list should be newest first")
	}
	if list[0].HasAvatar || !list[1].HasAvatar {
		t.Errorf("HasAvatar flags wrong: %+v", list)
	}
	if list[1].CardJSON != "" {
		t.Error("list should not carry card bodies")
	}

	got, ok, err := st.GetCharacter(c.ID)
	if err != nil || !ok {
		t.Fatalf("GetCharacter: ok=%v err=%v", ok, err)
	}
	if got.CardJSON != `{"name":"Ashfall"}` || !got.HasAvatar {
		t.Errorf("got = %+v", got)
	}
	if _, ok, _ := st.GetCharacter(999); ok {
		t.Error("missing character reported as found")
	}

	avatar, err := st.GetAvatar(c.ID)
	if err != nil || len(avatar) != 2 {
		t.Errorf("GetAvatar = %v err=%v", avatar, err)
	}
	if avatar, _ := st.GetAvatar(noAvatar.ID); avatar != nil {
		t.Errorf("avatar for avatarless character = %v", avatar)
	}
}

func TestDeleteCharacterCascades(t *testing.T) {
	st := openTestStore(t)
	c, err := st.CreateCharacter("Doomed", `{"name":"Doomed"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := st.CreateChatForCharacter(c.ID, "t", "ollama", "m")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(chat.ID, "assistant", "hi", 1, false); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteCharacter(c.ID); err != nil {
		t.Fatalf("DeleteCharacter: %v", err)
	}
	if _, ok, _ := st.GetCharacter(c.ID); ok {
		t.Error("character still present")
	}
	if _, ok, _ := st.GetChat(chat.ID); ok {
		t.Error("chat still present")
	}
	msgs, err := st.ActiveMessages(chat.ID)
	if err != nil || len(msgs) != 0 {
		t.Errorf("messages still present: %v err=%v", msgs, err)
	}
}

func TestChatCharacterLinks(t *testing.T) {
	st := openTestStore(t)
	c, err := st.CreateCharacter("Host", `{"name":"Host"}`, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := st.LatestChatForCharacter(c.ID); err != nil || ok {
		t.Errorf("no chats yet: ok=%v err=%v", ok, err)
	}
	first, err := st.CreateChatForCharacter(c.ID, "first", "ollama", "m")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.LatestChatForCharacter(c.ID)
	if err != nil || !ok || got.ID != first.ID {
		t.Errorf("latest = %+v ok=%v err=%v", got, ok, err)
	}

	if id, err := st.ChatCharacterID(first.ID); err != nil || id != c.ID {
		t.Errorf("ChatCharacterID = %d err=%v", id, err)
	}

	// Legacy chat (no character) can be linked after the fact.
	legacy, err := st.CreateChat("legacy", "ollama", "m")
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := st.ChatCharacterID(legacy.ID); id != 0 {
		t.Errorf("legacy chat character = %d, want 0", id)
	}
	if err := st.LinkChatCharacter(legacy.ID, c.ID); err != nil {
		t.Fatalf("LinkChatCharacter: %v", err)
	}
	if id, _ := st.ChatCharacterID(legacy.ID); id != c.ID {
		t.Errorf("after link, character = %d", id)
	}
	// The legacy chat is now the most recently updated one.
	got, _, err = st.LatestChatForCharacter(c.ID)
	if err != nil || got.ID != legacy.ID {
		t.Errorf("latest after link = %+v err=%v", got, err)
	}
}
