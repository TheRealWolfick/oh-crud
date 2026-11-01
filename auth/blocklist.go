package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/models"
)

// Access tokens can't be recalled, so revoking a session, user or service writes a row
// to ohc_revocations and every instance keeps recent revocations in memory. A token is
// refused when any of its keys was revoked at or after the moment it was issued, so a
// login made after a revocation works straight away.
//
// An instance loads the recent rows once, at startup. After that it hears of each new
// revocation as it happens: its own directly, and other instances' as a message on the
// cluster bus (RevocationChannel). The database is never polled.
//
// Keys:
//   user:<users.id>  every token for the user
//   sid:<session>    every token in one login session (refresh token family)
//   svc:<id>         every token the service holds
const (
	revocationsTable = "ohc_revocations"

	// A revocation only matters while a token issued before it could still be live
	userTokenLifetime = 15 * time.Minute
	revocationMargin  = 5 * time.Minute
)

func userKey(id string) string       { return "user:" + id }
func sessionKey(sid string) string   { return "sid:" + sid }
func serviceKey(subject string) string { return subject } // service subjects are already svc:<id>

// BlockList is the in-memory copy of recent revocations
type BlockList struct {
	db       models.DBExecQuery
	lifetime func() time.Duration // the longest access token lifetime in use

	mu        sync.RWMutex
	revoked   map[string]time.Time // key -> latest revocation
	listeners []func(key string, at time.Time)
	share     func(Revocation) // sends this instance's revocations to its peers
}

// RevocationChannel is the cluster channel revocations travel on
const RevocationChannel = "auth.revocation"

// Revocation is the message one instance sends the others
type Revocation struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// ShareWith sets how this instance's revocations reach its peers. fn must not block.
func (b *BlockList) ShareWith(fn func(Revocation)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.share = fn
}

// Receive applies a revocation another instance made. It is the cluster handler for
// RevocationChannel, and safe to receive twice.
func (b *BlockList) Receive(data json.RawMessage) error {
	var r Revocation
	if err := json.Unmarshal(data, &r); err != nil { return err }
	if r.At.IsZero() || !(strings.HasPrefix(r.Key, "user:") || strings.HasPrefix(r.Key, "sid:") || strings.HasPrefix(r.Key, "svc:")) {
		return fmt.Errorf("not a revocation: %q at %v", r.Key, r.At)
	}
	b.apply(r.Key, r.At)
	return nil
}

// apply records a revocation in memory and announces it, unless an equal or later one
// for the key is already known (so a repeat does nothing)
func (b *BlockList) apply(key string, at time.Time) bool {
	b.mu.Lock()
	prev, ok := b.revoked[key]
	isNew := !ok || at.After(prev)
	if isNew { b.revoked[key] = at }
	b.mu.Unlock()
	if isNew { b.announce(map[string]time.Time{key: at}) }
	return isNew
}

// OnRevoke calls fn once for each revocation this instance learns of: one it makes, or
// one a peer sends. The websocket manager uses it to close sockets opened with a revoked
// token. fn runs on the revoking or receiving request's goroutine and must not block.
func (b *BlockList) OnRevoke(fn func(key string, at time.Time)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = append(b.listeners, fn)
}

func (b *BlockList) announce(revoked map[string]time.Time) {
	b.mu.RLock()
	listeners := b.listeners
	b.mu.RUnlock()
	for key, at := range revoked {
		for _, fn := range listeners { fn(key, at) }
	}
}

// TokenKeys are the revocation keys that cover a token: its user and session for a
// user token, its service for a service token
func TokenKeys(c *models.Claims) []string {
	switch c.TokenUse {
	case "user":
		keys := []string{userKey(c.Subject)}
		if c.SID != "" { keys = append(keys, sessionKey(c.SID)) }
		return keys
	case "service":
		return []string{serviceKey(c.Subject)}
	}
	return nil
}

// covered reports whether a token issued at iat falls under a revocation made at at.
// JWT times are whole seconds, so a token issued in the same second as the revocation
// can't be told apart from one issued just before it, and is covered too.
func covered(iat time.Time, at time.Time) bool { return !iat.After(at.Truncate(time.Second)) }

// RevocationCovers reports whether revoking key at moment at revokes the token c.
// A token with no iat can't be placed in time, so any revocation of its keys covers it.
func RevocationCovers(key string, at time.Time, c *models.Claims) bool {
	if c == nil || !slices.Contains(TokenKeys(c), key) { return false }
	return c.IssuedAt == nil || covered(c.IssuedAt.Time, at)
}

func NewBlockList(db models.DBExecQuery, serviceTokenLifetime func() time.Duration) *BlockList {
	return &BlockList{db: db, revoked: map[string]time.Time{}, lifetime: func() time.Duration {
		longest := userTokenLifetime
		if l := serviceTokenLifetime(); l > longest { longest = l }
		return longest
	}}
}

// window is how far back revocations are kept: the longest token lifetime plus a margin
func (b *BlockList) window() time.Duration { return b.lifetime() + revocationMargin }

// Blocked reports whether a token issued at iat for any of keys has been revoked.
// A token issued in the same second as a revocation is refused: JWT times are whole
// seconds, so it can't be told apart from one issued just before.
func (b *BlockList) Blocked(iat time.Time, keys ...string) bool {
	if b == nil { return false }
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, k := range keys {
		if at, ok := b.revoked[k]; ok && covered(iat, at) { return true }
	}
	return false
}

// Revoke records key, blocks it here at once, and sends it to the other instances.
// It returns the moment the revocation took effect.
func (b *BlockList) Revoke(ctx context.Context, key string, reason string) (time.Time, error) {
	var at time.Time
	err := b.db.QueryRow(ctx, `INSERT INTO `+revocationsTable+` (key, reason) VALUES ($1, $2) RETURNING created_at`,
		key, nullable(reason)).Scan(&at)
	if err != nil { return time.Time{}, err }
	b.apply(key, at)
	b.mu.RLock()
	share := b.share
	b.mu.RUnlock()
	if share != nil { share(Revocation{Key: key, At: at}) }
	return at, nil
}

// Load reads the revocations still inside the window, once, when the instance starts:
// those made before it was running, which no peer will send it again
func (b *BlockList) Load(ctx context.Context) error {
	rows, err := b.db.Query(ctx, `SELECT key, max(created_at) FROM `+revocationsTable+`
		WHERE created_at > now() - make_interval(secs => $1) GROUP BY key`, b.window().Seconds())
	if err != nil { return err }
	type row struct {
		Key string
		At  time.Time
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil { return err }

	for _, r := range list { b.apply(r.Key, r.At) }
	return nil
}

func nullable(s string) any {
	if s == "" { return nil }
	return s
}
