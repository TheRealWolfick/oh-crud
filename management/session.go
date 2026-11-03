package management

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"


	"lotusforge.au/api-server/globals"
)

// Sessions live in memory: restarting the management platform signs everyone out.
// That suits one instance; several would need the sessions shared.

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
	if !force && time.Now().Before(s.expiresAt.Add(-globals.MgmtRefreshEarly)) { return s.access, nil }
	t, err := c.Refresh(ctx, s.refresh)
	if err != nil { return "", err }
	s.set(t)
	return s.access, nil
}

// replace swaps in the tokens from a fresh login (one that changed the password),
// keeping the session id and CSRF token, and returns the access token it replaced
func (s *Session) replace(t *Tokens) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.access
	s.set(t)
	return old
}

// current is the access token as it stands, for logging out
func (s *Session) current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.access
}

// pendingLogin is a sign-in Oh CRUD answered with password_change_required: the
// password and any code were right, but an admin set the password, so it must be
// replaced first. It is held here so the password is never written into the page.
type pendingLogin struct {
	username, password, otp string
	at                      time.Time
}

// Store holds the sessions, and the sign-ins waiting for a new password
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
	pending  map[string]*pendingLogin
}

func NewStore() *Store { return &Store{sessions: map[string]*Session{}, pending: map[string]*pendingLogin{}} }

// Hold keeps a sign-in that needs a new password, and returns its id. Expired ones are
// cleared out here, as new ones arrive.
func (st *Store) Hold(username, password, otp string) string {
	id := randomID()
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, p := range st.pending {
		if time.Since(p.at) > globals.MgmtPendingTimeout { delete(st.pending, k) }
	}
	st.pending[id] = &pendingLogin{username: username, password: password, otp: otp, at: time.Now()}
	return id
}

// Held finds a sign-in still waiting for a new password
func (st *Store) Held(id string) (pendingLogin, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	p, ok := st.pending[id]
	if !ok { return pendingLogin{}, false }
	if time.Since(p.at) > globals.MgmtPendingTimeout {
		delete(st.pending, id)
		return pendingLogin{}, false
	}
	return *p, true
}

// Release forgets a waiting sign-in
func (st *Store) Release(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.pending, id)
}

// Start makes a session for freshly issued tokens. Expired sessions are cleared out
// here, as new ones arrive, rather than by a timer.
func (st *Store) Start(t *Tokens) *Session {
	s := &Session{ID: randomID(), CSRF: randomID(), lastSeen: time.Now()}
	s.set(t)
	st.mu.Lock()
	defer st.mu.Unlock()
	for id, old := range st.sessions {
		if time.Since(old.lastSeen) > globals.MgmtIdleTimeout { delete(st.sessions, id) }
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
	if time.Since(s.lastSeen) > globals.MgmtIdleTimeout {
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
