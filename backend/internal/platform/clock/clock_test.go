package clock

import (
	"testing"
	"time"
)

func TestFakeAdvance(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f := NewFake(start)
	if !f.Now().Equal(start) {
		t.Fatalf("unexpected start: %v", f.Now())
	}
	f.Advance(90 * time.Second)
	if got := f.Now().Sub(start); got != 90*time.Second {
		t.Fatalf("advance mismatch: %v", got)
	}
	f.Set(start)
	if !f.Now().Equal(start) {
		t.Fatal("set did not reset")
	}
	var _ Clock = System{}
	if (System{}).Now().IsZero() {
		t.Fatal("system clock returned zero")
	}
}
