//go:build starter_transcripts

package chat

// Live transcript run for the bundled starter characters: three turns
// per character against local Ollama models through the real chat
// pipeline (store, prompt builder, provider). Not part of the normal
// test run — it needs Ollama and the models below installed:
//
//	go test -tags starter_transcripts -run TestStarterTranscripts -v -timeout 60m ./internal/chat/
//
// Transcripts land in $MASQUE_TRANSCRIPT_DIR (default: the test's temp
// dir, printed at the end).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"masque/internal/store"
)

type transcriptRun struct {
	starter string // card id
	model   string
	label   string
	turns   []string
}

func TestStarterTranscripts(t *testing.T) {
	dir := os.Getenv("MASQUE_TRANSCRIPT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	const qwen = "huihui_ai/Qwen3.8-abliterated:27b"
	const gemma = "gemma4:31b"
	// The iMatrix repo's :latest tag resolves to an IQ1_M (1-bit) file,
	// which produces degenerate output; the Q4_K_M build is the fair
	// test of the RP fine-tune.
	impish := "hf.co/SicariusSicariiStuff/Impish_Nemo_12B_GGUF:Q4_K_M"
	if m := os.Getenv("MASQUE_IMPISH_MODEL"); m != "" {
		impish = m
	}

	level1 := []string{"1", "我喜欢茶。你呢？", "What is 龙井? I don't understand."}
	level3 := []string{"3", "我在附近迷路了，闻到茶香就进来了。", "我在上海住两年了，但是中文还是说得不太好。"}
	runs := []transcriptRun{
		{"laozhang", qwen, "qwen-level1", level1},
		{"laozhang", qwen, "qwen-level3", level3},
		{"laozhang", gemma, "gemma-level1", level1},
		{"laozhang", gemma, "gemma-level3", level3},
		{"narrator", impish, "impish", []string{"Horror", "I check my phone for a signal and listen.", "I call out: is anyone there?"}},
		{"wren", impish, "impish", []string{"Yes. Why am I awake early?", "Why is pod 02 under maintenance? Marlow's in that one.", "Show me the alarm log for the last three days. All of it."}},
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "transcripts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events := make(chan emitted, 1000)
	svc := NewService(st, func(event string, args ...any) { events <- emitted{event: event, args: args} }, nil)
	if _, err := svc.StartChat(); err != nil {
		t.Fatal(err)
	}
	chars, _ := st.ListCharacters()
	byStarter := map[string]int64{}
	for _, c := range chars {
		full, _, _ := st.GetCharacter(c.ID)
		if strings.Contains(full.CardJSON, `"starter": "wren"`) {
			byStarter["wren"] = c.ID
		} else if strings.Contains(full.CardJSON, `"starter": "narrator"`) {
			byStarter["narrator"] = c.ID
		} else if strings.Contains(full.CardJSON, `"starter": "laozhang"`) {
			byStarter["laozhang"] = c.ID
		}
	}

	waitDone := func(chatID int64) (string, error) {
		deadline := time.After(10 * time.Minute)
		for {
			select {
			case ev := <-events:
				switch ev.event {
				case fmt.Sprintf("chat:%d:done", chatID):
					return ev.args[0].(DonePayload).Content, nil
				case fmt.Sprintf("chat:%d:error", chatID):
					return "", fmt.Errorf("%v", ev.args[0])
				}
			case <-deadline:
				return "", fmt.Errorf("timed out")
			}
		}
	}

	only := os.Getenv("MASQUE_TRANSCRIPT_ONLY") // substring filter on the run name
	for _, run := range runs {
		name := run.starter + "-" + run.label
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			state, err := svc.NewChat(byStarter[run.starter])
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.SetModel(state.ChatID, "ollama", run.model); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			fmt.Fprintf(&out, "# %s on %s\n\n", run.starter, run.model)
			fmt.Fprintf(&out, "**%s:** %s\n\n", state.CharacterName, state.Messages[0].Content)
			for _, turn := range run.turns {
				if _, err := svc.Send(state.ChatID, turn); err != nil {
					t.Fatal(err)
				}
				reply, err := waitDone(state.ChatID)
				fmt.Fprintf(&out, "**User:** %s\n\n", turn)
				if err != nil {
					fmt.Fprintf(&out, "**ERROR:** %v\n\n", err)
					t.Errorf("turn %q: %v", turn, err)
					break
				}
				fmt.Fprintf(&out, "**%s:** %s\n\n", state.CharacterName, reply)
			}
			path := filepath.Join(dir, name+".md")
			if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s", path)
		})
	}
	t.Logf("transcripts in %s", dir)
}
