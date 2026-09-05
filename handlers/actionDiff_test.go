package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/models"
)

// TestDynamicActionDiff_GeneratesBatchCode covers the primary path: a not-yet-batched
// diff row is read, generate_batch_number() is called, and the result is scanned and
// returned as batch_code. Previously this scanned a SQL int directly into a *string,
// which errors on every call — this is a regression test for that.
func TestDynamicActionDiff_GeneratesBatchCode(t *testing.T) {
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeRows{
				cols: []string{"missing_from_supplied", "missing_from_stored", "diffs", "batched"},
				data: [][]any{{nil, nil, nil, false}},
			}, nil
		},
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return &fakeRow{values: []any{int64(42)}}
		},
	}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg()

	handler := dynamicActionDiff(cfg, qm, &models.ServerConfig{})
	req := requestWithRoles("/assets/diff?checksum=abc123", []string{"editor"})
	req.Method = http.MethodPut
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["batch_code"] != "42" {
		t.Errorf(`expected batch_code "42", got %v`, body["batch_code"])
	}
}

// TestDynamicActionDiff_ReadsExistingBatchCode covers the case where the diff is already
// batched: the atomic claim UPDATE matches no row (batched is already true), so the
// handler must fall back to reading the persisted batch_number back out rather than
// calling generate_batch_number again.
func TestDynamicActionDiff_ReadsExistingBatchCode(t *testing.T) {
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeRows{
				cols: []string{"missing_from_supplied", "missing_from_stored", "diffs", "batched", "batch_number"},
				data: [][]any{{nil, nil, nil, true, int64(7)}},
			}, nil
		},
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "UPDATE diffs") {
				// Already batched -- the claim matches no row.
				return &fakeRow{err: pgx.ErrNoRows}
			}
			return &fakeRow{values: []any{int64(7)}}
		},
	}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg()

	handler := dynamicActionDiff(cfg, qm, &models.ServerConfig{})
	req := requestWithRoles("/assets/diff?checksum=abc123", []string{"editor"})
	req.Method = http.MethodPut
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["batch_code"] != "7" {
		t.Errorf(`expected batch_code "7" for an already-batched diff, got %v`, body["batch_code"])
	}
}

// TestDynamicActionDiff_ConcurrentActionDoesNotRegenerateBatch covers the fatal
// production bug: a frontend that fires several requests for the same checksum in
// parallel (e.g. action + view + missing/supplied + missing/stored all on first open)
// must not cause generate_batch_number to be invoked more than once for that checksum.
// The claim UPDATE's WHERE batched = false clause is what makes losing racers fall
// through to a plain re-read instead of re-running the generator.
func TestDynamicActionDiff_ConcurrentActionDoesNotRegenerateBatch(t *testing.T) {
	claimCalls := 0
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeRows{
				cols: []string{"missing_from_supplied", "missing_from_stored", "diffs", "batched"},
				data: [][]any{{nil, nil, nil, false}},
			}, nil
		},
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "UPDATE diffs") {
				claimCalls++
				if claimCalls == 1 {
					return &fakeRow{values: []any{int64(9)}}
				}
				// A second, concurrent PUT loses the race: the row is already batched.
				return &fakeRow{err: pgx.ErrNoRows}
			}
			return &fakeRow{values: []any{int64(9)}}
		},
	}
	qm := newTestQueueManager(db)
	cfg := diffResourceCfg()
	handler := dynamicActionDiff(cfg, qm, &models.ServerConfig{})

	for i := 0; i < 2; i++ {
		req := requestWithRoles("/assets/diff?checksum=abc123", []string{"editor"})
		req.Method = http.MethodPut
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d (body: %s)", i, rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("request %d: failed to decode response: %v", i, err)
		}
		if body["batch_code"] != "9" {
			t.Errorf("request %d: expected batch_code \"9\", got %v", i, body["batch_code"])
		}
	}

	if claimCalls != 2 {
		t.Fatalf("expected 2 claim attempts (one per request), got %d", claimCalls)
	}
}
