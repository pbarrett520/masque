package settings

import (
	"path/filepath"
	"reflect"
	"testing"

	"masque/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "masque.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st)
}

func TestGetUnsetReturnsNil(t *testing.T) {
	svc := newTestService(t)
	v, err := svc.Get("nope")
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Errorf("got %v, want nil", v)
	}
}

func TestSetGetRoundTripsJSONValues(t *testing.T) {
	svc := newTestService(t)
	cases := []struct {
		key   string
		value any
	}{
		{"string", "hello"},
		{"number", 42.0},
		{"bool", true},
		{"object", map[string]any{"theme": "dark", "scale": 1.5}},
		{"array", []any{"a", "b"}},
	}
	for _, c := range cases {
		if err := svc.Set(c.key, c.value); err != nil {
			t.Fatalf("Set(%s): %v", c.key, err)
		}
		got, err := svc.Get(c.key)
		if err != nil {
			t.Fatalf("Get(%s): %v", c.key, err)
		}
		if !reflect.DeepEqual(got, c.value) {
			t.Errorf("Get(%s) = %#v, want %#v", c.key, got, c.value)
		}
	}
}

func TestSetNilDeletes(t *testing.T) {
	svc := newTestService(t)
	if err := svc.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Set("k", nil); err != nil {
		t.Fatal(err)
	}
	v, err := svc.Get("k")
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Errorf("got %v after delete, want nil", v)
	}
}
