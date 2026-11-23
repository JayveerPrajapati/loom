package store

import "testing"

func TestPercentileNearestRank(t *testing.T) {
	values := []int64{9, 1, 5, 3, 7}
	if got := percentile(values, 0); got != 1 {
		t.Fatalf("p0 = %d, want 1", got)
	}
	if got := percentile(values, 0.5); got != 5 {
		t.Fatalf("p50 = %d, want 5", got)
	}
	if got := percentile(values, 1); got != 9 {
		t.Fatalf("p100 = %d, want 9", got)
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
}
