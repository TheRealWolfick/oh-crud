package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// teeHandler sends each record to stderr and to the database, each at its own level
type teeHandler struct {
	primary slog.Handler
	db      *dbHandler
}

func (t *teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return t.primary.Enabled(ctx, l) || t.db.Enabled(ctx, l)
}

func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	if t.primary.Enabled(ctx, r.Level) { err = t.primary.Handle(ctx, r.Clone()) }
	if t.db.Enabled(ctx, r.Level) { t.db.Handle(ctx, r) }
	return err
}

func (t *teeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &teeHandler{primary: t.primary.WithAttrs(as), db: t.db.withAttrs(as)}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{primary: t.primary.WithGroup(name), db: t.db.withGroup(name)}
}

// slot is where AttachDB puts the sink and the level; empty until then
type slot struct {
	sink  atomic.Pointer[Sink]
	level atomic.Int64 // slog.Level
}

var dbSlot slot

// Row is one record as stored in ohc_logs
type Row struct {
	At        time.Time
	Level     slog.Level
	Msg       string
	RequestID string
	Service   string
	Attrs     map[string]any
}

// boundAttr is an attribute added with WithAttrs, under the groups open at the time
type boundAttr struct {
	groups []string
	attr   slog.Attr
}

// dbHandler turns records into Rows for the sink
type dbHandler struct {
	slot   *slot
	bound  []boundAttr
	groups []string
}

func newDBHandler(s *slot) *dbHandler { return &dbHandler{slot: s} }

func (h *dbHandler) Enabled(_ context.Context, l slog.Level) bool {
	return h.slot.sink.Load() != nil && l >= slog.Level(h.slot.level.Load())
}

func (h *dbHandler) Handle(_ context.Context, r slog.Record) error {
	sink := h.slot.sink.Load()
	if sink == nil { return nil }

	attrs := map[string]any{}
	for _, b := range h.bound { put(attrs, b.groups, b.attr) }
	r.Attrs(func(a slog.Attr) bool { put(attrs, h.groups, a); return true })

	row := Row{At: r.Time, Level: r.Level, Msg: r.Message, Service: "ohc", Attrs: attrs}
	if row.At.IsZero() { row.At = time.Now() }
	// A top-level request_id gets its own column, so one request can be found quickly
	if id, ok := attrs["request_id"].(string); ok {
		row.RequestID = id
		delete(attrs, "request_id")
	}
	sink.enqueue(row)
	return nil
}

func (h *dbHandler) withAttrs(as []slog.Attr) *dbHandler {
	n := &dbHandler{slot: h.slot, groups: h.groups, bound: append([]boundAttr{}, h.bound...)}
	for _, a := range as { n.bound = append(n.bound, boundAttr{groups: h.groups, attr: a}) }
	return n
}

func (h *dbHandler) withGroup(name string) *dbHandler {
	if name == "" { return h }
	groups := append(append([]string{}, h.groups...), name)
	return &dbHandler{slot: h.slot, bound: h.bound, groups: groups}
}

// put adds a under the nested maps named by groups, following slog's rules: an empty
// attribute is dropped, and a group with no key is inlined
func put(m map[string]any, groups []string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) { return }
	target := m
	for _, g := range groups {
		next, ok := target[g].(map[string]any)
		if !ok { next = map[string]any{}; target[g] = next }
		target = next
	}
	if a.Value.Kind() == slog.KindGroup {
		members := a.Value.Group()
		if len(members) == 0 { return }
		if a.Key == "" {
			for _, ga := range members { put(target, nil, ga) }
			return
		}
		inner, ok := target[a.Key].(map[string]any)
		if !ok { inner = map[string]any{}; target[a.Key] = inner }
		for _, ga := range members { put(inner, nil, ga) }
		return
	}
	if a.Key == "" { return }
	target[a.Key] = value(a.Value)
}

// value is v in a form that marshals to useful JSON
func value(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339Nano)
	}
	a := v.Any()
	switch x := a.(type) {
	case nil:
		return nil
	case error:
		return x.Error()
	case []error:
		// errors marshal to {}, so a list of them would store as [{}, {}]
		out := make([]string, len(x))
		for i, e := range x {
			if e != nil { out[i] = e.Error() }
		}
		return out
	case json.Marshaler:
		return x
	case fmt.Stringer:
		return x.String()
	}
	if _, err := json.Marshal(a); err != nil { return fmt.Sprintf("%+v", a) }
	return a
}
