package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"loom/internal/store"
)

// runConnector implements `loom connector <list|export|import>` — the
// connector portability surface (operational-trust Campaign 4),
// mirroring `loom script`. Connectors are data: node types defined over
// http.request's trusted pipeline.
func runConnector(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom connector <list|export|import> [options]")
	}
	switch args[0] {
	case "list":
		return runConnectorList(args[1:])
	case "export":
		return runConnectorExport(args[1:])
	case "import":
		return runConnectorImport(args[1:])
	default:
		return fmt.Errorf("unknown connector subcommand %q (want list, export or import)", args[0])
	}
}

func runConnectorList(args []string) error {
	fs := flag.NewFlagSet("connector list", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	list, err := st.ListConnectors()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no connectors")
		return nil
	}
	for _, c := range list {
		owner := ""
		if c.Owner != "" {
			owner = " owner=" + c.Owner
		}
		usage := ""
		if c.UsageCount > 0 {
			usage = fmt.Sprintf(" used-by=%d", c.UsageCount)
		}
		fmt.Printf("%s\t%s\t%d ops\tv%d%s%s\n", c.ID, c.Name, len(c.Operations), c.Version, owner, usage)
	}
	return nil
}

// connectorExportEntry is one connector in an export file. Only the
// current version travels; version history is instance-local by design.
type connectorExportEntry struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Operations  []store.ConnectorOperation `json:"operations"`
	Tags        []string                   `json:"tags,omitempty"`
	Owner       string                     `json:"owner,omitempty"`
}

func runConnectorExport(args []string) error {
	fs := flag.NewFlagSet("connector export", flag.ContinueOnError)
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

	list, err := st.ListConnectors()
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, arg := range fs.Args() {
		wanted[arg] = true
	}
	entries := []connectorExportEntry{}
	for _, c := range list {
		if len(wanted) > 0 && !wanted[c.ID] && !wanted[c.Name] {
			continue
		}
		entries = append(entries, connectorExportEntry{
			ID: c.ID, Name: c.Name, Description: c.Description,
			Operations: c.Operations, Tags: c.Tags, Owner: c.Owner,
		})
	}
	if len(entries) == 0 {
		return errors.New("no connectors matched")
	}
	raw, err := json.MarshalIndent(map[string]any{"kind": "loom-connectors", "connectors": entries}, "", "  ")
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

func runConnectorImport(args []string) error {
	fs := flag.NewFlagSet("connector import", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom connector import [options] <file>")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var file struct {
		Kind       string                 `json:"kind"`
		Connectors []connectorExportEntry `json:"connectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse %s: %w", fs.Arg(0), err)
	}
	if file.Kind != "loom-connectors" {
		return errors.New("not a loom connector export (missing kind: loom-connectors)")
	}
	if len(file.Connectors) == 0 {
		return errors.New("no connectors in file")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, entry := range file.Connectors {
		imported := &store.Connector{
			ID: entry.ID, Name: entry.Name, Description: entry.Description,
			Operations: entry.Operations, Tags: entry.Tags, Owner: entry.Owner,
		}
		existing, err := st.GetConnector(entry.ID)
		switch {
		case err == nil:
			if sameConnectorContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			if _, err := st.UpdateConnector(imported, existing.Version, "connector import"); err != nil {
				return fmt.Errorf("update %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrConnectorNotFound):
			// CLI imports behave as shared (creator-less), matching the
			// other asset families.
			imported.Visibility = store.VisibilityShared
			if _, err := st.CreateConnector(imported, "connector import"); err != nil {
				return fmt.Errorf("create %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}
	return nil
}

// sameConnectorContent reports whether the two connectors carry
// identical content (everything a version row would store).
func sameConnectorContent(a, b *store.Connector) bool {
	if a.Name != b.Name || a.Description != b.Description || a.Owner != b.Owner ||
		!tagsEqual(a.Tags, b.Tags) {
		return false
	}
	oa, _ := json.Marshal(a.Operations)
	ob, _ := json.Marshal(b.Operations)
	return string(oa) == string(ob)
}
