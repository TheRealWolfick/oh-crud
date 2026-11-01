package safe

import (
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRecoverCallsOnPanic(t *testing.T) {
	var got error
	func() {
		defer Recover(quiet, "test", func(err error) { got = err })
		panic("boom")
	}()
	if got == nil || !strings.Contains(got.Error(), "boom") {
		t.Errorf("expected onPanic to get the panic, got %v", got)
	}
}

func TestRecoverSurvivesPanicInOnPanic(t *testing.T) {
	func() {
		defer Recover(quiet, "test", func(error) { panic("again") })
		panic("boom")
	}()
}

func TestRecoverWithoutPanicDoesNothing(t *testing.T) {
	called := false
	func() {
		defer Recover(quiet, "test", func(error) { called = true })
	}()
	if called {
		t.Error("expected onPanic not to run without a panic")
	}
}

func TestGoRecovers(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	Go(quiet, "test", func() {
		defer wg.Done()
		panic("boom")
	})
	wg.Wait()
}
