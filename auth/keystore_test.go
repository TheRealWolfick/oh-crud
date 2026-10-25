package auth

import (
	"context"
	"crypto/ed25519"
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

func mustPublish(t *testing.T, ks *KeyStore, k *SigningKey) {
	t.Helper()
	if err := ks.Publish(context.Background(), k); err != nil {
		t.Fatalf("Publish %s: %v", k.KID, err)
	}
}

func TestKeyStorePublish(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	k := newTestKey(t)

	if err := ks.Publish(context.Background(), k); err != nil {
		t.Fatalf("Publish: %v", err)
	}

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

	if err := ks.Publish(context.Background(), k); err == nil {
		t.Errorf("Expected an error when the insert fails")
	}

	if _, found := ks.Lookup(k.KID); found {
		t.Errorf("Key should not be published when the insert fails")
	}
}

func TestKeyStoreActivate(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	first, second := newTestKey(t), newTestKey(t)
	mustPublish(t, ks, first)
	mustPublish(t, ks, second)

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
	mustPublish(t, ks, k)
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

func TestKeyStorePublishInvalidKey(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	k := newTestKey(t)
	k.Public = newTestKey(t).Public

	if err := ks.Publish(context.Background(), k); err == nil {
		t.Fatalf("Expected an error for mismatched key halves")
	}
	if len(fake_db.Calls) != 0 {
		t.Errorf("Invalid key must not reach the database, got %+v", fake_db.Calls)
	}
}

// fakeSigningKeysTable makes Exec store inserted keys and status changes in the
// FakeDB's query rows, so a later RefreshKeys reads back what was written
func fakeSigningKeysTable(fake_db *modelstest.FakeDB) {
	fake_db.ExecFunc = func(sql string, args []any) (pgconn.CommandTag, error) {
		switch {
		case strings.Contains(sql, "INSERT INTO signing_keys"):
			pub := args[1].(ed25519.PublicKey)
			fake_db.Rows.Data = append(fake_db.Rows.Data, []any{args[0], []byte(pub), args[2], args[3]})
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		case strings.Contains(sql, "status = 'active'"):
			for _, row := range fake_db.Rows.Data {
				if row[0] == args[0] {
					row[3] = "active"
					return pgconn.NewCommandTag("UPDATE 1"), nil
				}
			}
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
}

func TestKeyStoreRefreshKeysEmptyCreatesKey(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	fakeSigningKeysTable(fake_db)

	if err := ks.RefreshKeys(context.Background()); err != nil {
		t.Fatalf("RefreshKeys on an empty table: %v", err)
	}
	active := ks.Active()
	if active == nil {
		t.Fatalf("Expected a key to be created and activated")
	}
	if _, found := ks.Lookup(active.KID); !found || len(ks.JWKS()) != 1 {
		t.Errorf("Expected only %s in the JWKS, got %+v", active.KID, ks.JWKS())
	}
	if len(fake_db.Rows.Data) != 1 || fake_db.Rows.Data[0][3] != "active" {
		t.Errorf("Expected one active row in the database, got %+v", fake_db.Rows.Data)
	}
}

func TestKeyStoreRefreshKeysEmptyInsertFails(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	fake_db.ExecErr = errors.New("db down")

	if err := ks.RefreshKeys(context.Background()); err == nil {
		t.Fatalf("Expected an error when the bootstrap insert fails")
	}
	if ks.Active() != nil || len(ks.JWKS()) != 0 {
		t.Errorf("Keys must not change when the bootstrap insert fails")
	}
}

func TestKeyStoreRefreshKeysEmptyWhileLoaded(t *testing.T) {
	ks, fake_db := newTestKeyStore(t)
	loaded := newTestKey(t)
	fake_db.Rows.Data = [][]any{keyRow(t, loaded, "active")}
	if err := ks.RefreshKeys(context.Background()); err != nil {
		t.Fatalf("RefreshKeys: %v", err)
	}

	// The table is emptied while the server is running
	fake_db.Rows.Data = nil
	fake_db.Calls = nil

	if err := ks.RefreshKeys(context.Background()); err == nil {
		t.Fatalf("Expected an error when the table empties while keys are loaded")
	}
	if got := ks.Active(); got == nil || got.KID != loaded.KID || len(ks.JWKS()) != 1 {
		t.Errorf("Expected the loaded key %s to be kept, got %+v", loaded.KID, got)
	}
	for _, c := range fake_db.Calls {
		if c.Method == "Exec" {
			t.Errorf("No key should be created while keys are loaded, got %s", c.SQL)
		}
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
