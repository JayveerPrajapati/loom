package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"loom/internal/store"
)

// Mapping CLI (Configuration Hub v2, D13): list, export and import the
// reusable data.map mappings. The layout mirrors `loom schema` — only the
// current version travels; version history is instance-local by design.

func runMapping(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom mapping <list|export|import> [options]")
	}
	switch args[0] {
	case "list":
		return runMappingList(args[1:])
	case "export":
		return runMappingExport(args[1:])
	case "import":
		return runMappingImport(args[1:])
	default:
		return fmt.Errorf("unknown mapping subcommand %q (want list, export or import)", args[0])
	}
}

func runMappingList(args []string) error {
	fs := flag.NewFlagSet("mapping list", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	kind := fs.String("kind", "", "only mappings of this kind (item, body, query, headers, pathParams)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind != "" {
		if err := store.CheckMappingKind(*kind); err != nil {
			return err
		}
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	list, err := st.ListMappings()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no mappings")
		return nil
	}
	for _, m := range list {
		if *kind != "" && m.Kind != *kind {
			continue
		}
		owner := ""
		if m.Owner != "" {
			owner = " owner=" + m.Owner
		}
		usage := ""
		if m.UsageCount > 0 {
			usage = fmt.Sprintf(" used-by=%d", m.UsageCount)
		}
		kindLabel := ""
		if m.Kind != "" && m.Kind != store.MappingKindItem {
			kindLabel = "\t" + m.Kind
		}
		fmt.Printf("%s\t%s\t%d rows\tv%d%s%s%s\n", m.ID, m.Name, len(m.Rows), m.Version, owner, usage, kindLabel)
	}
	return nil
}

// mappingExportEntry is one mapping in an export file.
type mappingExportEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Kind is the row shape (v3 D4): item, body, query, headers or
	// pathParams. Empty in old exports means item.
	Kind  string             `json:"kind,omitempty"`
	Rows  []store.MappingRow `json:"rows"`
	Tags  []string           `json:"tags,omitempty"`
	Owner string             `json:"owner,omitempty"`
}

func runMappingExport(args []string) error {
	fs := flag.NewFlagSet("mapping export", flag.ContinueOnError)
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

	list, err := st.ListMappings()
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, arg := range fs.Args() {
		wanted[arg] = true
	}
	entries := []mappingExportEntry{}
	for _, m := range list {
		if len(wanted) > 0 && !wanted[m.ID] && !wanted[m.Name] {
			continue
		}
		entries = append(entries, mappingExportEntry{
			ID: m.ID, Name: m.Name, Description: m.Description,
			Kind: m.Kind, Rows: m.Rows, Tags: m.Tags, Owner: m.Owner,
		})
	}
	if len(entries) == 0 {
		return errors.New("no mappings matched")
	}
	raw, err := json.MarshalIndent(map[string]any{"kind": "loom-mappings", "mappings": entries}, "", "  ")
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

func runMappingImport(args []string) error {
	fs := flag.NewFlagSet("mapping import", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom mapping import [options] <file>")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var file struct {
		Kind     string               `json:"kind"`
		Mappings []mappingExportEntry `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse %s: %w", fs.Arg(0), err)
	}
	if file.Kind != "loom-mappings" {
		return errors.New("not a loom mapping export (missing kind: loom-mappings)")
	}
	if len(file.Mappings) == 0 {
		return errors.New("no mappings in file")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, entry := range file.Mappings {
		if entry.ID == "" || entry.Name == "" || len(entry.Rows) == 0 {
			return fmt.Errorf("mapping entry %q is missing id, name or rows", entry.Name)
		}
		if entry.Kind != "" {
			if err := store.CheckMappingKind(entry.Kind); err != nil {
				return fmt.Errorf("mapping entry %q: %w", entry.Name, err)
			}
		}
		imported := &store.Mapping{
			ID: entry.ID, Name: entry.Name, Description: entry.Description,
			Kind: entry.Kind, Rows: entry.Rows, Tags: entry.Tags, Owner: entry.Owner,
		}
		existing, err := st.GetMapping(entry.ID)
		switch {
		case err == nil:
			if sameMappingContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			if _, err := st.UpdateMapping(imported, existing.Version, "mapping import"); err != nil {
				return fmt.Errorf("update %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrMappingNotFound):
			if _, err := st.CreateMapping(imported, "mapping import"); err != nil {
				return fmt.Errorf("create %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}
	return nil
}

// sameMappingContent reports whether the two mappings carry identical
// content (everything a version row would store).
func sameMappingContent(a, b *store.Mapping) bool {
	if a.Name != b.Name || a.Description != b.Description || a.Owner != b.Owner ||
		a.Kind != b.Kind || !tagsEqual(a.Tags, b.Tags) {
		return false
	}
	ra, _ := json.Marshal(a.Rows)
	rb, _ := json.Marshal(b.Rows)
	return string(ra) == string(rb)
}
