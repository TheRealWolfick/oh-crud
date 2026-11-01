package monitors

import (
	"log/slog"
	"sync"
	"time"

	"lotusforge.au/api-server/safe"
)

// settleFor is how long a changed file must stay unchanged before it is read. Editors
// often save in several writes, and a model is checked, synced and logged only once
// its file has settled, so a single save gives a single reload of the final contents.
const settleFor = 10 * time.Second

// settler stages work per file: each new change restarts that file's wait and replaces
// what will run, so only the last change within the window is acted on
type settler struct {
	wait   time.Duration
	lg     *slog.Logger
	name   string
	mu     sync.Mutex
	timers map[string]*time.Timer
}

func newSettler(wait time.Duration, lg *slog.Logger, name string) *settler {
	return &settler{wait: wait, lg: lg, name: name, timers: map[string]*time.Timer{}}
}

// stage runs fn for key once key has had no new stage call for the wait
func (s *settler) stage(key string, fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.timers[key]; ok { t.Stop() }
	var t *time.Timer
	t = time.AfterFunc(s.wait, func() {
		s.mu.Lock()
		if s.timers[key] != t { s.mu.Unlock(); return } // replaced while firing
		delete(s.timers, key)
		s.mu.Unlock()
		defer safe.Recover(s.lg, s.name, nil)
		fn()
	})
	s.timers[key] = t
}
