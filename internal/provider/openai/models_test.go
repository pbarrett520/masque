package openai

import (
	"strings"
	"testing"
)

func verdictOf(t *testing.T, body, id string) (bool, bool) {
	t.Helper()
	models, err := decodeModelList(strings.NewReader(body))
	if err != nil {
		t.Fatalf("decodeModelList: %v", err)
	}
	for _, m := range models {
		if m.ID == id {
			if m.Chat == nil {
				return false, false
			}
			return *m.Chat, true
		}
	}
	t.Fatalf("model %q not decoded", id)
	return false, false
}

func TestDecodeModelListMetadata(t *testing.T) {
	openrouter := `{"data":[
	  {"id":"a/chat","architecture":{"modality":"text->text","input_modalities":["text"],"output_modalities":["text"]}},
	  {"id":"a/imagegen","architecture":{"modality":"text+image->text+image","output_modalities":["text","image"]}},
	  {"id":"a/embed","architecture":{"output_modalities":["embeddings"]}},
	  {"id":"a/legacy","architecture":{"modality":"text+image->text"}}
	]}`
	for id, want := range map[string]bool{"a/chat": true, "a/imagegen": false, "a/embed": false, "a/legacy": true} {
		got, ok := verdictOf(t, openrouter, id)
		if !ok || got != want {
			t.Errorf("openrouter %s: chat=%v ok=%v, want %v", id, got, ok, want)
		}
	}

	// Together: bare array, type field.
	together := `[{"id":"t/chat","type":"chat"},{"id":"t/img","type":"image"},{"id":"t/base","type":"language"},{"id":"t/emb","type":"embedding"}]`
	if got, ok := verdictOf(t, together, "t/chat"); !ok || !got {
		t.Error("together chat type should be chat")
	}
	if got, ok := verdictOf(t, together, "t/img"); !ok || got {
		t.Error("together image type should not be chat")
	}
	if _, ok := verdictOf(t, together, "t/base"); ok {
		t.Error("together language type should be undecided")
	}

	// Mistral: capabilities.completion_chat.
	mistral := `{"data":[{"id":"mistral-embed","capabilities":{"completion_chat":false,"embedding":true}},{"id":"mistral-small-latest","capabilities":{"completion_chat":true}}]}`
	if got, ok := verdictOf(t, mistral, "mistral-embed"); !ok || got {
		t.Error("mistral embed should not be chat")
	}
	if got, ok := verdictOf(t, mistral, "mistral-small-latest"); !ok || !got {
		t.Error("mistral small should be chat")
	}

	// Plain OpenAI: no metadata at all.
	if _, ok := verdictOf(t, `{"data":[{"id":"gpt-4o","object":"model"}]}`, "gpt-4o"); ok {
		t.Error("openai entries carry no verdict")
	}
}
