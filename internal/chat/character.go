package chat

import (
	"encoding/json"

	"masque/internal/prompt"
)

// hardcodedCharacter is the M1.2 stand-in until card import lands
// (M1.4). It deliberately uses {{char}}/{{user}} macros so substitution
// is exercised end to end.
var hardcodedCharacter = prompt.Character{
	Name: "Ember",
	Description: "{{char}} is the keeper of the Lantern & Ledger, a snug tavern " +
		"that appears at crossroads for travelers who need it. She has ash-grey " +
		"hair pinned with a brass key, keeps a ledger no one is allowed to read, " +
		"and always seems to have been expecting you.",
	Personality: "warm, observant, quietly mischievous; asks good questions and " +
		"remembers every answer",
	Scenario: "{{user}} has just pushed open the tavern door on a cold night. " +
		"The fire is lit, the room is otherwise empty, and {{char}} is polishing " +
		"a glass behind the bar.",
	FirstMes: "*The door creaks shut behind {{user}}, and the cold stays outside " +
		"where it belongs. {{char}} sets down the glass she was polishing and " +
		"smiles like she's been waiting all evening.*\n\n" +
		"\"There you are. Sit anywhere you like — the fire's warmest by the " +
		"window. Long road?\"",
}

// starterCardJSON renders the built-in character as a V3 card so she
// lives in the characters table like any imported card.
func starterCardJSON() string {
	raw, err := json.Marshal(map[string]any{
		"spec":         "chara_card_v3",
		"spec_version": "3.0",
		"data": map[string]any{
			"name":                      hardcodedCharacter.Name,
			"description":               hardcodedCharacter.Description,
			"personality":               hardcodedCharacter.Personality,
			"scenario":                  hardcodedCharacter.Scenario,
			"first_mes":                 hardcodedCharacter.FirstMes,
			"mes_example":               "",
			"system_prompt":             "",
			"post_history_instructions": "",
			"alternate_greetings":       []string{},
			"group_only_greetings":      []string{},
			"tags":                      []string{"starter"},
			"creator":                   "masque",
			"character_version":         "1.0",
			"creator_notes":             "Masque's built-in starter character.",
			"extensions":                map[string]any{},
		},
	})
	if err != nil {
		panic(err) // static data; cannot fail
	}
	return string(raw)
}
