package nodes

import (
	"context"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

func TestRemovedNodeCatalogEntries(t *testing.T) {
	registry := Registry()
	for _, typ := range []string{"chat.slack", "data.csv.parse"} {
		if _, ok := Get(typ); ok {
			t.Errorf("catalog still declares %q", typ)
		}
		if _, ok := registry[typ]; ok {
			t.Errorf("registry still implements %q", typ)
		}
	}
}

func TestEmitNodeRendersPayload(t *testing.T) {
	h := &recordingHost{}
	in := emitInput(h, 0)
	in.Config = map[string]any{
		"event":   "ping",
		"payload": map[string]any{"greeting": "hi {{ $json.name }}"},
	}
	in.Item = flow.Item{"name": "loom"}

	out, err := emitNode{}.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out["main"]["greeting"]; got != "hi loom" {
		t.Fatalf("rendered output = %#v, want greeting=hi loom", got)
	}
	if len(h.events) != 1 {
		t.Fatalf("published %d events, want 1", len(h.events))
	}
	if got := h.events[0].Item["greeting"]; got != "hi loom" {
		t.Fatalf("published payload = %#v, want greeting=hi loom", got)
	}
}

func TestEmitNodeDefaultsPayloadToIncomingItem(t *testing.T) {
	h := &recordingHost{}
	in := emitInput(h, 0)
	in.Config = map[string]any{"event": "ping"}
	in.Item = flow.Item{"hello": "world"}

	out, err := emitNode{}.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out["main"]["hello"]; got != "world" {
		t.Fatalf("main output = %#v, want the incoming item", got)
	}
	if len(h.events) != 1 {
		t.Fatalf("published %d events, want 1", len(h.events))
	}
	if got := h.events[0].Item["hello"]; got != "world" {
		t.Fatalf("published payload = %#v, want the incoming item", got)
	}
}

func TestCodeNodeTimeout(t *testing.T) {
	started := time.Now()
	_, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"source":         "while (true) {}",
			"timeoutSeconds": float64(1),
		},
	})
	if err == nil {
		t.Fatal("runaway script was accepted")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want timeout error", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s, want a bounded run", elapsed)
	}
}

func TestPostgresOperationMismatch(t *testing.T) {
	host := &fakeHost{}
	cases := []struct {
		operation string
		query     string
	}{
		{"query", "insert into t values (1)"},
		{"insert", "select 1"},
		{"update", "delete from t"},
		{"bogus", "drop table t"},
	}
	for _, tc := range cases {
		_, err := postgresNode{}.Execute(context.Background(), flow.Input{
			Config:      map[string]any{"operation": tc.operation, "query": tc.query},
			Host:        host,
			Credentials: map[string]string{"postgres": "cred_pg"},
		})
		if err == nil || !strings.Contains(err.Error(), "db.postgres") {
			t.Errorf("operation %q with query %q: err = %v, want db.postgres mismatch", tc.operation, tc.query, err)
		}
	}

	for _, query := range []string{
		"  WITH x AS (SELECT 1) SELECT * FROM x",
		"-- report\nSELECT 1",
		"/* report */ SELECT 1",
		"(SELECT 1)",
	} {
		if _, err := (postgresNode{}).Execute(context.Background(), flow.Input{
			Config:      map[string]any{"operation": "query", "query": query},
			Host:        host,
			Credentials: map[string]string{"postgres": "cred_pg"},
		}); err != nil {
			t.Errorf("query %q rejected: %v", query, err)
		}
	}
}

func TestPostgresSingleStatementGuard(t *testing.T) {
	host := &fakeHost{}
	cases := []struct {
		name      string
		operation string
		query     string
		wantErr   string
	}{
		{"live bypass", "query", "SELECT 1; DROP TABLE IF EXISTS audit;", "multiple SQL statements"},
		{"second statement no trailer", "query", "SELECT 1; DROP TABLE IF EXISTS audit", "multiple SQL statements"},
		{"trailing semicolon", "query", "SELECT 1;", ""},
		{"semicolon in string", "query", "SELECT ';'", ""},
		{"doubled quote in string", "query", "SELECT 'it''s; fine'", ""},
		{"semicolon in dollar quote", "query", "SELECT $$;$$", ""},
		{"tagged dollar quote", "query", "SELECT $tag$;$tag$", ""},
		{"line comment prefix", "query", "-- c\nSELECT 1", ""},
		{"block comment prefix", "query", "/* c */ SELECT 1", ""},
		{"nested block comment prefix", "query", "/* a /* b */ c */ SELECT 1", ""},
		{"trailing line comment", "query", "SELECT 1 -- x", ""},
		{"trailing block comment", "query", "SELECT 1 /* x */", ""},
		{"double quoted semicolon", "query", `SELECT "a;b"`, ""},
		{"cte select", "query", "WITH x AS (SELECT 1) SELECT * FROM x", ""},
		{"cte delete", "query", "WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x", "must be read-only"},
		{"two trailing semicolons", "query", "SELECT 1;;", "multiple SQL statements"},
		{"insert multi statement", "insert", "INSERT INTO t VALUES (1); DROP TABLE t;", "multiple SQL statements"},
		{"escaped quote hides semicolon", "query", `SELECT E'\';'; DROP TABLE t`, "multiple SQL statements"},
		{"plain backslash does not hide quote", "query", `SELECT 'a\'; DROP TABLE x; --'`, "multiple SQL statements"},
		{"empty operation drop", "", "DROP TABLE t", "db.postgres"},
		{"bogus operation is read-only", "bogus", "DROP TABLE t", "db.postgres"},
		{"empty operation select", "", "SELECT 1", ""},
		{"bogus operation select", "bogus", "SELECT 1", ""},
		{"empty operation with", "", "WITH x AS (SELECT 1) SELECT * FROM x", ""},
		{"select into", "query", "SELECT * INTO newtbl FROM t", "must be read-only"},
		{"select into in cte", "query", "WITH x AS (SELECT 1) SELECT * INTO newtbl FROM x", "must be read-only"},
		{"merge", "query", "MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE", "db.postgres"},
		{"set mutation", "query", "SET search_path = evil", "db.postgres"},
		{"insert accepted", "insert", "INSERT INTO t VALUES (1)", ""},
		{"update accepted", "update", "UPDATE t SET a = 1", ""},
		{"with insert accepted", "insert", "WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (postgresNode{}).Execute(context.Background(), flow.Input{
				Config:      map[string]any{"operation": tc.operation, "query": tc.query},
				Host:        host,
				Credentials: map[string]string{"postgres": "cred_pg"},
			})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("query %q rejected: %v", tc.query, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("query %q was accepted, want error containing %q", tc.query, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
