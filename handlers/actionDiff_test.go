package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
// batched: batch_number (a bigint column) must be read and formatted without going
// through the jsonb-oriented decodeJSONB helper, which previously tried to unmarshal a
// JSON number into a *string and silently left batch_code empty.
func TestDynamicActionDiff_ReadsExistingBatchCode(t *testing.T) {
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeRows{
				cols: []string{"missing_from_supplied", "missing_from_stored", "diffs", "batched", "batch_number"},
				data: [][]any{{nil, nil, nil, true, int64(7)}},
			}, nil
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
