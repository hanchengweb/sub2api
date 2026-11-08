package mediatasks

import (
	_ "embed"
	"encoding/json"
)

// Voice is a documented preset for the domestic seed-tts-2.0 resource.
// ICL_ presets are public speakers, distinct from private S_ clone slots.
type Voice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Language    string `json:"language,omitempty"`
	Description string `json:"description,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
}

//go:embed seed_tts_voices.json
var voiceCatalogJSON []byte

var Voices = loadVoices()

func loadVoices() []Voice {
	var catalog struct {
		Voices []Voice `json:"voices"`
	}
	if err := json.Unmarshal(voiceCatalogJSON, &catalog); err != nil {
		panic("invalid embedded speech voice catalog")
	}
	return catalog.Voices
}

func ValidVoice(id string) bool {
	for _, voice := range Voices {
		if voice.ID == id {
			return true
		}
	}
	return false
}
