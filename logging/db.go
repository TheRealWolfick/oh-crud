package logging

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/safe"
)

// logTable holds the application log, partitioned by day (UTC), so old days are
// dropped whole: instant, and leaving no dead rows for VACUUM. It isn't a YAML model:
// the model format can't declare partitioning, and the schema sync only manages the
// tables it is given, so the two never meet.
const logTable = "ohc_logs"

const setupSQL = `
CREATE TABLE IF NOT EXISTS ohc_logs (
    at         timestamptz NOT NULL,
    level      text        NOT NULL,
    severity   smallint    NOT NULL,
    msg        text        NOT NULL,
    request_id text,
    service    text        NOT NULL DEFAULT 'ohc',
    attrs      jsonb       NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (at);
CREATE INDEX IF NOT EXISTS ohc_logs_at ON ohc_logs (at);
CREATE INDEX IF NOT EXISTS ohc_logs_severity_at ON ohc_logs (severity, at);
CREATE INDEX IF NOT EXISTS ohc_logs_service_at ON ohc_logs (service, at);
CREATE INDEX IF NOT EXISTS ohc_logs_request_id ON ohc_logs (request_id) WHERE request_id IS NOT NULL;`

// DB is what AttachDB needs from a pgx pool
type DB interface {
	Copier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Settings are read again on every maintenance run, so server.yaml changes apply
// without a restart
type Settings struct {
	RetentionDays int    // days kept, today included
	Level         string // lowest level written to the database
}

// How far ahead partitions are made, and how often maintenance runs
const (
	daysAhead     = 3
	maintainEvery = time.Hour
)

// AttachDB creates ohc_logs and today's partitions, then starts writing every logger
// from Load to it, and keeps the partitions maintained until ctx ends. If the table
// can't be set up it returns the error and logging stays on stderr.
func AttachDB(ctx context.Context, db DB, settings func() Settings) (*Sink, error) {
	if _, err := db.Exec(ctx, setupSQL); err != nil {
		return nil, fmt.Errorf("create %s: %w", logTable, err)
	}
	s := settings()
	if _, _, err := maintain(ctx, db, time.Now(), s.RetentionDays); err != nil {
		return nil, fmt.Errorf("create %s partitions: %w", logTable, err)
	}

	sink := newSink(db)
	runCtx, cancel := context.WithCancel(ctx)
	sink.cancel = cancel
	go sink.run(runCtx)

	dbSlot.level.Store(int64(parseLogLevel(s.Level)))
	dbSlot.sink.Store(sink)

	safe.Go(fallback(), "attach db", func() {
		t := time.NewTicker(maintainEvery)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				s := settings()
				dbSlot.level.Store(int64(parseLogLevel(s.Level)))
				created, dropped, err := maintain(runCtx, db, time.Now(), s.RetentionDays)
				lg := fallback()
				if err != nil { lg.Error("log partition maintenance failed", "error", err); continue }
				if len(created) > 0 || len(dropped) > 0 {
					lg.Info("log partitions maintained", "created", created, "dropped", dropped, "retention_days", s.RetentionDays)
				}
			}
		}
	})
	return sink, nil
}

// maintain creates the coming days' partitions and drops those past retention. One
// instance at a time does it, under an advisory lock; the others skip the run.
func maintain(ctx context.Context, db DB, now time.Time, retentionDays int) (created []string, dropped []string, err error) {
	tx, err := db.Begin(ctx)
	if err != nil { return nil, nil, err }
	defer tx.Rollback(ctx)

	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('ohc_logs_maintenance'))`).Scan(&locked); err != nil {
		return nil, nil, err
	}
	if !locked { return nil, nil, nil }

	rows, err := tx.Query(ctx, `
		SELECT c.relname FROM pg_inherits i
		  JOIN pg_class c ON c.oid = i.inhrelid
		  JOIN pg_class p ON p.oid = i.inhparent
		 WHERE p.relname = $1`, logTable)
	if err != nil { return nil, nil, err }
	existing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil { return nil, nil, err }

	create, drop := partitionPlan(now, existing, retentionDays)
	for _, day := range create {
		name := partitionName(day)
		sql := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
			pgx.Identifier{name}.Sanitize(), logTable, day.Format("2006-01-02")+" 00:00:00+00", day.AddDate(0, 0, 1).Format("2006-01-02")+" 00:00:00+00")
		if _, err := tx.Exec(ctx, sql); err != nil { return nil, nil, err }
		created = append(created, name)
	}
	for _, name := range drop {
		if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS `+pgx.Identifier{name}.Sanitize()); err != nil { return nil, nil, err }
		dropped = append(dropped, name)
	}
	return created, dropped, tx.Commit(ctx)
}

const partitionPrefix = logTable + "_p"

func partitionName(day time.Time) string { return partitionPrefix + day.Format("20060102") }

// partitionDay is the UTC day a partition holds, if name is one of ours
func partitionDay(name string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(name, partitionPrefix)
	if !ok { return time.Time{}, false }
	d, err := time.Parse("20060102", rest)
	return d, err == nil
}

// partitionPlan says which days need a partition and which partitions are past
// retention. retentionDays counts today: 14 keeps today and the 13 days before it.
// Yesterday is made too (if retention keeps it), so a record stamped just before
// midnight on a fresh database still has somewhere to go.
func partitionPlan(now time.Time, existing []string, retentionDays int) (create []time.Time, drop []string) {
	// Round up retentionDays to at least one
	if retentionDays < 1 { retentionDays = 1 }

	// Round to the hour to get todays time
	today := now.UTC().Truncate(24 * time.Hour)

	// Track the oldest permissable table for tracking
	oldestKept := today.AddDate(0, 0, -(retentionDays - 1))

	// Read each existing partition
	have := map[time.Time]bool{}
	for _, name := range existing {
		// Extract the day the partition is for
		day, ok := partitionDay(name)
		if !ok { continue }
		// If it's too old, drop it
		if day.Before(oldestKept) {
			drop = append(drop, name)
			continue
		}
		have[day] = true
	}

	// Read yesterday as the first retention day
	first := today.AddDate(0, 0, -1)
	// If first date is before the oldest allowed date, update first to be the oldest date instead 
	if first.Before(oldestKept) { first = oldestKept }
	// For every day between first and 3 days later 
	for d := first; !d.After(today.AddDate(0, 0, daysAhead)); d = d.AddDate(0, 0, 1) {
		// If it doesn't exist, append it to the create list
		if !have[d] { create = append(create, d) }
	}
	slices.Sort(drop)
	return create, drop
}
