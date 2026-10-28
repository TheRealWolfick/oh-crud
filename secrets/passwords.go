package secrets

import "github.com/alexedwards/argon2id"

var Argon2Params = &argon2id.Params{
	Memory: 64 * 1024, Parallelism: 1, Iterations: 2, SaltLength: 16, KeyLength: 32,
}

// DummyHash is checked against when a user does not exist, so the response
// takes as long as a real password check and does not reveal valid usernames
var DummyHash, _ = argon2id.CreateHash("fake-password", Argon2Params)

func HashPassword(pw string) (string, error) {
	return argon2id.CreateHash(pw, Argon2Params)
}

func NeedsRehash(p *argon2id.Params) bool {
	return p.Memory < Argon2Params.Memory || p.Iterations < Argon2Params.Iterations
}
