// Package card imports and exports character cards (dev spec §7): PNG
// with an embedded card (tEXt/zTXt chunk, base64 JSON) and bare .json,
// V1/V2 (chara_card_v2) and V3 (chara_card_v3). The original JSON is
// stored verbatim as the canonical source; fields are read defensively.
//
//go:generate go run ./testdata/gen testdata
package card

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Card is the parsed, prompt-relevant view of a character card. Raw
// holds the complete original JSON — everything Masque doesn't use
// (lorebook, extensions, creator notes) is preserved there untouched.
type Card struct {
	Spec               string   `json:"spec"` // chara_card_v2, chara_card_v3, or "" for V1
	Name               string   `json:"name"`
	Nickname           string   `json:"nickname"` // V3; replaces {{char}} when set
	Description        string   `json:"description"`
	Personality        string   `json:"personality"`
	Scenario           string   `json:"scenario"`
	FirstMes           string   `json:"firstMes"`
	MesExample         string   `json:"mesExample"`
	SystemPrompt       string   `json:"systemPrompt"`
	AlternateGreetings []string `json:"alternateGreetings"`
	HasLorebook        bool     `json:"hasLorebook"` // preserved but not injected in M1

	Raw json.RawMessage `json:"-"`
}

// DisplayName is what {{char}} resolves to: the V3 nickname when
// present, else the name.
func (c Card) DisplayName() string {
	if c.Nickname != "" {
		return c.Nickname
	}
	return c.Name
}

// fields is the union of V1/V2/V3 data fields Masque reads.
type fields struct {
	Name               string          `json:"name"`
	Nickname           string          `json:"nickname"`
	Description        string          `json:"description"`
	Personality        string          `json:"personality"`
	Scenario           string          `json:"scenario"`
	FirstMes           string          `json:"first_mes"`
	MesExample         string          `json:"mes_example"`
	SystemPrompt       string          `json:"system_prompt"`
	AlternateGreetings []string        `json:"alternate_greetings"`
	CharacterBook      json.RawMessage `json:"character_book"`
}

// envelope is the V2/V3 wrapper around the data object.
type envelope struct {
	Spec string          `json:"spec"`
	Data json.RawMessage `json:"data"`
}

// ParseJSON parses a V1, V2, or V3 card from bare JSON.
func ParseJSON(raw []byte) (Card, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Card{}, fmt.Errorf("not valid JSON: %w", err)
	}

	body := raw // V1: fields at top level
	switch env.Spec {
	case "chara_card_v2", "chara_card_v3":
		if len(env.Data) == 0 {
			return Card{}, fmt.Errorf("%s card has no data object", env.Spec)
		}
		body = env.Data
	case "":
		// V1 has no spec field; fall through with the top-level object.
	default:
		return Card{}, fmt.Errorf("unsupported card spec %q", env.Spec)
	}

	var f fields
	if err := json.Unmarshal(body, &f); err != nil {
		return Card{}, fmt.Errorf("reading card fields: %w", err)
	}
	if strings.TrimSpace(f.Name) == "" {
		return Card{}, fmt.Errorf("card has no character name; not a character card?")
	}
	return Card{
		Spec:               env.Spec,
		Name:               f.Name,
		Nickname:           f.Nickname,
		Description:        f.Description,
		Personality:        f.Personality,
		Scenario:           f.Scenario,
		FirstMes:           f.FirstMes,
		MesExample:         f.MesExample,
		SystemPrompt:       f.SystemPrompt,
		AlternateGreetings: f.AlternateGreetings,
		HasLorebook:        len(f.CharacterBook) > 0 && string(f.CharacterBook) != "null",
		Raw:                json.RawMessage(raw),
	}, nil
}

// ParsePNG parses a card embedded in a PNG. When both a ccv3 and a
// chara chunk are present the ccv3 chunk wins (V3 spec: "if the
// application detects both, the application SHOULD use the ccv3
// chunk").
func ParsePNG(data []byte) (Card, error) {
	texts, err := pngTextValues(data)
	if err != nil {
		return Card{}, err
	}
	encoded, ok := texts["ccv3"]
	if !ok {
		encoded, ok = texts["chara"]
	}
	if !ok {
		return Card{}, fmt.Errorf("PNG has no embedded character card (no ccv3 or chara chunk)")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return Card{}, fmt.Errorf("embedded card is not valid base64: %w", err)
	}
	return ParseJSON(raw)
}

// Parse sniffs data and parses it as a PNG card (returning the PNG
// itself as the avatar) or a bare JSON card (no avatar).
func Parse(data []byte) (Card, []byte, error) {
	if IsPNG(data) {
		c, err := ParsePNG(data)
		return c, data, err
	}
	c, err := ParseJSON(data)
	return c, nil, err
}

// ExportJSON returns the card as V3 JSON (spec §7). The original data
// object is carried through untouched — unknown fields are never
// destroyed — with only the V3 envelope and required-field defaults
// applied.
func ExportJSON(c Card) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(c.Raw, &env); err != nil {
		return nil, fmt.Errorf("re-reading card: %w", err)
	}
	body := c.Raw
	if env.Spec != "" {
		body = env.Data
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("re-reading card data: %w", err)
	}
	// Required V3 fields that V1/V2 cards lack.
	for key, def := range map[string]any{
		"tags": []string{}, "creator": "", "character_version": "",
		"creator_notes": "", "system_prompt": "", "post_history_instructions": "",
		"alternate_greetings": []string{}, "group_only_greetings": []string{},
		"extensions": map[string]any{},
	} {
		if _, ok := data[key]; !ok {
			data[key] = def
		}
	}
	return json.Marshal(map[string]any{
		"spec":         "chara_card_v3",
		"spec_version": "3.0",
		"data":         data,
	})
}

// ExportPNG re-embeds the card into avatarPNG as a ccv3 chunk (spec §7:
// PNG re-embed if an avatar exists).
func ExportPNG(c Card, avatarPNG []byte) ([]byte, error) {
	raw, err := ExportJSON(c)
	if err != nil {
		return nil, err
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(raw))
	// Replace any stale card chunks so the export has one authoritative
	// ccv3 chunk and no leftover chara chunk contradicting it.
	out, err := SetPNGTextChunks(avatarPNG, map[string][]byte{"ccv3": encoded, "chara": nil})
	if err != nil {
		return nil, err
	}
	return out, nil
}
