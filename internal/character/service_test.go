package character

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"masque/internal/store"
)

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st)
}

func cardFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "card", "testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func TestImportPNGAndJSON(t *testing.T) {
	svc := newService(t)

	png, err := svc.Import(cardFixture(t, "v2.png"), "marrow.png")
	if err != nil {
		t.Fatalf("Import PNG: %v", err)
	}
	if png.Name != "Marrow" || !png.HasAvatar || !png.HasLorebook || png.Spec != "chara_card_v2" {
		t.Errorf("PNG import view = %+v", png)
	}

	jsonCard, err := svc.Import(cardFixture(t, "v3.json"), "quill.json")
	if err != nil {
		t.Fatalf("Import JSON: %v", err)
	}
	if jsonCard.Name != "Quillon" || jsonCard.HasAvatar || jsonCard.Spec != "chara_card_v3" {
		t.Errorf("JSON import view = %+v", jsonCard)
	}

	list, err := svc.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %+v err=%v", list, err)
	}
	if list[0].Name != "Quillon" || list[1].Name != "Marrow" {
		t.Errorf("list order = %+v", list)
	}

	avatar, err := svc.Avatar(png.ID)
	if err != nil || !strings.HasPrefix(avatar, "data:image/png;base64,") {
		t.Errorf("Avatar = %.40q err=%v", avatar, err)
	}
	if avatar, err := svc.Avatar(jsonCard.ID); err != nil || avatar != "" {
		t.Errorf("avatarless Avatar = %q err=%v", avatar, err)
	}
}

func TestImportRejectsGarbage(t *testing.T) {
	svc := newService(t)
	if _, err := svc.Import("not-base64!!!", "x.png"); err == nil {
		t.Error("bad base64 transport: want error")
	}
	if _, err := svc.Import(cardFixture(t, "no_card.png"), "plain.png"); err == nil {
		t.Error("PNG without card: want error")
	}
	if _, err := svc.Import(cardFixture(t, "no_name.json"), "x.json"); err == nil {
		t.Error("nameless card: want error")
	}
	if list, _ := svc.List(); len(list) != 0 {
		t.Errorf("failed imports persisted: %+v", list)
	}
}

func TestCreateAndDelete(t *testing.T) {
	svc := newService(t)
	v, err := svc.Create(CreateForm{
		Name:        "  Handmade  ",
		Description: "Made in the form.",
		Greeting:    "*waves*",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.Name != "Handmade" || v.Spec != "chara_card_v3" || v.HasAvatar {
		t.Errorf("created = %+v", v)
	}

	if _, err := svc.Create(CreateForm{Name: "   "}); err == nil {
		t.Error("blank name: want error")
	}
	if _, err := svc.Create(CreateForm{Name: "X", AvatarB64: base64.StdEncoding.EncodeToString([]byte("JPEGDATA"))}); err == nil {
		t.Error("non-PNG avatar: want error")
	}

	if err := svc.Delete(v.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list, _ := svc.List(); len(list) != 0 {
		t.Errorf("after delete: %+v", list)
	}
}
