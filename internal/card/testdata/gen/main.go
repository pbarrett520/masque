// Command gen regenerates the card fixture library in testdata/
// (invoked via `go generate ./internal/card`). All fixtures are
// original Masque test characters — no imported third-party cards —
// so the library is license-clean to commit.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"masque/internal/card"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := run(dir); err != nil {
		log.Fatal(err)
	}
}

func b64Card(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return []byte(base64.StdEncoding.EncodeToString(raw))
}

// solidPNG renders a small flat-color avatar so PNG fixtures are real,
// decodable images.
func solidPNG(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func v2Card(name string) map[string]any {
	return map[string]any{
		"spec":         "chara_card_v2",
		"spec_version": "2.0",
		"data": map[string]any{
			"name":                      name,
			"description":               "{{char}} is a fixture character used by Masque's importer tests.",
			"personality":               "deterministic, reproducible",
			"scenario":                  "{{user}} runs the test suite.",
			"first_mes":                 "*{{char}} materializes.* \"Hello, {{user}}.\"",
			"mes_example":               "<START>\n{{user}}: ping\n{{char}}: pong",
			"system_prompt":             "{{original}}\n\nAlways stay deterministic.",
			"post_history_instructions": "",
			"alternate_greetings":       []string{"*A second greeting.*", "*A third greeting.*"},
			"character_book": map[string]any{
				"extensions": map[string]any{},
				"entries": []map[string]any{{
					"keys": []string{"testing"}, "content": "Lorebooks are preserved but not injected in M1.",
					"extensions": map[string]any{}, "enabled": true, "insertion_order": 0,
				}},
			},
			"tags":              []string{"fixture"},
			"creator":           "masque-tests",
			"character_version": "1.0",
			"extensions":        map[string]any{"masque_test/keep_me": true},
		},
	}
}

func v3Card(name, nickname string) map[string]any {
	return map[string]any{
		"spec":         "chara_card_v3",
		"spec_version": "3.0",
		"data": map[string]any{
			"name":                      name,
			"nickname":                  nickname,
			"description":               "A V3 fixture character.",
			"personality":               "brisk",
			"scenario":                  "A V3 import test.",
			"first_mes":                 "\"Call me {{char}}.\"",
			"mes_example":               "",
			"system_prompt":             "",
			"post_history_instructions": "",
			"alternate_greetings":       []string{},
			"group_only_greetings":      []string{"*Group greeting.*"},
			"tags":                      []string{"fixture", "v3"},
			"creator":                   "masque-tests",
			"character_version":         "1.0",
			"creator_notes":             "Original Masque test fixture.",
			"extensions":                map[string]any{},
			"assets": []map[string]any{{
				"type": "icon", "uri": "ccdefault:", "name": "main", "ext": "png",
			}},
		},
	}
}

func run(dir string) error {
	writes := map[string][]byte{}

	// Bare JSON fixtures.
	v1, _ := json.MarshalIndent(map[string]any{
		"name":        "Willow",
		"description": "A V1-era card: bare fields, no spec envelope.",
		"personality": "quiet",
		"scenario":    "A V1 import test.",
		"first_mes":   "*Willow waves at {{user}}.*",
		"mes_example": "",
	}, "", "  ")
	writes["v1.json"] = v1
	v2, _ := json.MarshalIndent(v2Card("Ashfall"), "", "  ")
	writes["v2.json"] = v2
	v3, _ := json.MarshalIndent(v3Card("Quillon", "Quill"), "", "  ")
	writes["v3.json"] = v3
	writes["no_name.json"] = []byte(`{"description": "a card with no name field"}`)
	writes["bad.json"] = []byte(`{"name": "Broken",`)

	// PNG fixtures.
	set := func(base []byte, values map[string][]byte) []byte {
		out, err := card.SetPNGTextChunks(base, values)
		if err != nil {
			panic(err)
		}
		return out
	}
	writes["v2.png"] = set(solidPNG(color.RGBA{160, 80, 40, 255}),
		map[string][]byte{"chara": b64Card(v2Card("Marrow"))})
	writes["v3.png"] = set(solidPNG(color.RGBA{40, 80, 160, 255}),
		map[string][]byte{"ccv3": b64Card(v3Card("Lantern", ""))})
	// Both chunks present: parser must prefer ccv3.
	writes["dual.png"] = set(solidPNG(color.RGBA{80, 160, 40, 255}), map[string][]byte{
		"chara": b64Card(v2Card("WrongPick")),
		"ccv3":  b64Card(v3Card("RightPick", "")),
	})
	writes["no_card.png"] = solidPNG(color.RGBA{20, 20, 20, 255})
	writes["bad_base64.png"] = set(solidPNG(color.RGBA{90, 90, 90, 255}),
		map[string][]byte{"chara": []byte("!!! not base64 !!!")})

	for name, data := range writes {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", name, len(data))
	}
	return nil
}
