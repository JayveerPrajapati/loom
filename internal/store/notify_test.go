package store

import (
	"sync"
	"testing"
	"time"
)

func waitForSignal(t *testing.T, ch <-chan struct{}, within time.Duration) bool {
	t.Helper()
	select {
	case <-ch:
		return true
	case <-time.After(within):
		return false
	}
}

// Event appends and status flips must wake a subscribed stream.
func TestRunChangeNotifierDeliversMutations(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-n", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	ch, stop := s.SubscribeRunChanges("run-n")
	defer stop()

	if _, err := s.AppendEvent("run-n", Event{Type: "node.started", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !waitForSignal(t, ch, time.Second) {
		t.Fatal("event append did not notify")
	}

	fin := time.Now()
	if err := s.UpdateRunStatus("run-n", "done", "", &fin); err != nil {
		t.Fatal(err)
	}
	if !waitForSignal(t, ch, time.Second) {
		t.Fatal("status update did not notify")
	}

	if _, err := s.ResumeWaiting("run-n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Fatal("resume of a non-waiting run should not flip a row, so no wake is required — got one anyway")
	case <-time.After(100 * time.Millisecond):
	}
}

// A burst of mutations may coalesce into one pending signal; the woken
// stream re-lists after its last delivered seq, so no event is lost.
func TestRunChangeNotifierCoalescesWithoutLoss(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-b", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.AppendEvent("run-b", Event{Type: "node.started", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	ch, stop := s.SubscribeRunChanges("run-b")
	defer stop()
	if _, err := s.AppendEvent("run-b", Event{Type: "node.finished", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !waitForSignal(t, ch, time.Second) {
		t.Fatal("append did not notify")
	}
	events, err := s.ListEventsAfter("run-b", 0)
	if err != nil || len(events) != 6 {
		t.Fatalf("events after coalesced wake = %d (err=%v), want all 6", len(events), err)
	}
}

// Unsubscribe must remove the channel so neither the map nor a blocked
// sender leaks, and later notifications reach nobody.
func TestRunChangeNotifierUnsubscribeCleansUp(t *testing.T) {
	n := NewRunChangeNotifier()
	ch, stop := n.Subscribe("run-x")
	stop()

	n.Notify("run-x")
	select {
	case <-ch:
		t.Fatal("unsubscribed channel received a signal")
	case <-time.After(50 * time.Millisecond):
	}
	n.mu.Lock()
	remaining := len(n.subs)
	n.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("subscriber map still holds %d entries after unsubscribe", remaining)
	}

	// Double cancel must be safe.
	stop()

	// Other runs' subscriptions are untouched.
	ch2, stop2 := n.Subscribe("run-y")
	defer stop2()
	n.Notify("run-y")
	if !waitForSignal(t, ch2, time.Second) {
		t.Fatal("independent subscription missed its signal")
	}
}

// Many concurrent subscribers all receive one notification.
func TestRunChangeNotifierFansOut(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-f", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	const subs = 64
	var stops []func()
	var chans []<-chan struct{}
	for i := 0; i < subs; i++ {
		ch, stop := s.SubscribeRunChanges("run-f")
		stops = append(stops, stop)
		chans = append(chans, ch)
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()

	if _, err := s.AppendEvent("run-f", Event{Type: "node.started", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for i, ch := range chans {
		if !waitForSignal(t, ch, 2*time.Second) {
			t.Fatalf("subscriber %d missed the notification", i)
		}
	}
}

// Subscribers on different runs do not hear each other's signals.
func TestRunChangeNotifierIsolatesRuns(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	for _, id := range []string{"run-i1", "run-i2"} {
		if err := s.CreateRun(Run{
			ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "running", Trigger: "manual", StartedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ch1, stop1 := s.SubscribeRunChanges("run-i1")
	defer stop1()
	ch2, stop2 := s.SubscribeRunChanges("run-i2")
	defer stop2()

	if _, err := s.AppendEvent("run-i2", Event{Type: "node.started", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !waitForSignal(t, ch2, time.Second) {
		t.Fatal("target run missed its notification")
	}
	select {
	case <-ch1:
		t.Fatal("unrelated run received a notification")
	case <-time.After(100 * time.Millisecond):
	}
}

// Notify is safe to hammer from many goroutines while subscribers come and
// go; it must never block or race.
func TestRunChangeNotifierConcurrentNotifyAndSubscribe(t *testing.T) {
	n := NewRunChangeNotifier()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				n.Notify("run-racy")
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_, stop := n.Subscribe("run-racy")
				stop()
			}
		}()
	}
	wg.Wait()
}
