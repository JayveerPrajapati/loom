package host

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"loom/internal/flow"
)

// Sim is a deterministic in-memory host. It makes every example runnable
// without network access, which is also how CI and `loom test` run.
type Sim struct {
	now      func() time.Time
	mu       sync.Mutex
	flakyHit map[string]int
}

func NewSim() *Sim {
	return &Sim{now: time.Now, flakyHit: map[string]int{}}
}

func (s *Sim) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	url := strings.ToLower(req.URL)
	body := flow.Item{"ok": true}

	if strings.Contains(url, "/flaky") {
		s.mu.Lock()
		s.flakyHit[url]++
		hits := s.flakyHit[url]
		s.mu.Unlock()
		if hits <= 3 {
			return flow.HTTPResponse{}, fmt.Errorf("simulated upstream failure (attempt %d)", hits)
		}
	}

	switch {
	case strings.Contains(url, "/orders/"):
		body = flow.Item{
			"id":       "A-1042",
			"orderId":  "A-1042",
			"amount":   249.0,
			"status":   "paid",
			"currency": "USD",
		}
	case strings.Contains(url, "/refunds"):
		body = flow.Item{"ok": true, "refundId": "R-1042", "amount": 249.0}
	case strings.Contains(url, "/audit"):
		body = flow.Item{"ok": true, "recorded": true}
	case strings.Contains(url, "/tickets"):
		body = flow.Item{"ok": true, "ticketId": "T-1"}
	case strings.Contains(url, "/headlines"):
		body = flow.Item{"title": "Loom ships typed workflows", "count": 3.0}
	case strings.Contains(url, "/contacts"):
		body = flow.Item{"ok": true, "contactId": "C-1", "email": "ada@example.com"}
	case strings.Contains(url, "/posts/") && strings.Contains(url, "/publish"):
		body = flow.Item{"ok": true, "published": true}
	case strings.Contains(url, "/posts"):
		body = flow.Item{"id": "P-1", "title": "Hello from Loom"}
	}

	return flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    body,
	}, nil
}

func (s *Sim) Query(_ context.Context, _, _ string) ([]flow.Item, error) {
	return []flow.Item{
		{"id": "ORD-1", "total": 120.0, "created_at": "2026-10-22T00:00:00Z"},
		{"id": "ORD-2", "total": 129.0, "created_at": "2026-10-22T01:00:00Z"},
		{"id": "ORD-3", "total": 75.0, "created_at": "2026-10-22T02:00:00Z"},
	}, nil
}

func (s *Sim) Slack(context.Context, string, string, string) error { return nil }

func (s *Sim) Sleep(ctx context.Context, _ time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (s *Sim) Now() time.Time { return s.now() }
