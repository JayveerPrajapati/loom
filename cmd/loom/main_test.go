package main

import (
	"testing"
	"time"
)

func TestParseCutoff(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		in   string
		want time.Time
	}{
		{"30d", now.AddDate(0, 0, -30)},
		{"24h", now.Add(-24 * time.Hour)},
		{"2026-10-01T00:00:00Z", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	} {
		got, err := parseCutoff(test.in, now)
		if err != nil {
			t.Fatalf("parse %q: %v", test.in, err)
		}
		if !got.Equal(test.want) {
			t.Fatalf("parse %q = %s, want %s", test.in, got, test.want)
		}
	}
	for _, invalid := range []string{"", "-1d", "soon"} {
		if _, err := parseCutoff(invalid, now); err == nil {
			t.Fatalf("parse %q: expected error", invalid)
		}
	}
}
