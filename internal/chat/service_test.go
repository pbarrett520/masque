package chat

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"masque/internal/provider"
	"masque/internal/store"
)

// fakeProvider streams a scripted set of events and records the request.
type fakeProvider struct {
	script        []provider.StreamEvent // played in order; nil Err/Done entries are deltas
	errOnOpen     error                  // ChatStream fails immediately
	holdOpen      bool                   // after script, wait for ctx cancel then emit Err
	contextWindow int

	reqs chan provider.ChatRequest
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{reqs: make(chan provider.ChatRequest, 10)}
}

func (f *fakeProvider) ID() string                        { return "fake" }
func (f *fakeProvider) HealthCheck(context.Context) error { return nil }
func (f *fakeProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "fake-model"}}, nil
}

func (f *fakeProvider) ContextWindow(context.Context, string) (int, error) {
	if f.contextWindow == 0 {
		return 0, fmt.Errorf("no window")
	}
	return f.contextWindow, nil
}

func (f *fakeProvider) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	f.reqs <- req
	if f.errOnOpen != nil {
		return nil, f.errOnOpen
	}
	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		for _, ev := range f.script {
			ch <- ev
			if ev.Done || ev.Err != nil {
				return
			}
		}
		if f.holdOpen {
			<-ctx.Done()
			ch <- provider.StreamEvent{Err: ctx.Err()}
		}
	}()
	return ch, nil
}

type emitted struct {
	event string
	args  []any
}

type fixture struct {
	svc    *Service
	store  *store.Store
	fake   *fakeProvider
	events chan emitted
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	f := &fixture{store: st, fake: newFakeProvider(), events: make(chan emitted, 100)}
	f.svc = NewService(st, func(event string, args ...any) {
		f.events <- emitted{event: event, args: args}
	})
	f.svc.providerFor = func(string) (provider.Provider, error) { return f.fake, nil }
	return f
}

// waitEvent returns the next emitted event matching name, skipping others.
func (f *fixture) waitEvent(t *testing.T, name string) emitted {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-f.events:
			if ev.event == name {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %q", name)
		}
	}
}

func (f *fixture) startWithModel(t *testing.T) State {
	t.Helper()
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat: %v", err)
	}
	if err := f.svc.SetModel(state.ChatID, "ollama", "fake-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	return state
}

func TestStartChatSeedsGreeting(t *testing.T) {
	f := newFixture(t)
	if err := f.store.SetSetting("user.display_name", `"Pat"`); err != nil {
		t.Fatal(err)
	}

	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat: %v", err)
	}
	if state.CharacterName != "Ember" {
		t.Errorf("character = %q", state.CharacterName)
	}
	if len(state.Messages) != 1 {
		t.Fatalf("got %d messages, want the greeting", len(state.Messages))
	}
	greeting := state.Messages[0]
	if greeting.Role != provider.RoleAssistant {
		t.Errorf("greeting role = %q", greeting.Role)
	}
	if strings.Contains(greeting.Content, "{{") {
		t.Errorf("greeting has unsubstituted macros: %q", greeting.Content)
	}
	if !strings.Contains(greeting.Content, "Pat") {
		t.Errorf("greeting not substituted with persona name: %q", greeting.Content)
	}

	// Second start resumes the same chat without reseeding.
	again, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat (resume): %v", err)
	}
	if again.ChatID != state.ChatID {
		t.Errorf("resume created a new chat: %d != %d", again.ChatID, state.ChatID)
	}
	if len(again.Messages) != 1 {
		t.Errorf("resume has %d messages, want 1", len(again.Messages))
	}
}

func TestStartChatRecoversFromStaleChatID(t *testing.T) {
	f := newFixture(t)
	if err := f.store.SetSetting("chat.dev_chat_id", "9999"); err != nil {
		t.Fatal(err)
	}
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatalf("StartChat: %v", err)
	}
	if state.ChatID == 9999 || len(state.Messages) != 1 {
		t.Errorf("stale chat id not recovered: %+v", state)
	}
}

func TestSendStreamsAndPersists(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{
		{Delta: "Wel"},
		{Delta: "come."},
		{Done: true, Usage: &provider.Usage{PromptTokens: 50, CompletionTokens: 7}},
	}
	f.fake.contextWindow = 4096
	state := f.startWithModel(t)

	userMsg, err := f.svc.Send(state.ChatID, "Hello there")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if userMsg.Role != provider.RoleUser || userMsg.Content != "Hello there" || userMsg.ID == 0 {
		t.Errorf("returned user message = %+v", userMsg)
	}

	done := f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))
	payload := done.args[0].(DonePayload)
	if payload.Content != "Welcome." || payload.Truncated {
		t.Errorf("done payload = %+v", payload)
	}
	if payload.Usage == nil || payload.Usage.CompletionTokens != 7 {
		t.Errorf("usage = %+v", payload.Usage)
	}

	// The provider saw the assembled prompt: system with character and
	// history including greeting + user turn.
	req := <-f.fake.reqs
	if req.Model != "fake-model" {
		t.Errorf("model = %q", req.Model)
	}
	if !strings.Contains(req.System, "Ember") {
		t.Errorf("system prompt missing character:\n%s", req.System)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("got %d history messages, want greeting + user", len(req.Messages))
	}
	if req.Messages[1].Role != provider.RoleUser || req.Messages[1].Content != "Hello there" {
		t.Errorf("last history message = %+v", req.Messages[1])
	}

	// Persistence: greeting, user, assistant.
	msgs, err := f.store.ActiveMessages(state.ChatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("store has %d messages, want 3", len(msgs))
	}
	last := msgs[2]
	if last.Role != provider.RoleAssistant || last.Content != "Welcome." || last.Truncated {
		t.Errorf("persisted assistant message = %+v", last)
	}
	if last.TokenEstimate != 7 {
		t.Errorf("token estimate = %d, want provider-reported 7", last.TokenEstimate)
	}
}

func TestSendValidation(t *testing.T) {
	f := newFixture(t)
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Send(state.ChatID, "   "); err == nil {
		t.Error("empty message: want error")
	}
	if _, err := f.svc.Send(state.ChatID, "hi"); err == nil {
		t.Error("no model selected: want error")
	}
	if _, err := f.svc.Send(9999, "hi"); err == nil {
		t.Error("missing chat: want error")
	}
	// Nothing persisted by failed sends.
	msgs, _ := f.store.ActiveMessages(state.ChatID)
	if len(msgs) != 1 {
		t.Errorf("failed sends persisted messages: %d", len(msgs))
	}
}

func TestSendRejectsConcurrentGeneration(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "thinking"}}
	f.fake.holdOpen = true
	state := f.startWithModel(t)

	if _, err := f.svc.Send(state.ChatID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:delta", state.ChatID))

	if _, err := f.svc.Send(state.ChatID, "second"); err == nil {
		t.Error("second send while generating: want error")
	}

	f.svc.Stop(state.ChatID)
	f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))

	// After the first finishes, sending works again.
	f.fake.holdOpen = false
	f.fake.script = []provider.StreamEvent{{Done: true}}
	if _, err := f.svc.Send(state.ChatID, "third"); err != nil {
		t.Errorf("send after stop: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))
}

func TestStopPersistsPartialTruncated(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{{Delta: "par"}}
	f.fake.holdOpen = true
	state := f.startWithModel(t)

	if _, err := f.svc.Send(state.ChatID, "go"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, fmt.Sprintf("chat:%d:delta", state.ChatID))
	f.svc.Stop(state.ChatID)

	done := f.waitEvent(t, fmt.Sprintf("chat:%d:done", state.ChatID))
	payload := done.args[0].(DonePayload)
	if payload.Content != "par" || !payload.Truncated {
		t.Errorf("done payload = %+v, want truncated partial", payload)
	}

	msgs, _ := f.store.ActiveMessages(state.ChatID)
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant || last.Content != "par" || !last.Truncated {
		t.Errorf("persisted partial = %+v", last)
	}
}

func TestProviderErrorEmitsErrorEvent(t *testing.T) {
	f := newFixture(t)
	f.fake.script = []provider.StreamEvent{
		{Delta: "half"},
		{Err: fmt.Errorf("model crashed")},
	}
	state := f.startWithModel(t)

	if _, err := f.svc.Send(state.ChatID, "go"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	errEv := f.waitEvent(t, fmt.Sprintf("chat:%d:error", state.ChatID))
	if msg := errEv.args[0].(string); !strings.Contains(msg, "model crashed") {
		t.Errorf("error event = %q", msg)
	}
	// The partial half-reply is preserved, marked truncated.
	msgs, _ := f.store.ActiveMessages(state.ChatID)
	last := msgs[len(msgs)-1]
	if last.Content != "half" || !last.Truncated {
		t.Errorf("partial on error = %+v", last)
	}
}

func TestSetModelPersistsDefault(t *testing.T) {
	f := newFixture(t)
	state, err := f.svc.StartChat()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetModel(state.ChatID, "ollama", "fake-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	chat, _, err := f.store.GetChat(state.ChatID)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Model != "fake-model" || chat.ProviderID != "ollama" {
		t.Errorf("chat after SetModel = %+v", chat)
	}
	raw, ok, err := f.store.GetSetting("provider.default_model")
	if err != nil || !ok || raw != `"fake-model"` {
		t.Errorf("default model setting = %q ok=%v err=%v", raw, ok, err)
	}
	if raw, _, _ := f.store.GetSetting("provider.default_id"); raw != `"ollama"` {
		t.Errorf("default provider setting = %q", raw)
	}
	if err := f.svc.SetModel(state.ChatID, "ollama", ""); err == nil {
		t.Error("empty model: want error")
	}
}

func TestMidChatProviderSwitch(t *testing.T) {
	f := newFixture(t)
	local := f.fake
	cloud := newFakeProvider()
	cloud.script = []provider.StreamEvent{{Delta: "from cloud"}, {Done: true}}
	local.script = []provider.StreamEvent{{Delta: "from local"}, {Done: true}}
	f.svc.providerFor = func(id string) (provider.Provider, error) {
		switch id {
		case "", "ollama":
			return local, nil
		case "openai":
			return cloud, nil
		default:
			return nil, fmt.Errorf("unknown provider %q", id)
		}
	}
	state := f.startWithModel(t)
	doneEvent := fmt.Sprintf("chat:%d:done", state.ChatID)

	if _, err := f.svc.Send(state.ChatID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	f.waitEvent(t, doneEvent)
	<-local.reqs // consumed: first turn went to the local provider

	// Switch provider mid-chat, then keep talking.
	if err := f.svc.SetModel(state.ChatID, "openai", "cloud-model"); err != nil {
		t.Fatalf("SetModel(openai): %v", err)
	}
	if _, err := f.svc.Send(state.ChatID, "second"); err != nil {
		t.Fatalf("Send after switch: %v", err)
	}
	f.waitEvent(t, doneEvent)

	req := <-cloud.reqs
	if req.Model != "cloud-model" {
		t.Errorf("cloud provider got model %q", req.Model)
	}
	// The switched-to provider sees the full history, including turns
	// generated by the previous provider.
	var contents []string
	for _, m := range req.Messages {
		contents = append(contents, m.Content)
	}
	joined := strings.Join(contents, "|")
	if !strings.Contains(joined, "from local") || !strings.Contains(joined, "second") {
		t.Errorf("history not carried across switch: %q", joined)
	}
	select {
	case r := <-local.reqs:
		t.Errorf("local provider called after switch: %+v", r)
	default:
	}

	// Unknown provider is rejected before touching the chat.
	if err := f.svc.SetModel(state.ChatID, "nope", "m"); err == nil {
		t.Error("unknown provider: want error")
	}
}
