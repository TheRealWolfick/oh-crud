package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/models/modelstest"
)

func TestServicePublicKey(t *testing.T) {
	pub := make([]byte, 32)
	fake_db := &modelstest.FakeDB{Row: []any{pub, "svc-a", "reader", nil, nil}}
	s := NewServices(fake_db)

	key, svc, err := s.ServicePublicKey(context.Background(), "svc-a", "kid-1")
	if err != nil {
		t.Fatalf("ServicePublicKey: %v", err)
	}
	if len(key) != 32 || svc.ID != "svc-a" || svc.Role != "reader" {
		t.Errorf("Unexpected result: key %d bytes, service %+v", len(key), svc)
	}
	if svc.Entra_client_id != nil || svc.Disabled_at != nil {
		t.Errorf("Expected NULL columns to scan as nil, got %+v", svc)
	}

	call := fake_db.LastCall()
	if len(call.Args) != 2 || call.Args[0] != "kid-1" || call.Args[1] != "svc-a" {
		t.Errorf("Expected args [kid service_id], got %v", call.Args)
	}
	for _, want := range []string{"k.revoked_at IS NULL", "s.disabled_at IS NULL"} {
		if !strings.Contains(call.SQL, want) {
			t.Errorf("Expected the query to filter on %q", want)
		}
	}
}

func TestServicePublicKeyNotFound(t *testing.T) {
	// A nil Row scans as pgx.ErrNoRows, which is what a revoked key or disabled service returns
	s := NewServices(&modelstest.FakeDB{})

	key, svc, err := s.ServicePublicKey(context.Background(), "svc-a", "kid-1")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Expected pgx.ErrNoRows, got %v", err)
	}
	if key != nil || svc != nil {
		t.Errorf("Expected no key or service on error")
	}
}

func TestTouchServiceKey(t *testing.T) {
	fake_db := &modelstest.FakeDB{ExecTag: pgconn.NewCommandTag("UPDATE 1")}
	s := NewServices(fake_db)

	if err := s.TouchServiceKey(context.Background(), "kid-1"); err != nil {
		t.Fatalf("TouchServiceKey: %v", err)
	}
	call := fake_db.LastCall()
	if !strings.Contains(call.SQL, "last_used_at = now()") || len(call.Args) != 1 || call.Args[0] != "kid-1" {
		t.Errorf("Unexpected update: %s %v", call.SQL, call.Args)
	}
}

func TestTouchServiceKeyErrors(t *testing.T) {
	cases := map[string]*modelstest.FakeDB{
		"no row":   {ExecTag: pgconn.NewCommandTag("UPDATE 0")},
		"db error": {ExecErr: errors.New("db down")},
	}
	for name, fake_db := range cases {
		if err := NewServices(fake_db).TouchServiceKey(context.Background(), "kid-1"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
