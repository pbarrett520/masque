// Package prompt assembles the context sent to providers (dev spec §5).
// The M1 pipeline is deliberately simple and fully inspectable: Build
// returns the final request pieces plus a structured breakdown
// (segment → source → token estimate). That breakdown is the seam where
// the dev-mode context inspector (M1.7) and, later, the graph engine's
// memory injections plug in.
package prompt

import (
	"regexp"
	"strings"

	"masque/internal/provider"
)

// Defaults when the model's real limits are unknown.
const (
	DefaultContextWindow  = 8192
	DefaultReservedOutput = 1024
)

// defaultTemplate is the built-in RP system preamble, used when the card
// has no system_prompt of its own.
const defaultTemplate = "You are {{char}}. Write {{char}}'s next reply in an ongoing roleplay " +
	"with {{user}}. Stay in character, be proactive, and keep replies vivid " +
	"but reasonably short. Never write {{user}}'s actions or dialogue."

// Character is the subset of a character card the builder uses. In M1.2
// it is filled from the hardcoded character; from M1.4 CardService maps
// imported V2/V3 cards onto it.
type Character struct {
	Name         string
	Description  string
	Personality  string
	Scenario     string
	SystemPrompt string // overrides defaultTemplate when set
	FirstMes     string // greeting; used by ChatService, not by Build
}

// Persona is the user's identity in the chat.
type Persona struct {
	Name        string
	Description string
}

// Input is everything Build needs.
type Input struct {
	Character Character
	Persona   Persona
	History   []provider.Message // active messages, oldest first
	// ContextWindow and ReservedOutput are token counts; zero means use
	// the package defaults.
	ContextWindow  int
	ReservedOutput int
}

// Segment is one labeled piece of the assembled context.
type Segment struct {
	Name    string `json:"name"`   // "system" or "history"
	Source  string `json:"source"` // e.g. "card.description", "default_template", "message[user]"
	Content string `json:"content"`
	Tokens  int    `json:"tokens"`
}

// Result is the assembled request plus the inspector breakdown.
type Result struct {
	System   string             // assembled system prompt
	Messages []provider.Message // history that fit the budget, oldest first
	Segments []Segment          // system parts, then included history

	ContextWindow   int // budget inputs, after defaulting
	ReservedOutput  int
	SystemTokens    int
	HistoryTokens   int
	DroppedMessages int // history messages truncated oldest-first
}

// EstimateTokens estimates the token count of s as ceil(bytes/3.5), the
// fast heuristic from dev spec §5. Revisit with a real BPE if budgeting
// proves too sloppy (open question §14.3).
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (2*len(s) + 6) / 7
}

var macroRe = regexp.MustCompile(`(?i){{\s*(char|user)\s*}}`)

// Substitute replaces the {{char}} and {{user}} macros (case-insensitive,
// tolerating inner whitespace) — the only macro language in M1.
func Substitute(s, charName, userName string) string {
	return macroRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Contains(strings.ToLower(m), "char") {
			return charName
		}
		return userName
	})
}

// Build assembles the system prompt and the history slice that fits the
// token budget: budget = context_window − reserved_output − system size,
// history truncated oldest-first, never mid-message. The newest message
// is always kept so a request is never empty, even when it alone
// overflows the budget.
func Build(in Input) Result {
	res := Result{
		ContextWindow:  in.ContextWindow,
		ReservedOutput: in.ReservedOutput,
	}
	if res.ContextWindow <= 0 {
		res.ContextWindow = DefaultContextWindow
	}
	if res.ReservedOutput <= 0 {
		res.ReservedOutput = DefaultReservedOutput
	}

	sub := func(s string) string {
		return Substitute(s, in.Character.Name, in.Persona.Name)
	}

	// System prompt: preamble (card override or default template), then
	// card fields and persona, each a labeled segment; empty parts skipped.
	type part struct {
		source string
		text   string
	}
	preamble := part{source: "default_template", text: sub(defaultTemplate)}
	if in.Character.SystemPrompt != "" {
		preamble = part{source: "card.system_prompt", text: sub(in.Character.SystemPrompt)}
	}
	parts := []part{preamble}
	if d := in.Character.Description; d != "" {
		parts = append(parts, part{source: "card.description", text: sub(d)})
	}
	if p := in.Character.Personality; p != "" {
		parts = append(parts, part{
			source: "card.personality",
			text:   in.Character.Name + "'s personality: " + sub(p),
		})
	}
	if s := in.Character.Scenario; s != "" {
		parts = append(parts, part{source: "card.scenario", text: "Scenario: " + sub(s)})
	}
	if p := in.Persona.Description; p != "" {
		parts = append(parts, part{
			source: "persona.description",
			text:   in.Persona.Name + " is: " + sub(p),
		})
	}

	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		texts = append(texts, p.text)
		res.Segments = append(res.Segments, Segment{
			Name:    "system",
			Source:  p.source,
			Content: p.text,
			Tokens:  EstimateTokens(p.text),
		})
	}
	res.System = strings.Join(texts, "\n\n")
	res.SystemTokens = EstimateTokens(res.System)

	// History: walk newest-first, include what fits, keep at least the
	// newest message.
	budget := res.ContextWindow - res.ReservedOutput - res.SystemTokens
	included := 0
	for i := len(in.History) - 1; i >= 0; i-- {
		cost := EstimateTokens(in.History[i].Content)
		if included > 0 && res.HistoryTokens+cost > budget {
			break
		}
		res.HistoryTokens += cost
		included++
	}
	res.DroppedMessages = len(in.History) - included
	res.Messages = in.History[res.DroppedMessages:]
	for _, m := range res.Messages {
		res.Segments = append(res.Segments, Segment{
			Name:    "history",
			Source:  "message[" + m.Role + "]",
			Content: m.Content,
			Tokens:  EstimateTokens(m.Content),
		})
	}
	return res
}
