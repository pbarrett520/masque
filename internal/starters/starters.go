// Package starters bundles the characters a fresh install starts with:
// three original, SFW cards stored as V3 JSON in exactly the format the
// importer accepts (so they double as reference cards), each with an
// avatar. Seed inserts them once per install; Restore re-adds any the
// user deleted without touching ones they edited.
//
// Each card carries extensions.masque.starter = "<id>" — the marker
// that ties a row back to its bundled source. That extensions.masque
// object is also where per-character fields coming later (recommended
// model, lorebook links) will live.
package starters

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"masque/internal/card"
	"masque/internal/store"
)

//go:embed cards/*.json avatars/*.png
var files embed.FS

// SeededSetting records that the one-time seed ran. A starter the user
// deletes never comes back on its own; only Restore re-adds it.
const SeededSetting = "seed.starters"

// Starter is one bundled card.
type Starter struct {
	ID     string // marker value, also the file stem
	Name   string
	Card   []byte // V3 JSON, verbatim from the embedded file
	Avatar []byte // PNG
}

// All returns the bundled starters in display order.
func All() ([]Starter, error) {
	entries, err := fs.ReadDir(files, "cards")
	if err != nil {
		return nil, fmt.Errorf("reading starter cards: %w", err)
	}
	var out []Starter
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		raw, err := files.ReadFile("cards/" + e.Name())
		if err != nil {
			return nil, err
		}
		parsed, err := card.ParseJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("starter %s: %w", id, err)
		}
		if got := MarkerOf(raw); got != id {
			return nil, fmt.Errorf("starter %s: extensions.masque.starter is %q", id, got)
		}
		avatar, err := files.ReadFile("avatars/" + id + ".png")
		if err != nil {
			return nil, fmt.Errorf("starter %s: avatar: %w", id, err)
		}
		out = append(out, Starter{ID: id, Name: parsed.Name, Card: raw, Avatar: avatar})
	}
	// Display order: the sci-fi hook first, the game master, then the
	// tutor. Everything else alphabetical after.
	rank := map[string]int{"wren": 0, "narrator": 1, "laozhang": 2}
	sort.SliceStable(out, func(i, j int) bool {
		ri, iok := rank[out[i].ID]
		rj, jok := rank[out[j].ID]
		switch {
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// MarkerOf returns the card's extensions.masque.starter value, "" when
// the card is not a starter.
func MarkerOf(cardJSON []byte) string {
	var env struct {
		Spec string `json:"spec"`
		Data struct {
			Extensions struct {
				Masque struct {
					Starter string `json:"starter"`
				} `json:"masque"`
			} `json:"extensions"`
		} `json:"data"`
		// V1 cards keep extensions at the top level.
		Extensions struct {
			Masque struct {
				Starter string `json:"starter"`
			} `json:"masque"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(cardJSON, &env); err != nil {
		return ""
	}
	if env.Spec == "" {
		return env.Extensions.Masque.Starter
	}
	return env.Data.Extensions.Masque.Starter
}

// Seed inserts every starter once per install. It is a no-op after the
// first run, whatever the user did to the rows since.
func Seed(st *store.Store) error {
	if _, done, err := st.GetSetting(SeededSetting); err != nil {
		return err
	} else if done {
		return nil
	}
	if _, err := Restore(st); err != nil {
		return err
	}
	return st.SetSetting(SeededSetting, "true")
}

// Restore re-adds every starter that has no live row carrying its
// marker. Edited starters keep the marker and are left alone; deleted
// ones (deleted_at set) come back as fresh rows. Returns the names
// restored, in order.
func Restore(st *store.Store) ([]string, error) {
	all, err := All()
	if err != nil {
		return nil, err
	}
	present, err := liveMarkers(st)
	if err != nil {
		return nil, err
	}
	var restored []string
	// The library lists newest first, so insert in reverse display
	// order: WREN gets the highest id and the first tile.
	for i := len(all) - 1; i >= 0; i-- {
		s := all[i]
		if present[s.ID] {
			continue
		}
		if _, err := st.CreateCharacter(s.Name, string(s.Card), s.Avatar); err != nil {
			return nil, fmt.Errorf("seeding %s: %w", s.Name, err)
		}
		restored = append(restored, s.Name)
	}
	return restored, nil
}

// liveMarkers maps the starter ids present among non-deleted rows.
func liveMarkers(st *store.Store) (map[string]bool, error) {
	chars, err := st.ListCharacters()
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, c := range chars {
		full, ok, err := st.GetCharacter(c.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if m := MarkerOf([]byte(full.CardJSON)); m != "" {
			present[m] = true
		}
	}
	return present, nil
}
