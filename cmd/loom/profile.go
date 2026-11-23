package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"loom/internal/graph"
	"loom/internal/store"
)

// runProfile implements `loom profile list|export|import` — the
// Configuration Hub's portability surface (plan §18 D8). Profiles live in
// loom.db and are already covered by `loom backup`; these commands exist
// for cross-instance moves and review.
func runProfile(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom profile <list|export|import> [options]")
	}
	switch args[0] {
	case "list":
		return runProfileList(args[1:])
	case "export":
		return runProfileExport(args[1:])
	case "import":
		return runProfileImport(args[1:])
	default:
		return fmt.Errorf("unknown profile subcommand %q (want list, export or import)", args[0])
	}
}

func runProfileList(args []string) error {
	fs := flag.NewFlagSet("profile list", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	profiles, err := st.ListConfigProfiles()
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		fmt.Println("no config profiles")
		return nil
	}
	for _, p := range profiles {
		owner := ""
		if p.Owner != "" {
			owner = " owner=" + p.Owner
		}
		usage := ""
		if p.UsageCount > 0 {
			usage = fmt.Sprintf(" used-by=%d", p.UsageCount)
		}
		fmt.Printf("%s\t%s\tv%d%s%s\n", p.ID, p.Name, p.Version, owner, usage)
	}
	return nil
}

// profileExportEntry is one profile in an export file. Only the current
// version travels: import on another instance creates it at v1 (or bumps an
// existing one), and version history is instance-local by design.
type profileExportEntry struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	NodeType       string               `json:"nodeType"`
	Description    string               `json:"description,omitempty"`
	Config         map[string]any       `json:"config"`
	CredentialRefs map[string]string    `json:"credentialRefs,omitempty"`
	Retry          *graph.RetryPolicy   `json:"retry,omitempty"`
	Tags           []string             `json:"tags,omitempty"`
	Owner          string               `json:"owner,omitempty"`
	Bundle         *store.ProfileBundle `json:"bundle,omitempty"`
}

func runProfileExport(args []string) error {
	fs := flag.NewFlagSet("profile export", flag.ContinueOnError)
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

	profiles, err := st.ListConfigProfiles()
	if err != nil {
		return err
	}
	// Optional id/name filters; no filters means every profile.
	wanted := map[string]bool{}
	for _, arg := range fs.Args() {
		wanted[arg] = true
	}
	entries := []profileExportEntry{}
	for _, p := range profiles {
		if len(wanted) > 0 && !wanted[p.ID] && !wanted[p.Name] {
			continue
		}
		entries = append(entries, profileExportEntry{
			ID: p.ID, Name: p.Name, NodeType: p.NodeType, Description: p.Description,
			Config: p.Config, CredentialRefs: p.CredentialRefs, Retry: p.Retry,
			Tags: p.Tags, Owner: p.Owner, Bundle: p.Bundle,
		})
	}
	if len(entries) == 0 {
		return errors.New("no config profiles matched")
	}
	raw, err := json.MarshalIndent(map[string]any{"kind": "loom-config-profiles", "profiles": entries}, "", "  ")
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

func runProfileImport(args []string) error {
	fs := flag.NewFlagSet("profile import", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom profile import [options] <file>")
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var file struct {
		Kind     string               `json:"kind"`
		Profiles []profileExportEntry `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse %s: %w", fs.Arg(0), err)
	}
	if file.Kind != "loom-config-profiles" {
		return errors.New("not a loom profile export (missing kind: loom-config-profiles)")
	}
	if len(file.Profiles) == 0 {
		return errors.New("no profiles in file")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, entry := range file.Profiles {
		if entry.ID == "" || entry.Name == "" || entry.NodeType == "" {
			return fmt.Errorf("profile entry %+v is missing id, name or nodeType", entry.Name)
		}
		imported := &store.ConfigProfile{
			ID: entry.ID, Name: entry.Name, NodeType: entry.NodeType, Description: entry.Description,
			Config: entry.Config, CredentialRefs: entry.CredentialRefs, Retry: entry.Retry,
			Tags: entry.Tags, Owner: entry.Owner, Bundle: entry.Bundle,
		}
		existing, err := st.GetConfigProfile(entry.ID)
		switch {
		case err == nil:
			// The profile exists: import as a new version when the
			// content differs, otherwise leave the instance untouched.
			if sameProfileContent(existing, imported) {
				fmt.Printf("%s: unchanged, skipped\n", entry.ID)
				continue
			}
			imported.Version = existing.Version
			if _, err := st.UpdateConfigProfile(imported, existing.Version, "profile import"); err != nil {
				return fmt.Errorf("update %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: updated to v%d\n", entry.ID, existing.Version+1)
		case errors.Is(err, store.ErrConfigProfileNotFound):
			if _, err := st.CreateConfigProfile(imported, "profile import"); err != nil {
				return fmt.Errorf("create %s: %w", entry.ID, err)
			}
			fmt.Printf("%s: created at v1\n", entry.ID)
		default:
			return err
		}
	}
	return nil
}

// sameProfileContent reports whether the two profiles carry identical
// content (everything a version row would store). Bundle references are
// content (Phase 1 P0-1): a changed bundle bumps the version.
func sameProfileContent(a, b *store.ConfigProfile) bool {
	if a.Name != b.Name || a.Description != b.Description || a.Owner != b.Owner ||
		!jsonEqual(a.Config, b.Config) || !credRefsEqual(a.CredentialRefs, b.CredentialRefs) ||
		!tagsEqual(a.Tags, b.Tags) {
		return false
	}
	ra, _ := json.Marshal(a.Retry)
	rb, _ := json.Marshal(b.Retry)
	if string(ra) != string(rb) {
		return false
	}
	ba, _ := json.Marshal(a.Bundle)
	bb, _ := json.Marshal(b.Bundle)
	return string(ba) == string(bb)
}

func jsonEqual(a, b map[string]any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return string(ra) == string(rb)
}

func credRefsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func tagsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
