package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"loom/internal/store"
)

// runScript implements `loom script <list|export|import>` — reusable
// scripts' portability surface (Profile-Centric UX Phase 4), mirroring
// `loom schema`: scripts live in loom.db (already covered by
// `loom backup`); these commands exist for cross-instance moves and
// review.
func runScript(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom script <list|export|import> [options]")
	}
	switch args[0] {
	case "list":
		return runScriptList(args[1:])
	case "export":
		return runScriptExport(args[1:])
	case "import":
		return runScriptImport(args[1:])
	default:
		return fmt.Errorf("unknown script subcommand %q (want list, export or import)", args[0])
	}
}

func runScriptList(args []string) error {
	fs := flag.NewFlagSet("script list", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	list, err := st.ListScripts()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no scripts")
		return nil
	}
	for _, d := range list {
		owner := ""
		if d.Owner != "" {
			owner = " owner=" + d.Owner
		}
		usage := ""
		if d.UsageCount > 0 {
			usage = fmt.Sprintf(" used-by=%d", d.UsageCount)
		}
		fmt.Printf("%s\t%s\t%s\t%d bytes\tv%d%s%s\n", d.ID, d.Name, d.Language, len(d.Source), d.Version, owner, usage)
	}
	return nil
}

// scriptExportEntry is one script in an export file. Only the current
// version travels: import on another instance creates it at v1 (or bumps
// an existing one), and version history is instance-local by design.
type scriptExportEntry struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Language       string   `json:"language"`
	Description    string   `json:"description,omitempty"`
	Source         string   `json:"source"`
	Requirements   []string `json:"requirements,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	MaxMemoryMB    int      `json:"maxMemoryMB,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Owner          string   `json:"owner,omitempty"`
}

func runScriptExport(args []string) error {
	fs := flag.NewFlagSet("script export", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	out := fs.String("out", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	list, err := st.ListScripts()
	if err != nil {
		return err
	}
	// Optional id/name filters; no filters means every script.
	wanted := map[string]bool{}
	for _, arg := range fs.Args() {
		wanted[arg] = true
	}
	entries := []scriptExportEntry{}
	for _, d := range list {
		if len(wanted) > 0 && !wanted[d.ID] && !wanted[d.Name] {
			continue
		}
		entries = append(entries, scriptExportEntry{
			ID: d.ID, Name: d.Name, Language: d.Language, Description: d.Description,
			Source: d.Source, Requirements: d.Requirements,
			TimeoutSeconds: d.TimeoutSeconds, MaxMemoryMB: d.MaxMemoryMB,
			Tags: d.Tags, Owner: d.Owner,
		})
	}
	if len(entries) == 0 {
		return errors.New("no scripts matched")
	}
	raw, err := json.MarshalIndent(map[string]any{"kind": "loom-scripts", "scripts": entries}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if *out == "" {
		_, err = os.Stdout.Write(raw)
		return err
	}
	return os.WriteFile(*out, raw, 0o644)
}

func runScriptImport(args []string) error {
	fs := flag.NewFlagSet("script import", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom script import [options] <file>")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var file struct {
		Kind    string              `json:"kind"`
		Scripts []scriptExportEntry `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse %s: %w", fs.Arg(0), err)
	}
	if file.Kind != "loom-scripts" {
		return errors.New("not a loom script export (missing kind: loom-scripts)")
	}
	if len(file.Scripts) == 0 {
		return errors.New("no scripts in file")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, entry := range file.Scripts {
		imported, err := scriptFromEntry(entry)
		if err != nil {
			return err
		}
		existing, err := st.GetScript(entry.ID)
		switch {
		case err == nil:
			// The script exists: import as a new version when the
			// content differs, otherwise leave the instance untouched.
			if sameScriptContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			if _, err := st.UpdateScript(imported, existing.Version, "script import"); err != nil {
				return fmt.Errorf("update %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrScriptNotFound):
			if _, err := st.CreateScript(imported, "script import"); err != nil {
				return fmt.Errorf("create %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}
	return nil
}

// scriptFromEntry validates an import entry and converts it to a store
// Script (the store re-validates requirements on create/update).
func scriptFromEntry(entry scriptExportEntry) (*store.Script, error) {
	if entry.ID == "" || entry.Name == "" || entry.Source == "" {
		return nil, fmt.Errorf("script entry %q is missing id, name or source", entry.Name)
	}
	if entry.Language != store.ScriptLanguageJavaScript && entry.Language != store.ScriptLanguagePython {
		return nil, fmt.Errorf("script entry %q has unknown language %q", entry.Name, entry.Language)
	}
	return &store.Script{
		ID: entry.ID, Name: entry.Name, Language: entry.Language, Description: entry.Description,
		Source: entry.Source, Requirements: entry.Requirements,
		TimeoutSeconds: entry.TimeoutSeconds, MaxMemoryMB: entry.MaxMemoryMB,
		Tags: entry.Tags, Owner: entry.Owner,
	}, nil
}

// sameScriptContent reports whether the two scripts carry identical
// content (everything a version row would store).
func sameScriptContent(a, b *store.Script) bool {
	if a.Name != b.Name || a.Language != b.Language || a.Description != b.Description ||
		a.Source != b.Source || a.TimeoutSeconds != b.TimeoutSeconds || a.MaxMemoryMB != b.MaxMemoryMB ||
		a.Owner != b.Owner || !tagsEqual(a.Tags, b.Tags) {
		return false
	}
	ra, _ := json.Marshal(a.Requirements)
	rb, _ := json.Marshal(b.Requirements)
	return string(ra) == string(rb)
}
