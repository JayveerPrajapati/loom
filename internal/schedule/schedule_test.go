package schedule

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func TestNextCronUsesTimezone(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next, ok, err := Next("cron", map[string]any{
		"schedule": "0 9 * * *",
		"timezone": "America/New_York",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("cron trigger should have a next run")
	}
	want := time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestNextOnceAndManual(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next, ok, err := Next("once", map[string]any{"runAt": "2026-02-03T04:05:06Z"}, now)
	if err != nil || !ok {
		t.Fatalf("once next = %s, %v, %v", next, ok, err)
	}
	if next.Format(time.RFC3339) != "2026-02-03T04:05:06Z" {
		t.Fatalf("once next = %s", next)
	}
	if _, ok, err := Next("manual", nil, now); err != nil || ok {
		t.Fatalf("manual next ok=%v err=%v", ok, err)
	}
}

func TestExpressionParsesWithCronScheduler(t *testing.T) {
	engine := cron.New()
	if _, err := engine.AddFunc(Expression(map[string]any{
		"schedule": "0 9 * * *",
		"timezone": "America/New_York",
	}), func() {}); err != nil {
		t.Fatalf("scheduler parser rejected expression: %v", err)
	}
}

func TestDescriptorUsesTimezonePrefix(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next, ok, err := Next("cron", map[string]any{
		"schedule": "@daily",
		"timezone": "America/New_York",
	}, now)
	if err != nil || !ok {
		t.Fatalf("descriptor next = %s, %v, %v", next, ok, err)
	}
	want := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("descriptor next = %s, want %s", next, want)
	}
	if expr := Expression(map[string]any{"schedule": "@daily", "timezone": "UTC"}); expr != "CRON_TZ=UTC @daily" {
		t.Fatalf("expression = %q", expr)
	}
}

func TestValidateRejectsBadSchedule(t *testing.T) {
	if err := Validate("cron", map[string]any{"schedule": "not a cron"}); err == nil {
		t.Fatal("expected invalid schedule error")
	}
	if err := Validate("cron", map[string]any{"schedule": "0 9 * * *", "timezone": "Mars/Olympus"}); err == nil {
		t.Fatal("expected invalid timezone error")
	}
}
