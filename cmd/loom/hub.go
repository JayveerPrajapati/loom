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

// Hub export/import (Configuration Hub v2, V6): one bundle carrying every
// reusable hub asset — config profiles, data contracts and mappings — so a
// Loom instance (or a teammate's) can be seeded in one command. The entry
// formats are the same ones the per-kind commands use; only the envelope
// is new.

type hubBundle struct {
	Kind       string                 `json:"kind"`
	Profiles   []profileExportEntry   `json:"profiles"`
	Schemas    []schemaExportEntry    `json:"schemas"`
	Mappings   []mappingExportEntry   `json:"mappings"`
	Scripts    []scriptExportEntry    `json:"scripts"`
	Connectors []connectorExportEntry `json:"connectors"`
}

func runHub(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom hub <export|import> [options]")
	}
	switch args[0] {
	case "export":
		return runHubExport(args[1:])
	case "import":
		return runHubImport(args[1:])
	default:
		return fmt.Errorf("unknown hub subcommand %q (want export or import)", args[0])
	}
}

func runHubExport(args []string) error {
	fs := flag.NewFlagSet("hub export", flag.ContinueOnError)
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

	bundle := hubBundle{Kind: "loom-hub", Profiles: []profileExportEntry{}, Schemas: []schemaExportEntry{}, Mappings: []mappingExportEntry{}, Scripts: []scriptExportEntry{}, Connectors: []connectorExportEntry{}}

	profiles, err := st.ListConfigProfiles()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		bundle.Profiles = append(bundle.Profiles, profileExportEntry{
			ID: p.ID, Name: p.Name, NodeType: p.NodeType, Description: p.Description,
			Config: p.Config, CredentialRefs: p.CredentialRefs, Retry: p.Retry,
			Tags: p.Tags, Owner: p.Owner, Bundle: p.Bundle,
		})
	}

	schemaList, err := st.ListDataSchemas()
	if err != nil {
		return err
	}
	for _, d := range schemaList {
		bundle.Schemas = append(bundle.Schemas, schemaExportEntry{
			ID: d.ID, Name: d.Name, Description: d.Description,
			Fields: d.Fields, Tags: d.Tags, Owner: d.Owner,
		})
	}

	mappingList, err := st.ListMappings()
	if err != nil {
		return err
	}
	for _, m := range mappingList {
		bundle.Mappings = append(bundle.Mappings, mappingExportEntry{
			ID: m.ID, Name: m.Name, Description: m.Description,
			Kind: m.Kind, Rows: m.Rows, Tags: m.Tags, Owner: m.Owner,
		})
	}

	scriptList, err := st.ListScripts()
	if err != nil {
		return err
	}
	for _, s := range scriptList {
		bundle.Scripts = append(bundle.Scripts, scriptExportEntry{
			ID: s.ID, Name: s.Name, Language: s.Language, Description: s.Description,
			Source: s.Source, Requirements: s.Requirements,
			TimeoutSeconds: s.TimeoutSeconds, MaxMemoryMB: s.MaxMemoryMB,
			Tags: s.Tags, Owner: s.Owner,
		})
	}

	connectorList, err := st.ListConnectors()
	if err != nil {
		return err
	}
	for _, c := range connectorList {
		bundle.Connectors = append(bundle.Connectors, connectorExportEntry{
			ID: c.ID, Name: c.Name, Description: c.Description,
			Operations: c.Operations, Tags: c.Tags, Owner: c.Owner,
		})
	}

	if len(bundle.Profiles)+len(bundle.Schemas)+len(bundle.Mappings)+len(bundle.Scripts)+len(bundle.Connectors) == 0 {
		return errors.New("nothing to export (no profiles, schemas, mappings, scripts or connectors)")
	}
	raw, err := json.MarshalIndent(bundle, "", "  ")
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

func runHubImport(args []string) error {
	fs := flag.NewFlagSet("hub import", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom hub import [options] <file>")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var bundle hubBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("parse %s: %w", fs.Arg(0), err)
	}
	if bundle.Kind != "loom-hub" {
		return errors.New("not a loom hub bundle (missing kind: loom-hub)")
	}
	if len(bundle.Profiles)+len(bundle.Schemas)+len(bundle.Mappings)+len(bundle.Scripts)+len(bundle.Connectors) == 0 {
		return errors.New("no assets in bundle")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	for _, entry := range bundle.Profiles {
		if entry.ID == "" || entry.Name == "" || entry.NodeType == "" {
			return fmt.Errorf("profile entry %q is missing id, name or nodeType", entry.Name)
		}
		imported := &store.ConfigProfile{
			ID: entry.ID, Name: entry.Name, NodeType: entry.NodeType, Description: entry.Description,
			Config: entry.Config, CredentialRefs: entry.CredentialRefs, Retry: entry.Retry,
			Tags: entry.Tags, Owner: entry.Owner,
		}
		existing, err := st.GetConfigProfile(entry.ID)
		switch {
		case err == nil:
			if sameProfileContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			if _, err := st.UpdateConfigProfile(imported, existing.Version, "hub import"); err != nil {
				return fmt.Errorf("update profile %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrConfigProfileNotFound):
			if _, err := st.CreateConfigProfile(imported, "hub import"); err != nil {
				return fmt.Errorf("create profile %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}

	for _, entry := range bundle.Schemas {
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
			if sameSchemaContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			if _, err := st.UpdateDataSchema(imported, existing.Version, "hub import"); err != nil {
				return fmt.Errorf("update schema %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrDataSchemaNotFound):
			if _, err := st.CreateDataSchema(imported, "hub import"); err != nil {
				return fmt.Errorf("create schema %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}

	for _, entry := range bundle.Mappings {
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
			if _, err := st.UpdateMapping(imported, existing.Version, "hub import"); err != nil {
				return fmt.Errorf("update mapping %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrMappingNotFound):
			if _, err := st.CreateMapping(imported, "hub import"); err != nil {
				return fmt.Errorf("create mapping %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}

	for _, entry := range bundle.Scripts {
		imported, err := scriptFromEntry(entry)
		if err != nil {
			return err
		}
		existing, err := st.GetScript(entry.ID)
		switch {
		case err == nil:
			if sameScriptContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			if _, err := st.UpdateScript(imported, existing.Version, "hub import"); err != nil {
				return fmt.Errorf("update script %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrScriptNotFound):
			if _, err := st.CreateScript(imported, "hub import"); err != nil {
				return fmt.Errorf("create script %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}

	for _, entry := range bundle.Connectors {
		if entry.ID == "" || entry.Name == "" || len(entry.Operations) == 0 {
			return fmt.Errorf("connector entry %q is missing id, name or operations", entry.Name)
		}
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
			if _, err := st.UpdateConnector(imported, existing.Version, "hub import"); err != nil {
				return fmt.Errorf("update connector %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrConnectorNotFound):
			imported.Visibility = store.VisibilityShared
			if _, err := st.CreateConnector(imported, "hub import"); err != nil {
				return fmt.Errorf("create connector %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}
	return nil
}
