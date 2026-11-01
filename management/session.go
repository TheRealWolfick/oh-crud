package management

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Sessions live in memory: restarting the management platform signs everyone out.
// That suits one instance; several would need the sessions shared.

// idleTimeout signs a person out after this long without a request
const idleTimeout = 12 * time.Hour

// refreshEarly renews the access token this long before it expires, so a request never
// carries one that dies in flight
const refreshEarly = 30 * time.Second

// Session is one signed-in person
type Session struct {
	ID   string
	CSRF string // sent with every change, from the page, so another site can't make one

	mu        sync.Mutex // held while refreshing: a refresh token is single use
	access    string
	refresh   string
	expiresAt time.Time
	claims    Claims
	lastSeen  time.Time
}

func randomID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Claims is what the session's current access token says about the person
func (s *Session) Claims() Claims {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

func (s *Session) set(t *Tokens) {
	s.access, s.refresh, s.expiresAt = t.Access, t.Refresh, t.ExpiresAt
	s.claims = readClaims(t.Access)
}

// Access returns a live access token, refreshing it first if it is about to expire.
// force refreshes regardless, after Oh CRUD refused the current one (a role removed,
// say). Requests on one session wait for each other here, so the refresh token is
// only ever used once: Oh CRUD ends the whole session if one is used twice.
func (s *Session) Access(ctx context.Context, c *Client, force bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && time.Now().Before(s.expiresAt.Add(-refreshEarly)) { return s.access, nil }
	t, err := c.Refresh(ctx, s.refresh)
	if err != nil { return "", err }
	s.set(t)
	return s.access, nil
}

// current is the access token as it stands, for logging out
func (s *Session) current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.access
}

// Store holds the sessions
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewStore() *Store { return &Store{sessions: map[string]*Session{}} }

// Start makes a session for freshly issued tokens. Expired sessions are cleared out
// here, as new ones arrive, rather than by a timer.
func (st *Store) Start(t *Tokens) *Session {
	s := &Session{ID: randomID(), CSRF: randomID(), lastSeen: time.Now()}
	s.set(t)
	st.mu.Lock()
	defer st.mu.Unlock()
	for id, old := range st.sessions {
		if time.Since(old.lastSeen) > idleTimeout { delete(st.sessions, id) }
	}
	st.sessions[s.ID] = s
	return s
}

// Get finds a live session and marks it seen
func (st *Store) Get(id string) (*Session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok { return nil, false }
	if time.Since(s.lastSeen) > idleTimeout {
		delete(st.sessions, id)
		return nil, false
	}
	s.lastSeen = time.Now()
	return s, true
}

// End removes a session
func (st *Store) End(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, id)
}
