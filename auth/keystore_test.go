package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"lotusforge.au/api-server/models/modelstest"
	"lotusforge.au/api-server/secrets"
)

// AES-256 needs a 32 byte master key
var testMaster = []byte("0123456789abcdef0123456789abcdef")

func newTestKeyStore(t *testing.T) (*KeyStore, *modelstest.FakeDB) {
	t.Helper()
	fake_db := &modelstest.FakeDB{ExecTag: pgconn.NewCommandTag("UPDATE 1")}
	return NewKeyStore("https://ohcrud.internal", "oh-crud", fake_db, testMaster), fake_db
}

func newTestKey(t *testing.T) *SigningKey {
	t.Helper()
	k, err := NewSigningKey()
	if err != nil {
		t.Fatalf("NewSigningKey: %v", err)
	}
	return k
}

func TestKeyStorePublish(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	k := newTestKey(t)

	ks.Publish(k)

	if _, found := ks.Lookup(k.KID); !found {
		t.Fatalf("Expected %s to be published", k.KID)
	}
	call := fake_db.LastCall()
	if !strings.Contains(call.SQL, "INSERT INTO signing_keys") || len(call.Args) != 4 {
		t.Errorf("Unexpected insert: %s %v", call.SQL, call.Args)
	}
}

func TestKeyStorePublishNotSaved(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	fake_db.ExecErr = errors.New("db down")
	k := newTestKey(t)

	ks.Publish(k)

	if _, found := ks.Lookup(k.KID); found {
		t.Errorf("Key should not be published when the insert fails")
	}
}

func TestKeyStoreActivate(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	first, second := newTestKey(t), newTestKey(t)
	ks.Publish(first)
	ks.Publish(second)

	if err := ks.Activate(context.Background(), first.KID); err != nil {
		t.Fatalf("Activate first: %v", err)
	}
	fake_db.Calls = nil

	if err := ks.Activate(context.Background(), second.KID); err != nil {
		t.Fatalf("Activate second: %v", err)
	}
	if ks.Active() != second {
		t.Errorf("Expected %s to be active", second.KID)
	}
	if !fake_db.Tx.Committed {
		t.Errorf("Expected the transaction to commit")
	}
	if len(fake_db.Calls) != 2 || !strings.Contains(fake_db.Calls[0].SQL, "'retiring'") || fake_db.Calls[0].Args[0] != first.KID {
		t.Errorf("Expected a retire of %s then an activate, got %+v", first.KID, fake_db.Calls)
	}
}

func TestKeyStoreActivateCommitFails(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	k := newTestKey(t)
	ks.Publish(k)
	fake_db.CommitErr = errors.New("commit failed")

	if err := ks.Activate(context.Background(), k.KID); err == nil {
		t.Fatalf("Expected an error when commit fails")
	}
	if ks.Active() != nil {
		t.Errorf("Active key must not change when commit fails")
	}
}

func TestKeyStoreRefreshKeys(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	active, next := newTestKey(t), newTestKey(t)
	fake_db.Rows.Data = [][]any{
		keyRow(t, active, "active"),
		keyRow(t, next, "next"),
	}

	if err := ks.RefreshKeys(context.Background()); err != nil {
		t.Fatalf("RefreshKeys: %v", err)
	}
	if got := ks.Active(); got == nil || got.KID != active.KID {
		t.Errorf("Expected %s to be active, got %+v", active.KID, got)
	}
	if len(ks.JWKS()) != 2 {
		t.Errorf("Expected 2 keys in the JWKS, got %d", len(ks.JWKS()))
	}
}

func TestKeyStoreRefreshKeysReadError(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	fake_db.Rows.Data = [][]any{keyRow(t, newTestKey(t), "active")}
	fake_db.Rows.IterErr = errors.New("connection reset")

	if err := ks.RefreshKeys(context.Background()); err == nil {
		t.Fatalf("Expected the row read error to be returned")
	}
	if ks.Active() != nil {
		t.Errorf("Keys must not be swapped in after a read error")
	}
}

// keyRow builds a signing_keys row the way RefreshKeys selects it
func keyRow(t *testing.T, k *SigningKey, status string) []any {
	t.Helper()
	sealed, err := secrets.Encrypt(testMaster, k.Private, []byte(k.KID))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return []any{k.KID, []byte(k.Public), sealed, status}
}
