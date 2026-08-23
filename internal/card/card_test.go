package card

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v (run `go generate ./internal/card`?)", name, err)
	}
	return data
}

func TestParseV1JSON(t *testing.T) {
	c, avatar, err := Parse(fixture(t, "v1.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if avatar != nil {
		t.Error("JSON card should have no avatar")
	}
	if c.Spec != "" || c.Name != "Willow" {
		t.Errorf("spec=%q name=%q", c.Spec, c.Name)
	}
	if !strings.Contains(c.FirstMes, "{{user}}") {
		t.Errorf("first_mes = %q", c.FirstMes)
	}
	if c.HasLorebook {
		t.Error("V1 fixture has no lorebook")
	}
}

func TestParseV2JSON(t *testing.T) {
	c, _, err := Parse(fixture(t, "v2.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Spec != "chara_card_v2" || c.Name != "Ashfall" {
		t.Errorf("spec=%q name=%q", c.Spec, c.Name)
	}
	if !strings.Contains(c.SystemPrompt, "{{original}}") {
		t.Errorf("system_prompt = %q", c.SystemPrompt)
	}
	if len(c.AlternateGreetings) != 2 {
		t.Errorf("alternate_greetings = %v", c.AlternateGreetings)
	}
	if !c.HasLorebook {
		t.Error("V2 fixture has a character_book; HasLorebook should be true")
	}
	if c.DisplayName() != "Ashfall" {
		t.Errorf("DisplayName = %q", c.DisplayName())
	}
}

func TestParseV3JSON(t *testing.T) {
	c, _, err := Parse(fixture(t, "v3.json"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Spec != "chara_card_v3" || c.Name != "Quillon" {
		t.Errorf("spec=%q name=%q", c.Spec, c.Name)
	}
	if c.Nickname != "Quill" || c.DisplayName() != "Quill" {
		t.Errorf("nickname handling: %q / %q", c.Nickname, c.DisplayName())
	}
}

func TestParsePNGV2(t *testing.T) {
	c, avatar, err := Parse(fixture(t, "v2.png"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name != "Marrow" || c.Spec != "chara_card_v2" {
		t.Errorf("name=%q spec=%q", c.Name, c.Spec)
	}
	if !IsPNG(avatar) {
		t.Error("PNG import should return the PNG as the avatar")
	}
}

func TestParsePNGV3(t *testing.T) {
	c, _, err := Parse(fixture(t, "v3.png"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name != "Lantern" || c.Spec != "chara_card_v3" {
		t.Errorf("name=%q spec=%q", c.Name, c.Spec)
	}
}

func TestParsePNGPrefersCcv3(t *testing.T) {
	c, _, err := Parse(fixture(t, "dual.png"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Name != "RightPick" {
		t.Errorf("picked %q; both chunks present must resolve to the ccv3 card", c.Name)
	}
}

func TestParsePNGZTXt(t *testing.T) {
	// zTXt chunks are rare in the wild but allowed (dev spec §7); built
	// here rather than committed since SetPNGTextChunks only writes tEXt.
	raw, err := json.Marshal(map[string]any{"name": "Cinder", "description": "compressed"})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write([]byte(base64.StdEncoding.EncodeToString(raw))); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	chunkData := append(append([]byte("chara"), 0, 0), compressed.Bytes()...)

	base := fixture(t, "no_card.png")
	chunks, err := pngChunks(base)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	out.Write(pngSignature)
	for i, c := range chunks {
		writeChunk(&out, c.typ, c.data)
		if i == 0 {
			writeChunk(&out, "zTXt", chunkData)
		}
	}

	c, err := ParsePNG(out.Bytes())
	if err != nil {
		t.Fatalf("ParsePNG: %v", err)
	}
	if c.Name != "Cinder" {
		t.Errorf("name = %q", c.Name)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		fixture string
		wantErr string
	}{
		{"no_card.png", "no embedded character card"},
		{"bad_base64.png", "not valid base64"},
		{"bad.json", "not valid JSON"},
		{"no_name.json", "no character name"},
	}
	for _, tc := range cases {
		_, _, err := Parse(fixture(t, tc.fixture))
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want %q", tc.fixture, err, tc.wantErr)
		}
	}
	if _, _, err := Parse([]byte("plain text")); err == nil {
		t.Error("plain text: want error")
	}
	if _, err := ParseJSON([]byte(`{"spec":"chara_card_v9","data":{"name":"X"}}`)); err == nil {
		t.Error("unknown spec: want error")
	}
	if _, err := ParsePNG([]byte("nope")); err == nil {
		t.Error("non-PNG bytes: want error")
	}
}

func TestExportJSONUpconvertsAndPreserves(t *testing.T) {
	// V2 in → V3 out, with unknown fields intact.
	c, _, err := Parse(fixture(t, "v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ExportJSON(c)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var env struct {
		Spec        string         `json:"spec"`
		SpecVersion string         `json:"spec_version"`
		Data        map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	if env.Spec != "chara_card_v3" || env.SpecVersion != "3.0" {
		t.Errorf("envelope = %s/%s", env.Spec, env.SpecVersion)
	}
	if env.Data["name"] != "Ashfall" {
		t.Errorf("name = %v", env.Data["name"])
	}
	// The V2 card's extension namespace must survive the round trip.
	ext, _ := env.Data["extensions"].(map[string]any)
	if ext["masque_test/keep_me"] != true {
		t.Errorf("extensions not preserved: %v", env.Data["extensions"])
	}
	// Required V3 fields filled in.
	if _, ok := env.Data["group_only_greetings"]; !ok {
		t.Error("group_only_greetings not defaulted")
	}
	if _, ok := env.Data["character_book"]; !ok {
		t.Error("character_book lost in export")
	}
}

func TestExportJSONFromV1(t *testing.T) {
	c, _, err := Parse(fixture(t, "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ExportJSON(c)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	reparsed, err := ParseJSON(out)
	if err != nil {
		t.Fatalf("re-parsing export: %v", err)
	}
	if reparsed.Spec != "chara_card_v3" || reparsed.Name != "Willow" {
		t.Errorf("reparsed = %q/%q", reparsed.Spec, reparsed.Name)
	}
}

func TestExportPNGRoundTrip(t *testing.T) {
	// Import a V2 PNG, export it, re-import: V3 with the same fields,
	// exactly one authoritative card chunk.
	c, avatar, err := Parse(fixture(t, "v2.png"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ExportPNG(c, avatar)
	if err != nil {
		t.Fatalf("ExportPNG: %v", err)
	}
	texts, err := pngTextValues(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, stale := texts["chara"]; stale {
		t.Error("stale chara chunk left in exported PNG")
	}
	reparsed, _, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parsing exported PNG: %v", err)
	}
	if reparsed.Spec != "chara_card_v3" || reparsed.Name != "Marrow" {
		t.Errorf("reparsed = %q/%q", reparsed.Spec, reparsed.Name)
	}
	if reparsed.SystemPrompt != c.SystemPrompt || len(reparsed.AlternateGreetings) != len(c.AlternateGreetings) {
		t.Error("fields drifted across PNG round trip")
	}
}

// TestSmokeSeraphina parses real-world cards dropped into
// testdata/smoke/ (gitignored). These are data the fixtures were NOT
// built around; the test only asserts they import cleanly.
func TestSmokeSeraphina(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "smoke"))
	if err != nil {
		t.Skip("no testdata/smoke directory; drop real cards there for a smoke run")
	}
	ran := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".png") && !strings.HasSuffix(name, ".json")) {
			continue
		}
		ran = true
		data := fixture(t, filepath.Join("smoke", name))
		c, avatar, err := Parse(data)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if c.Name == "" || c.FirstMes == "" {
			t.Errorf("%s: parsed but looks empty: name=%q", name, c.Name)
		}
		t.Logf("%s: spec=%s name=%q lorebook=%v alt_greetings=%d avatar=%dB",
			name, c.Spec, c.Name, c.HasLorebook, len(c.AlternateGreetings), len(avatar))
	}
	if !ran {
		t.Skip("testdata/smoke is empty")
	}
}
