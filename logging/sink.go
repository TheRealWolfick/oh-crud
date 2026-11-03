package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"


	"lotusforge.au/api-server/globals"
)

// Copier is the part of a pgx pool the sink uses
type Copier interface {
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, rows pgx.CopyFromSource) (int64, error)
}

var logColumns = []string{"at", "level", "severity", "msg", "request_id", "service", "attrs"}

// Sink queues rows and writes them to ohc_logs in batches. Logging never waits for
// the database: when the queue is full a row is dropped and counted, and the count is
// written as a row of its own once there is room. Every row was already written to
// stderr, so a database outage loses nothing that isn't there.
type Sink struct {
	db      Copier
	queue   chan Row
	dropped atomic.Int64
	stderr  func(format string, args ...any) // the sink's own problems, never logged through itself
	done    chan struct{}
	stop    sync.Once
	cancel  context.CancelFunc
}

func newSink(db Copier) *Sink {
	return &Sink{
		db:     db,
		queue:  make(chan Row, globals.LogQueueSize),
		done:   make(chan struct{}),
		stderr: func(f string, a ...any) { fmt.Fprintf(os.Stderr, "logging: "+f+"\n", a...) },
	}
}

func (s *Sink) enqueue(r Row) {
	select {
	case s.queue <- r:
	default:
		s.dropped.Add(1)
	}
}

// run writes batches until ctx ends, then flushes what is left
func (s *Sink) run(ctx context.Context) {
	defer close(s.done)
	batch := make([]Row, 0, globals.LogBatchSize)
	tick := time.NewTicker(globals.LogFlushEvery)
	defer tick.Stop()

	flush := func() {
		if n := s.dropped.Swap(0); n > 0 {
			batch = append(batch, Row{At: time.Now(), Level: slog.LevelWarn, Service: "ohc",
				Msg: "log rows dropped: the database writer fell behind", Attrs: map[string]any{"dropped": n}})
		}
		if len(batch) == 0 { return }
		s.write(batch)
		batch = batch[:0]
	}

	for {
		select {
		case r := <-s.queue:
			batch = append(batch, r)
			if len(batch) >= globals.LogBatchSize { flush() }
		case <-tick.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case r := <-s.queue:
					batch = append(batch, r)
					if len(batch) >= globals.LogBatchSize { flush() }
				default:
					flush()
					return
				}
			}
		}
	}
}

func (s *Sink) write(batch []Row) {
	ctx, cancel := context.WithTimeout(context.Background(), globals.LogWriteTimeout)
	defer cancel()
	_, err := s.db.CopyFrom(ctx, pgx.Identifier{globals.LogTable}, logColumns, pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
		r := batch[i]
		var reqID any
		if r.RequestID != "" { reqID = r.RequestID }
		attrs := r.Attrs
		if attrs == nil { attrs = map[string]any{} }
		return []any{r.At, r.Level.String(), int16(r.Level), r.Msg, reqID, r.Service, attrs}, nil
	}))
	if err != nil {
		s.stderr("could not write %d log rows to %s (they are on stderr): %v", len(batch), globals.LogTable, err)
	}
}

// Close stops the sink after writing everything queued
func (s *Sink) Close() {
	s.stop.Do(func() {
		if s.cancel != nil { s.cancel() }
		<-s.done
	})
}
