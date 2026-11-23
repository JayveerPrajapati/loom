package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func craftArchive(t *testing.T, path string, entries []tar.Header, contents map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for i := range entries {
		if body, ok := contents[entries[i].Name]; ok {
			entries[i].Size = int64(len(body))
		}
		if err := tw.WriteHeader(&entries[i]); err != nil {
			t.Fatal(err)
		}
		if body, ok := contents[entries[i].Name]; ok {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// A traversal entry anywhere in the archive must be rejected outright.
func TestExtractBackupRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	entries := []tar.Header{
		{Name: "loom.db", Mode: 0o600, Size: 3},
		{Name: "../../evil.sh", Mode: 0o600, Size: 5},
	}
	craftArchive(t, filepath.Join(dir, "evil.tar.gz"), entries,
		map[string]string{"loom.db": "db", "../../evil.sh": "pwned"})
	_, err := extractBackup(filepath.Join(dir, "evil.tar.gz"))
	if err == nil {
		t.Fatal("traversal entry accepted")
	}
	if !strings.Contains(err.Error(), "unexpected archive entry") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "evil.sh")); serr == nil {
		t.Fatal("file escaped the archive")
	}
}

// Archives re-packed by third-party tar tools record directory entries;
// those must be accepted, not treated as tampering.
func TestExtractBackupAcceptsDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	entries := []tar.Header{
		{Name: "blobs/", Typeflag: tar.TypeDir},
		{Name: "blobs/ab/", Typeflag: tar.TypeDir},
		{Name: "blobs/ab/abcd", Mode: 0o600, Size: 3},
		{Name: "keys/", Typeflag: tar.TypeDir},
		{Name: "loom.db", Mode: 0o600, Size: 3},
	}
	craftArchive(t, filepath.Join(dir, "ok.tar.gz"), entries,
		map[string]string{"blobs/ab/abcd": "abc", "loom.db": "db"})
	out, err := extractBackup(filepath.Join(dir, "ok.tar.gz"))
	if err != nil {
		t.Fatalf("directory entries rejected: %v", err)
	}
	defer os.RemoveAll(out)
	for _, name := range []string{"loom.db", filepath.Join("blobs", "ab", "abcd")} {
		if _, serr := os.Stat(filepath.Join(out, name)); serr != nil {
			t.Fatalf("missing %s: %v", name, serr)
		}
	}
}

// An archive without a database is not a backup.
func TestExtractBackupRequiresDatabase(t *testing.T) {
	dir := t.TempDir()
	entries := []tar.Header{{Name: "blobs/ab/abcd", Mode: 0o600, Size: 3}}
	craftArchive(t, filepath.Join(dir, "nodb.tar.gz"), entries,
		map[string]string{"blobs/ab/abcd": "abc"})
	if _, err := extractBackup(filepath.Join(dir, "nodb.tar.gz")); err == nil {
		t.Fatal("archive without loom.db accepted")
	}
}

// A backup must carry everything a code.python workflow needs on restore:
// the built environment under data/envs/, the subprocess package policy, and
// the head-seal marker — losing any of them silently downgrades the restore.
func TestBackupCarriesPythonEnvsPolicyAndSealMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "envs", "deadbeef"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "envs", "deadbeef", "pyvenv.cfg"), []byte("env"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subprocess-policy.json"), []byte(`{"allow":["numpy"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "..", "roundtrip.tar.gz")
	if err := runBackup([]string{"--data", dir, "--out", out}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	extracted, err := extractBackup(out)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer os.RemoveAll(extracted)
	for _, name := range []string{
		"loom.db",
		"head.sealed",
		"subprocess-policy.json",
		filepath.Join("envs", "deadbeef", "pyvenv.cfg"),
	} {
		if _, serr := os.Stat(filepath.Join(extracted, name)); serr != nil {
			t.Fatalf("backup lost %s: %v", name, serr)
		}
	}
}
