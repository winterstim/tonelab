package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"tonelab/backend/daw"
)

// chainStore keeps the effect chains the agent enumerated, beside the
// conversations. Keyed by DAW: an index from another DAW's chain means
// nothing here, and a user who switches backends must not inherit one.
type chainStore struct {
	path string
	daw  string
}

func NewChainStore(path, dawName string) *chainStore {
	return &chainStore{path: path, daw: dawName}
}

type storedChains struct {
	DAW    string              `json:"daw"`
	Tracks map[string][]daw.FX `json:"tracks"`
}

func (s *chainStore) Load() (map[int][]daw.FX, error) {
	body, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var stored storedChains
	if err := json.Unmarshal(body, &stored); err != nil || stored.DAW != s.daw {
		return nil, os.ErrNotExist
	}
	known := make(map[int][]daw.FX, len(stored.Tracks))
	for key, chain := range stored.Tracks {
		if track, err := strconv.Atoi(key); err == nil && track > 0 {
			known[track] = chain
		}
	}
	return known, nil
}

// Save writes the whole file and renames it into place, as the
// conversations are, so an interrupted write leaves the previous one.
func (s *chainStore) Save(known map[int][]daw.FX) error {
	stored := storedChains{DAW: s.daw, Tracks: make(map[string][]daw.FX, len(known))}
	for track, chain := range known {
		stored.Tracks[strconv.Itoa(track)] = chain
	}
	body, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temporary := s.path + ".new"
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}
