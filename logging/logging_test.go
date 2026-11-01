package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func day(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }

func TestPartitionPlan(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 30, 0, 0, time.UTC)

	// A fresh table: yesterday, today and three days ahead
	create, drop := partitionPlan(now, nil, 14)
	want := []time.Time{day("2026-10-31"), day("2026-11-01"), day("2026-11-02"), day("2026-11-03"), day("2026-11-04")}
	if !reflect.DeepEqual(create, want) || len(drop) != 0 {
		t.Errorf("fresh: create %v drop %v", create, drop)
	}

	// 14 days kept means today and the 13 before it: 19 Oct is the oldest kept
	existing := []string{"ohc_logs_p20261018", "ohc_logs_p20261019", "ohc_logs_p20261031", "ohc_logs_p20261101",
		"ohc_logs_p20261102", "ohc_logs_p20261103", "ohc_logs_p20261104", "ohc_logs_default", "unrelated"}
	create, drop = partitionPlan(now, existing, 14)
	if len(create) != 0 {
		t.Errorf("expected nothing to create, got %v", create)
	}
	if !reflect.DeepEqual(drop, []string{"ohc_logs_p20261018"}) {
		t.Errorf("expected only 18 Oct dropped, got %v", drop)
	}

	// Keeping one day means not making yesterday's partition only to drop it
	create, _ = partitionPlan(now, nil, 1)
	if create[0] != day("2026-11-01") {
		t.Errorf("expected today first with retention 1, got %v", create)
	}

	// The day follows UTC, whatever the server's zone
	sydney := time.FixedZone("AEDT", 11*3600)
	create, _ = partitionPlan(time.Date(2026, 11, 2, 9, 0, 0, 0, sydney), nil, 14) // 1 Nov 22:00 UTC
	if create[1] != day("2026-11-01") {
		t.Errorf("expected today to be 1 Nov UTC, got %v", create)
	}
}

// testLogger builds a tee onto a buffer and a sink that tests drain by hand
func testLogger(stderrLevel slog.Level, dbLevel slog.Level) (*slog.Logger, *bytes.Buffer, *Sink) {
	var buf bytes.Buffer
	s := &slot{}
	sink := newSink(nil)
	s.sink.Store(sink)
	s.level.Store(int64(dbLevel))
	h := &teeHandler{primary: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: stderrLevel}), db: newDBHandler(s)}
	return slog.New(h), &buf, sink
}

func drain(s *Sink) []Row {
	var rows []Row
	for {
		select {
		case r := <-s.queue:
			rows = append(rows, r)
		default:
			return rows
		}
	}
}

func TestHandlerBuildsRows(t *testing.T) {
	lg, _, sink := testLogger(slog.LevelInfo, slog.LevelInfo)

	lg.With("component", "auth").WithGroup("req").With("ip", "192.0.2.1").
		Info("login", "request_id", "r-1", "user", "usr_1", slog.Group("tok", "kid", "k1"), "took", 1500*time.Millisecond, "err", errors.New("boom"))

	rows := drain(sink)
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	r := rows[0]
	if r.Msg != "login" || r.Level != slog.LevelInfo || r.Service != "ohc" || r.At.IsZero() {
		t.Errorf("unexpected row %+v", r)
	}
	want := map[string]any{
		"component": "auth",
		"req": map[string]any{
			"ip": "192.0.2.1", "request_id": "r-1", "user": "usr_1",
			"tok": map[string]any{"kid": "k1"}, "took": "1.5s", "err": "boom",
		},
	}
	if !reflect.DeepEqual(r.Attrs, want) {
		t.Errorf("attrs\n got  %#v\n want %#v", r.Attrs, want)
	}
	// Only a top-level request_id becomes the column; this one was inside a group
	if r.RequestID != "" {
		t.Errorf("expected no request_id column for a grouped one, got %q", r.RequestID)
	}

	lg2, _, sink2 := testLogger(slog.LevelInfo, slog.LevelInfo)
	lg2.Info("hello", "request_id", "r-2")
	if r := drain(sink2)[0]; r.RequestID != "r-2" || r.Attrs["request_id"] != nil {
		t.Errorf("expected request_id moved to its column, got %+v", r)
	}
}

// stderr and the database each keep their own level
func TestTeeLevels(t *testing.T) {
	lg, buf, sink := testLogger(slog.LevelDebug, slog.LevelWarn)
	lg.Debug("noisy")
	lg.Info("normal")
	lg.Warn("careful")

	out := buf.String()
	for _, m := range []string{"noisy", "normal", "careful"} {
		if !strings.Contains(out, m) {
			t.Errorf("expected %q on stderr", m)
		}
	}
	rows := drain(sink)
	if len(rows) != 1 || rows[0].Msg != "careful" {
		t.Errorf("expected only the warning in the database, got %+v", rows)
	}
}

// Before AttachDB, nothing is queued and stderr works as before
func TestNoSinkYet(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(&teeHandler{primary: slog.NewTextHandler(&buf, nil), db: newDBHandler(&slot{})})
	lg.Error("early")
	if !strings.Contains(buf.String(), "early") {
		t.Errorf("expected stderr output before the database is attached")
	}
}

func TestValue(t *testing.T) {
	cases := map[string]struct {
		in   slog.Value
		want any
	}{
		"error":         {slog.AnyValue(errors.New("x")), "x"},
		"errors":        {slog.AnyValue([]error{errors.New("a"), errors.New("b")}), nil},
		"stringer":      {slog.AnyValue(fmtStringer{}), "S"},
		"time":          {slog.TimeValue(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), "2026-01-02T03:04:05Z"},
		"unmarshalable": {slog.AnyValue(make(chan int)), nil},
	}
	for name, tc := range cases {
		got := value(tc.in)
		if name == "errors" {
			if !reflect.DeepEqual(got, []string{"a", "b"}) {
				t.Errorf("errors: got %#v", got)
			}
			continue
		}
		if name == "unmarshalable" {
			if s, ok := got.(string); !ok || s == "" {
				t.Errorf("%s: expected a string fallback, got %#v", name, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %#v, want %#v", name, got, tc.want)
		}
	}
}

type fmtStringer struct{}

func (fmtStringer) String() string { return "S" }

// fakeCopier records what the sink writes
type fakeCopier struct {
	rows [][]any
	err  error
}

func (f *fakeCopier) CopyFrom(_ context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error) {
	if f.err != nil { return 0, f.err }
	for src.Next() {
		v, _ := src.Values()
		f.rows = append(f.rows, v)
	}
	return int64(len(f.rows)), nil
}

func TestSinkWritesAndCountsDrops(t *testing.T) {
	fc := &fakeCopier{}
	s := newSink(fc)
	s.queue = make(chan Row, 2) // tiny queue to force drops
	s.enqueue(Row{Msg: "a", Level: slog.LevelInfo, Service: "ohc", At: time.Now()})
	s.enqueue(Row{Msg: "b", Level: slog.LevelError, Service: "ohc", At: time.Now(), RequestID: "r-1"})
	s.enqueue(Row{Msg: "c"}) // dropped
	s.enqueue(Row{Msg: "d"}) // dropped

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.run(ctx)
	s.Close() // flushes everything queued

	if len(fc.rows) != 3 {
		t.Fatalf("expected two rows and a drop notice, got %v", fc.rows)
	}
	if fc.rows[1][1] != "ERROR" || fc.rows[1][2] != int16(8) || fc.rows[1][4] != "r-1" {
		t.Errorf("unexpected row values %v", fc.rows[1])
	}
	if fc.rows[0][4] != nil {
		t.Errorf("expected an empty request id to be NULL, got %#v", fc.rows[0][4])
	}
	notice := fc.rows[2]
	if notice[1] != "WARN" || !strings.Contains(notice[3].(string), "dropped") || notice[6].(map[string]any)["dropped"] != int64(2) {
		t.Errorf("expected a notice of 2 dropped rows, got %v", notice)
	}
}

// A database error is reported on stderr directly, never through the logger
func TestSinkReportsWriteErrorsOnStderr(t *testing.T) {
	s := newSink(&fakeCopier{err: errors.New("db down")})
	var got string
	s.stderr = func(f string, a ...any) { got = f }
	s.write([]Row{{Msg: "x"}})
	if !strings.Contains(got, "could not write") {
		t.Errorf("expected a stderr report, got %q", got)
	}
}

// A []error attribute, as function validation logs, is stored as text through the
// whole handler path, not as [{}]
func TestHandlerStoresErrorLists(t *testing.T) {
	lg, _, sink := testLogger(slog.LevelInfo, slog.LevelInfo)
	errs := []error{errors.New("bound-to does not match"), errors.New("unknown field")}
	lg.Warn("Function file failed validation", "errors", errs)

	r := drain(sink)[0]
	if !reflect.DeepEqual(r.Attrs["errors"], []string{"bound-to does not match", "unknown field"}) {
		t.Errorf("got %#v", r.Attrs["errors"])
	}
}
