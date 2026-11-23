package host

import (
	"testing"
)

func FuzzValidateSingleSQLStatement(f *testing.F) {
	seeds := []string{
		"",
		"   ",
		"select 1",
		"select 1;",
		"select 1; select 2",
		"select 1 -- select 2\n; select 3",
		"/* select 1 */ select 2; /* select 3 */ select 4",
		"/* outer /* nested */ still comment */ select 1",
		"select $$; select 2$$",
		"select $tag$; select 2$tag$",
		"select E'\\'; select 2'",
		"select '\\'; select 2'",
		"select 'It''s fine'; select 2",
		"select $tag$unterminated",
		"select 'unterminated",
		"select 1;\n\n-- trailing comment",
		"WITH x AS (INSERT INTO t VALUES (1) RETURNING *) SELECT * FROM x",
		"MERGE INTO t USING s ON true WHEN MATCHED THEN UPDATE SET x=1",
		"SELECT * INTO new_table FROM old_table",
		"select '\x00'; select 2",
		"select 'مرحبا'; select 2",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		// The immediate invariant is parser robustness: no malformed literal,
		// comment, dollar quote, Unicode, NUL byte, or oversized input may panic.
		_ = validateSingleSQLStatement(query)
		_ = scanSQLGuardSemicolons(query)
	})
}
