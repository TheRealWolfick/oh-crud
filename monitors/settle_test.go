package monitors

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestSettlerRunsOnlyTheLastChange(t *testing.T) {
	s := newSettler(80*time.Millisecond, slog.New(slog.DiscardHandler), "test")
	var mu sync.Mutex
	var ran []string
	record := func(v string) func() { return func() { mu.Lock(); ran = append(ran, v); mu.Unlock() } }

	// Three writes to one file inside the window, one to another file
	s.stage("a.yaml", record("a1"))
	time.Sleep(30 * time.Millisecond)
	s.stage("a.yaml", record("a2"))
	s.stage("b.yaml", record("b1"))
	time.Sleep(30 * time.Millisecond)
	s.stage("a.yaml", record("a3")) // restarts a's wait

	time.Sleep(60 * time.Millisecond) // b has settled, a has not
	mu.Lock()
	if len(ran) != 1 || ran[0] != "b1" {
		t.Errorf("expected only b to have run so far, got %v", ran)
	}
	mu.Unlock()

	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || ran[1] != "a3" {
		t.Errorf("expected a's last change to run once, got %v", ran)
	}
}

// A panic in staged work is recovered, and later work still runs
func TestSettlerRecoversPanics(t *testing.T) {
	s := newSettler(10*time.Millisecond, slog.New(slog.DiscardHandler), "test")
	done := make(chan struct{})
	s.stage("bad.yaml", func() { panic("boom") })
	s.stage("good.yaml", func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("staged work after a panic never ran")
	}
}
