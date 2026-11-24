package voxproto

import (
	_ "embed"
	"encoding/json"
)

//go:embed data/songs.json
var songsJSON []byte

// Song is a song preset of the Písničky tab (web/src/ui/songs.ts and vtx-presets.ts).
type Song struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Year        *int   `json:"year"`
	Genre       string `json:"genre"`
	Description string `json:"description"`
	AuditionSettings
}

// Songs lists the song presets followed by the bundled Tone Room presets.
var Songs []Song

func init() {
	if err := json.Unmarshal(songsJSON, &Songs); err != nil {
		panic("voxproto: data/songs.json: " + err.Error()) // embedded, generated file
	}
}
