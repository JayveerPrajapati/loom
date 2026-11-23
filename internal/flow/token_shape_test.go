package flow_test

import (
	"encoding/hex"
	"testing"

	"loom/internal/flow"
)

func TestNewTokenShape(t *testing.T) {
	token := flow.NewToken()
	if len(token) != 32 {
		t.Fatalf("token length = %d, want 32 hex chars", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
	id := flow.NewID("run")
	if len(id) != len("run-")+24 {
		t.Fatalf("id = %q, want a run- prefix plus 24 hex chars", id)
	}
}
