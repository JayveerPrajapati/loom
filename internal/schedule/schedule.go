package schedule

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// Next returns the next scheduled time for a job trigger. The bool is false
// for manual triggers, which have no next run.
func Next(triggerType string, config map[string]any, now time.Time) (time.Time, bool, error) {
	switch triggerType {
	case "", "manual":
		return time.Time{}, false, nil
	case "cron":
		expr, _ := config["schedule"].(string)
		expr = strings.TrimSpace(expr)
		if expr == "" {
			return time.Time{}, true, fmt.Errorf("cron schedule is required")
		}
		if zone, _ := config["timezone"].(string); strings.TrimSpace(zone) != "" {
			if _, err := time.LoadLocation(strings.TrimSpace(zone)); err != nil {
				return time.Time{}, true, fmt.Errorf("invalid timezone %q: %w", zone, err)
			}
			expr = "CRON_TZ=" + strings.TrimSpace(zone) + " " + expr
		}
		entry, err := parser.Parse(expr)
		if err != nil {
			return time.Time{}, true, fmt.Errorf("invalid cron schedule %q: %w", expr, err)
		}
		return entry.Next(now), true, nil
	case "once":
		raw, _ := config["runAt"].(string)
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
		if err != nil {
			return time.Time{}, true, fmt.Errorf("invalid one-time runAt %q: %w", raw, err)
		}
		return at, true, nil
	default:
		return time.Time{}, false, fmt.Errorf("unsupported trigger type %q", triggerType)
	}
}

// Validate checks a job trigger and its configuration.
func Validate(triggerType string, config map[string]any) error {
	switch triggerType {
	case "", "manual":
		return nil
	case "cron", "once":
		_, _, err := Next(triggerType, config, time.Now())
		return err
	default:
		return fmt.Errorf("unsupported trigger type %q", triggerType)
	}
}

// Expression returns the cron expression used by the scheduler, including an
// optional CRON_TZ prefix.
func Expression(config map[string]any) string {
	expr, _ := config["schedule"].(string)
	expr = strings.TrimSpace(expr)
	if zone, _ := config["timezone"].(string); strings.TrimSpace(zone) != "" {
		return "CRON_TZ=" + strings.TrimSpace(zone) + " " + expr
	}
	return expr
}
