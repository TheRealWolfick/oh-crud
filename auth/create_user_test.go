package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
	"lotusforge.au/api-server/models/modelstest"
)

func TestCreateUser(t *testing.T) {
	db := &modelstest.FakeDB{}
	a := &Auth{ks: &KeyStore{db: db}}

	u, err := a.CreateUser(context.Background(), "alice", "Alice", "hunter2", []string{"viewer"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if len(u.ID) != userIDLength {
		t.Errorf("Expected a %d character id, got %q", userIDLength, u.ID)
	}

	call := db.LastCall()
	if !strings.Contains(call.SQL, "INSERT INTO ohc_users") {
		t.Fatalf("Expected an insert into users, got %q", call.SQL)
	}
	if call.Args[0] != u.ID || call.Args[1] != "alice" {
		t.Errorf("Expected the generated id and username to be inserted, got %v", call.Args)
	}
	hash, ok := call.Args[3].(*string)
	if !ok || hash == nil {
		t.Fatalf("Expected a password hash, got %v", call.Args[3])
	}
	if match, _, _ := argon2id.CheckHash("hunter2", *hash); !match {
		t.Error("Stored hash does not match the password")
	}

	// A second user gets a different id
	u2, err := a.CreateUser(context.Background(), "bob", "", "", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u2.ID == u.ID {
		t.Error("Expected each user to get a new id")
	}
	if hash, _ := db.LastCall().Args[3].(*string); hash != nil {
		t.Error("Expected no hash for a user without a password")
	}
}

func TestCreateUserRejects(t *testing.T) {
	cases := map[string]string{
		"empty username": "",
		"too long":       strings.Repeat("a", 65),
		"service prefix": serviceUserPrefix + "reader",
	}
	for name, username := range cases {
		t.Run(name, func(t *testing.T) {
			db := &modelstest.FakeDB{}
			a := &Auth{ks: &KeyStore{db: db}}
			if _, err := a.CreateUser(context.Background(), username, "", "", nil); err == nil {
				t.Error("Expected an error")
			}
			if len(db.Calls) != 0 {
				t.Error("Expected nothing written")
			}
		})
	}

	t.Run("db error", func(t *testing.T) {
		a := &Auth{ks: &KeyStore{db: &modelstest.FakeDB{ExecErr: errors.New("duplicate key")}}}
		if _, err := a.CreateUser(context.Background(), "alice", "", "", nil); err == nil {
			t.Error("Expected the database error to be returned")
		}
	})
}
