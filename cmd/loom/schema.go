package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"loom/internal/schemas"
	"loom/internal/store"
)

// runSchema implements `loom schema list|export|import` — data contracts'
// portability surface (Configuration Hub v2, plan V1). Contracts live in
// loom.db and are already covered by `loom backup`; these commands exist
// for cross-instance moves and review, mirroring `loom profile`.
func runSchema(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom schema <list|export|import> [options]")
	}
	switch args[0] {
	case "list":
		return runSchemaList(args[1:])
	case "export":
		return runSchemaExport(args[1:])
	case "import":
		return runSchemaImport(args[1:])
	default:
		return fmt.Errorf("unknown schema subcommand %q (want list, export or import)", args[0])
	}
}

func runSchemaList(args []string) error {
	fs := flag.NewFlagSet("schema list", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	list, err := st.ListDataSchemas()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no data schemas")
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
		fmt.Printf("%s\t%s\t%d fields\tv%d%s%s\n", d.ID, d.Name, len(d.Fields), d.Version, owner, usage)
	}
	return nil
}

// schemaExportEntry is one contract in an export file. Only the current
// version travels: import on another instance creates it at v1 (or bumps an
// existing one), and version history is instance-local by design.
type schemaExportEntry struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Fields      []schemas.Field `json:"fields"`
	Tags        []string        `json:"tags,omitempty"`
	Owner       string          `json:"owner,omitempty"`
}

func runSchemaExport(args []string) error {
	fs := flag.NewFlagSet("schema export", flag.ContinueOnError)
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

	list, err := st.ListDataSchemas()
	if err != nil {
		return err
	}
	// Optional id/name filters; no filters means every schema.
	wanted := map[string]bool{}
	for _, arg := range fs.Args() {
		wanted[arg] = true
	}
	entries := []schemaExportEntry{}
	for _, d := range list {
		if len(wanted) > 0 && !wanted[d.ID] && !wanted[d.Name] {
			continue
		}
		entries = append(entries, schemaExportEntry{
			ID: d.ID, Name: d.Name, Description: d.Description,
			Fields: d.Fields, Tags: d.Tags, Owner: d.Owner,
		})
	}
	if len(entries) == 0 {
		return errors.New("no data schemas matched")
	}
	raw, err := json.MarshalIndent(map[string]any{"kind": "loom-data-schemas", "schemas": entries}, "", "  ")
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

func runSchemaImport(args []string) error {
	fs := flag.NewFlagSet("schema import", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom schema import [options] <file>")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var file struct {
		Kind    string              `json:"kind"`
		Schemas []schemaExportEntry `json:"schemas"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse %s: %w", fs.Arg(0), err)
	}
	if file.Kind != "loom-data-schemas" {
		return errors.New("not a loom schema export (missing kind: loom-data-schemas)")
	}
	if len(file.Schemas) == 0 {
		return errors.New("no schemas in file")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, entry := range file.Schemas {
		if entry.ID == "" || entry.Name == "" || len(entry.Fields) == 0 {
			return fmt.Errorf("schema entry %q is missing id, name or fields", entry.Name)
		}
		if err := schemas.Validate(entry.Fields); err != nil {
			return fmt.Errorf("schema %s: %w", entry.ID, err)
		}
		imported := &store.DataSchema{
			ID: entry.ID, Name: entry.Name, Description: entry.Description,
			Fields: entry.Fields, Tags: entry.Tags, Owner: entry.Owner,
		}
		existing, err := st.GetDataSchema(entry.ID)
		switch {
		case err == nil:
			// The schema exists: import as a new version when the
			// content differs, otherwise leave the instance untouched.
			if sameSchemaContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			if _, err := st.UpdateDataSchema(imported, existing.Version, "schema import"); err != nil {
				return fmt.Errorf("update %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrDataSchemaNotFound):
			if _, err := st.CreateDataSchema(imported, "schema import"); err != nil {
				return fmt.Errorf("create %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}
	return nil
}

// sameSchemaContent reports whether the two contracts carry identical
// content (everything a version row would store).
func sameSchemaContent(a, b *store.DataSchema) bool {
	if a.Name != b.Name || a.Description != b.Description || a.Owner != b.Owner ||
		!tagsEqual(a.Tags, b.Tags) {
		return false
	}
	fa, _ := json.Marshal(a.Fields)
	fb, _ := json.Marshal(b.Fields)
	return string(fa) == string(fb)
}
