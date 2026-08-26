package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"masque/internal/devlog"
	"masque/internal/provider"
)

// DescribeRequest makes the fake a RequestDescriber so inspection
// capture has something to record.
func (f *fakeProvider) DescribeRequest(req provider.ChatRequest) (provider.RequestDescription, error) {
	raw, err := json.Marshal(map[string]any{"model": req.Model, "stream": !req.NoStream})
	if err != nil {
		return provider.RequestDescription{}, err
	}
	return provider.RequestDescription{
		URL:    "http://fake/api/chat",
		Body:   raw,
		Report: provider.ParamReport{Sent: map[string]any{}, Dropped: []string{}},
	}, nil
}

func f64(v float64) *float64 { return &v }
func i(v int) *int           { return &v }

func TestParamsRoundTripAndClear(t *testing.T) {
	f := newFixture(t)
	state := f.startWithModel(t)

	// Unset: all nil.
	p, err := f.svc.Params(state.ChatID)
	if err != nil {
		t.Fatalf("Params: %v", err)
	}
	if p.Temperature != nil || p.Stop != nil {
		t.Errorf("expected empty params, got %+v", p)
	}

	want := provider.SamplerParams{Temperature: f64(0.9), TopK: i(40), Stop: []string{"\nUser:"}}
	if err := f.svc.SetParams(state.ChatID, want); err != nil {
		t.Fatalf("SetParams: %v", err)
	}
	p, err = f.svc.Params(state.ChatID)
	if err != nil {
		t.Fatalf("Params: %v", err)
	}
	if p.Temperature == nil || *p.Temperature != 0.9 || p.TopK == nil || *p.TopK != 40 || len(p.Stop) != 1 {
		t.Errorf("params round-trip mismatch: %+v", p)
	}

	// All-nil clears the overrides.
	if err := f.svc.SetParams(state.ChatID, provider.SamplerParams{}); err != nil {
		t.Fatalf("SetParams(clear): %v", err)
	}
	if raw, ok, _ := f.store.GetChatParams(state.ChatID); ok {
		t.Errorf("params not cleared: %q", raw)
	}

	if err := f.svc.SetParams(9999, want); err == nil {
		t.Error("SetParams on missing chat should fail")
	}
}

func TestGenerateAppliesParamsAndStreamingToggle(t *testing.T) {
	f := newFixture(t)
	state := f.startWithModel(t)
	if err := f.svc.SetParams(state.ChatID, provider.SamplerParams{Temperature: f64(1.1)}); err != nil {
		t.Fatalf("SetParams: %v", err)
	}
	if err := f.store.SetSetting("dev.streaming", "false"); err != nil {
		t.Fatal(err)
	}
	f.fake.script = []provider.StreamEvent{{Delta: "hi"}, {Done: true}}

	if _, err := f.svc.Send(state.ChatID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))

	req := <-f.fake.reqs
	if req.Params.Temperature == nil || *req.Params.Temperature != 1.1 {
		t.Errorf("params not passed to provider: %+v", req.Params)
	}
	if !req.NoStream {
		t.Error("dev.streaming=false should set NoStream on the request")
	}
}

func TestInspectionPersistedAndFetched(t *testing.T) {
	f := newFixture(t)
	state := f.startWithModel(t)
	f.fake.script = []provider.StreamEvent{{Delta: "reply"}, {Done: true}}

	if _, err := f.svc.Send(state.ChatID, "inspect me"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	done := f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))
	payload, ok := done.args[0].(DonePayload)
	if !ok {
		t.Fatalf("done payload type %T", done.args[0])
	}

	insp, err := f.svc.Inspect(state.ChatID, payload.MessageID)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Model != "fake-model" || insp.RequestURL != "http://fake/api/chat" {
		t.Errorf("inspection basics wrong: %+v", insp)
	}
	if len(insp.Segments) == 0 {
		t.Error("inspection has no segments")
	}
	// System segments come first, and history includes the user turn.
	if insp.Segments[0].Name != "system" {
		t.Errorf("first segment = %+v", insp.Segments[0])
	}
	found := false
	for _, seg := range insp.Segments {
		if seg.Name == "history" && strings.Contains(seg.Content, "inspect me") {
			found = true
		}
	}
	if !found {
		t.Error("user turn missing from segment breakdown")
	}
	if insp.SystemTokens <= 0 || insp.ContextWindow <= 0 {
		t.Errorf("token accounting missing: %+v", insp)
	}
	if !strings.Contains(string(insp.RawRequest), `"model":"fake-model"`) {
		t.Errorf("raw request missing: %s", insp.RawRequest)
	}

	// The user message has no record and says so.
	if _, err := f.svc.Inspect(state.ChatID, payload.MessageID-1); err == nil {
		t.Error("expected an error for a message without a prompt record")
	}
}

func TestDevlogRecordsOkAndError(t *testing.T) {
	f := newFixture(t)
	log := devlog.New()
	f.svc.log = log
	state := f.startWithModel(t)

	f.fake.script = []provider.StreamEvent{{Delta: "fine"}, {Done: true}}
	if _, err := f.svc.Send(state.ChatID, "one"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))

	f.fake.script = []provider.StreamEvent{{Err: errors.New("fake blew up")}}
	if _, err := f.svc.Send(state.ChatID, "two"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:error", state.ChatID))

	entries := log.Entries()
	if len(entries) != 2 {
		t.Fatalf("got %d log entries, want 2", len(entries))
	}
	// Newest first.
	if entries[0].Status != "error" || entries[0].Error == "" {
		t.Errorf("newest entry should be the error: %+v", entries[0])
	}
	if entries[1].Status != "ok" || entries[1].Response != "fine" {
		t.Errorf("oldest entry should be ok: %+v", entries[1])
	}
	if entries[1].URL != "http://fake/api/chat" || len(entries[1].Request) == 0 {
		t.Errorf("request description missing from log: %+v", entries[1])
	}
}

func TestRequestTimeoutBoundsGeneration(t *testing.T) {
	f := newFixture(t)
	state := f.startWithModel(t)
	if err := f.store.SetSetting("dev.request_timeout_secs", "1"); err != nil {
		t.Fatal(err)
	}
	f.fake.holdOpen = true // stream stays open until ctx fires

	start := time.Now()
	if _, err := f.svc.Send(state.ChatID, "slow"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ev := f.waitEvent(t, fmt.Sprintf("chat:%d:error", state.ChatID))
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("timeout took %v, want ~1s", elapsed)
	}
	if msg, _ := ev.args[0].(string); !strings.Contains(msg, "deadline") {
		t.Errorf("error should mention the deadline: %q", msg)
	}
}

func TestContextWindowOverrides(t *testing.T) {
	f := newFixture(t)
	if got := f.svc.staticContextWindow("openai"); got != 16_384 {
		t.Errorf("default openai window = %d", got)
	}
	if err := f.store.SetSetting("provider.openai.context_window", "32768"); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.staticContextWindow("openai"); got != 32_768 {
		t.Errorf("override not applied: %d", got)
	}
	if err := f.store.SetSetting("provider.anthropic.context_window", "100000"); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.staticContextWindow("anthropic"); got != 100_000 {
		t.Errorf("anthropic override not applied: %d", got)
	}
	if got := f.svc.staticContextWindow("ollama"); got != 0 {
		t.Errorf("ollama should stay probe-driven: %d", got)
	}
}
