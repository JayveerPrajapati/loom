package store

import (
	"path/filepath"
	"testing"
)

// Regression for the read-pool DSN: a data directory containing URI
// delimiters must not truncate the read connection's path. Before the
// escape, Open succeeded and writes worked, but every read-pool query hit
// a different, table-less database.
func TestOpenWithReadPoolEscapesURIPathCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom#data?dir")
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "Path test", NodeType: "http.request",
		Config: map[string]any{"method": "GET"},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	got, err := st.GetConfigProfile("profile-1")
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if got.Name != "Path test" {
		t.Fatalf("read pool returned %q, want %q", got.Name, "Path test")
	}
}
