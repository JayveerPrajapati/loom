// Package examples embeds the shipped example workflows so the editor can
// offer them without a filesystem.
package examples

import (
	"embed"
	"encoding/json"
	"sort"
)

//go:embed *.flow.json
var files embed.FS

type Example struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Workflow json.RawMessage `json:"workflow"`
}

func All() ([]Example, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	out := make([]Example, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := files.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		var meta struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
		out = append(out, Example{ID: meta.ID, Name: meta.Name, Workflow: json.RawMessage(raw)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
