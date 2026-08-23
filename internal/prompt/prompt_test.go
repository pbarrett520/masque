package prompt

import (
	"strings"
	"testing"

	"masque/internal/provider"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"a", 1},        // ceil(1/3.5)
		{"abc", 1},      // ceil(3/3.5)
		{"abcd", 2},     // ceil(4/3.5)
		{"1234567", 2},  // ceil(7/3.5) = 2 exactly
		{"12345678", 3}, // ceil(8/3.5)
		{"日本語", 3},      // 9 bytes → ceil(9/3.5)
		{strings.Repeat("x", 35), 10},
	}
	for _, c := range cases {
		if got := EstimateTokens(c.s); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestSubstitute(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hi {{char}}, I'm {{user}}.", "Hi Nyx, I'm Pat."},
		{"{{Char}} and {{USER}}", "Nyx and Pat"}, // case-insensitive
		{"{{ char }} / {{ user }}", "Nyx / Pat"}, // inner whitespace
		{"no macros", "no macros"},
		{"{{unknown}} stays", "{{unknown}} stays"},
	}
	for _, c := range cases {
		if got := Substitute(c.in, "Nyx", "Pat"); got != c.want {
			t.Errorf("Substitute(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func testInput() Input {
	return Input{
		Character: Character{
			Name:        "Nyx",
			Description: "A night spirit haunting {{user}}'s library.",
			Personality: "curious, wry",
			Scenario:    "A stormy evening.",
		},
		Persona: Persona{Name: "Pat", Description: "a tired archivist"},
	}
}

func TestBuildSystemAssembly(t *testing.T) {
	res := Build(testInput())

	if !strings.Contains(res.System, "You are Nyx.") {
		t.Errorf("system missing substituted default template:\n%s", res.System)
	}
	if !strings.Contains(res.System, "haunting Pat's library") {
		t.Errorf("card description not substituted:\n%s", res.System)
	}
	if !strings.Contains(res.System, "Nyx's personality: curious, wry") {
		t.Errorf("personality missing:\n%s", res.System)
	}
	if !strings.Contains(res.System, "Scenario: A stormy evening.") {
		t.Errorf("scenario missing:\n%s", res.System)
	}
	if !strings.Contains(res.System, "Pat is: a tired archivist") {
		t.Errorf("persona missing:\n%s", res.System)
	}

	wantSources := []string{
		"default_template", "card.description", "card.personality",
		"card.scenario", "persona.description",
	}
	if len(res.Segments) != len(wantSources) {
		t.Fatalf("got %d segments, want %d: %+v", len(res.Segments), len(wantSources), res.Segments)
	}
	for i, s := range res.Segments {
		if s.Source != wantSources[i] {
			t.Errorf("segment %d source = %q, want %q", i, s.Source, wantSources[i])
		}
		if s.Name != "system" {
			t.Errorf("segment %d name = %q, want system", i, s.Name)
		}
		if s.Tokens != EstimateTokens(s.Content) {
			t.Errorf("segment %d token estimate inconsistent", i)
		}
	}
	if res.SystemTokens != EstimateTokens(res.System) {
		t.Errorf("SystemTokens = %d, want %d", res.SystemTokens, EstimateTokens(res.System))
	}
}

func TestBuildSystemPromptOriginalPlaceholder(t *testing.T) {
	in := testInput()
	in.Character.SystemPrompt = "{{original}}\n\nAlso: {{char}} rhymes."
	res := Build(in)
	if !strings.Contains(res.System, "You are Nyx.") {
		t.Errorf("{{original}} not replaced with default template:\n%s", res.System)
	}
	if !strings.Contains(res.System, "Nyx rhymes.") {
		t.Errorf("card system_prompt tail missing:\n%s", res.System)
	}
	if strings.Contains(strings.ToLower(res.System), "{{original}}") {
		t.Errorf("placeholder left in output:\n%s", res.System)
	}
}

func TestBuildCardSystemPromptOverride(t *testing.T) {
	in := testInput()
	in.Character.SystemPrompt = "Custom rules for {{char}}."
	res := Build(in)

	if !strings.Contains(res.System, "Custom rules for Nyx.") {
		t.Errorf("card system_prompt not used:\n%s", res.System)
	}
	if strings.Contains(res.System, "ongoing roleplay") {
		t.Errorf("default template should be replaced by card system_prompt:\n%s", res.System)
	}
	if res.Segments[0].Source != "card.system_prompt" {
		t.Errorf("first segment source = %q, want card.system_prompt", res.Segments[0].Source)
	}
}

func TestBuildEmptyFieldsSkipped(t *testing.T) {
	in := Input{Character: Character{Name: "Nyx"}, Persona: Persona{Name: "Pat"}}
	res := Build(in)
	if len(res.Segments) != 1 {
		t.Fatalf("got %d segments, want just the template: %+v", len(res.Segments), res.Segments)
	}
	if strings.Contains(res.System, "\n\n\n") || strings.HasSuffix(res.System, "\n") {
		t.Errorf("empty fields left blank sections:\n%q", res.System)
	}
}

func TestBuildDefaults(t *testing.T) {
	res := Build(testInput())
	if res.ContextWindow != DefaultContextWindow || res.ReservedOutput != DefaultReservedOutput {
		t.Errorf("defaults not applied: window=%d reserved=%d", res.ContextWindow, res.ReservedOutput)
	}
}

func TestBuildTruncationOldestFirst(t *testing.T) {
	in := testInput()
	// Each message ~100 tokens (350 bytes). Window sized so system +
	// reserved leave room for ~3 messages.
	msg := strings.Repeat("x", 350)
	for i := 0; i < 10; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		in.History = append(in.History, provider.Message{Role: role, Content: msg})
	}
	sys := Build(in).SystemTokens
	in.ContextWindow = sys + 100 + 320 // reserved 100, room for 3×100-token messages + slack
	in.ReservedOutput = 100

	res := Build(in)
	if res.DroppedMessages != 7 {
		t.Fatalf("dropped %d messages, want 7 (window fits 3)", res.DroppedMessages)
	}
	if len(res.Messages) != 3 {
		t.Fatalf("kept %d messages, want 3", len(res.Messages))
	}
	// The kept slice must be the newest suffix, in original order.
	for i, m := range res.Messages {
		if m != in.History[7+i] {
			t.Errorf("kept message %d is not history[%d]", i, 7+i)
		}
	}
	if res.HistoryTokens != 300 {
		t.Errorf("HistoryTokens = %d, want 300", res.HistoryTokens)
	}
}

func TestBuildKeepsNewestWhenOverBudget(t *testing.T) {
	in := testInput()
	in.History = []provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("a", 4000)},
		{Role: provider.RoleUser, Content: strings.Repeat("b", 4000)},
	}
	in.ContextWindow = 64 // absurdly small; even one message overflows
	in.ReservedOutput = 32

	res := Build(in)
	if len(res.Messages) != 1 {
		t.Fatalf("kept %d messages, want exactly the newest", len(res.Messages))
	}
	if res.Messages[0].Content[0] != 'b' {
		t.Error("kept the oldest message instead of the newest")
	}
	if res.DroppedMessages != 1 {
		t.Errorf("DroppedMessages = %d, want 1", res.DroppedMessages)
	}
}

func TestBuildNoHistory(t *testing.T) {
	res := Build(testInput())
	if len(res.Messages) != 0 || res.DroppedMessages != 0 || res.HistoryTokens != 0 {
		t.Errorf("empty history mishandled: %+v", res)
	}
}

func TestBuildHistorySegments(t *testing.T) {
	in := testInput()
	in.History = []provider.Message{
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi there"},
	}
	res := Build(in)
	var hist []Segment
	for _, s := range res.Segments {
		if s.Name == "history" {
			hist = append(hist, s)
		}
	}
	if len(hist) != 2 {
		t.Fatalf("got %d history segments, want 2", len(hist))
	}
	if hist[0].Source != "message[user]" || hist[1].Source != "message[assistant]" {
		t.Errorf("history segment sources = %q, %q", hist[0].Source, hist[1].Source)
	}
}
