package secrets

import (
	"crypto/rand"

	"github.com/alexedwards/argon2id"
)

var Argon2Params = &argon2id.Params{
	Memory: 64 * 1024, Parallelism: 1, Iterations: 2, SaltLength: 16, KeyLength: 32,
}

// DummyHash is checked against when a user does not exist or has no password, so the
// response takes as long as a real password check and does not reveal valid usernames.
// It hashes a random password made at startup, so no submitted password can match it.
var DummyHash, _ = argon2id.CreateHash(rand.Text(), Argon2Params)

func HashPassword(pw string) (string, error) {
	return argon2id.CreateHash(pw, Argon2Params)
}

func NeedsRehash(p *argon2id.Params) bool {
	return p.Memory < Argon2Params.Memory || p.Iterations < Argon2Params.Iterations
}
