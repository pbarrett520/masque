package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"version":"0.30.6"}`))
	}))
	defer srv.Close()

	v, err := New(srv.URL).Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != "0.30.6" {
		t.Errorf("version = %q, want 0.30.6", v)
	}
}

func TestVersionUnreachable(t *testing.T) {
	p := New("http://127.0.0.1:1") // nothing listens here
	if _, err := p.Version(context.Background()); err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

// drainPull collects all events until close.
func drainPull(t *testing.T, events <-chan PullEvent) []PullEvent {
	t.Helper()
	var out []PullEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatal("timed out draining pull stream")
		}
	}
}

func TestPullStreamsProgressAndSuccess(t *testing.T) {
	lines := []string{
		`{"status":"pulling manifest"}`,
		`{"status":"pulling ab1c","digest":"sha256:ab1c","total":1000,"completed":250}`,
		`{"status":"pulling ab1c","digest":"sha256:ab1c","total":1000,"completed":1000}`,
		`{"status":"verifying sha256 digest"}`,
		`{"status":"writing manifest"}`,
		`{"status":"success"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding pull body: %v", err)
		}
		if body.Model != "test/model:q4" || !body.Stream {
			t.Errorf("unexpected pull body: %+v", body)
		}
		for _, l := range lines {
			_, _ = w.Write([]byte(l + "\n"))
		}
	}))
	defer srv.Close()

	events, err := New(srv.URL).Pull(context.Background(), "test/model:q4")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	got := drainPull(t, events)
	if len(got) != len(lines) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(lines), got)
	}
	if got[1].Total != 1000 || got[1].Completed != 250 || got[1].Digest != "sha256:ab1c" {
		t.Errorf("progress event mismatch: %+v", got[1])
	}
	last := got[len(got)-1]
	if !last.Done || last.Err != nil {
		t.Errorf("terminal event should be Done: %+v", last)
	}
}

func TestPullServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
		_, _ = w.Write([]byte(`{"error":"pull model manifest: file does not exist"}` + "\n"))
	}))
	defer srv.Close()

	events, err := New(srv.URL).Pull(context.Background(), "nope")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	got := drainPull(t, events)
	last := got[len(got)-1]
	if last.Err == nil || !strings.Contains(last.Err.Error(), "file does not exist") {
		t.Errorf("expected manifest error, got %+v", last)
	}
}

func TestPullHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).Pull(context.Background(), "m"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected HTTP error with detail, got %v", err)
	}
}

func TestPullCancelSurfacesContextError(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pulling ab1c","total":100,"completed":1}` + "\n"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done() // hold the stream open until the client cancels
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	events, err := New(srv.URL).Pull(ctx, "m")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	<-started
	cancel()
	got := drainPull(t, events)
	last := got[len(got)-1]
	if !errors.Is(last.Err, context.Canceled) {
		t.Errorf("terminal error = %v, want context.Canceled", last.Err)
	}
}

func TestDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/delete" || r.Method != http.MethodDelete {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "gone" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(srv.URL)
	if err := p.Delete(context.Background(), "here"); err != nil {
		t.Errorf("Delete(here): %v", err)
	}
	if err := p.Delete(context.Background(), "gone"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("Delete(gone) = %v, want not-installed error", err)
	}
}

func TestPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"m:latest","size":1000,"size_vram":900,"expires_at":"2026-08-26T12:00:00Z"}]}`))
	}))
	defer srv.Close()

	models, err := New(srv.URL).PS(context.Background())
	if err != nil {
		t.Fatalf("PS: %v", err)
	}
	if len(models) != 1 || models[0].Name != "m:latest" || models[0].SizeVRAM != 900 {
		t.Errorf("PS mismatch: %+v", models)
	}
}
