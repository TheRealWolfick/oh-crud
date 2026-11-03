package management

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"


	"lotusforge.au/api-server/globals"
)

// fakeOhCRUD stands in for Oh CRUD: it issues tokens, refuses a reused refresh token
// (ending the session, as Oh CRUD does) and records admin API calls
type fakeOhCRUD struct {
	srv       *httptest.Server
	mu        sync.Mutex
	n         int
	refreshes atomic.Int32
	used      map[string]bool
	calls     []*http.Request
	answer    func(w http.ResponseWriter, r *http.Request) // the admin API's answer; 200 {} by default
	expiresIn int
}

func jwtFor(user string, n int) string {
	payload, _ := json.Marshal(map[string]any{"sub": "usr_" + user, "preferred_username": user, "roles": []string{"administrator"}, "amr": []string{"pwd"}, "n": n})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

func newFake(t *testing.T) *fakeOhCRUD {
	f := &fakeOhCRUD{used: map[string]bool{}, expiresIn: 900}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/oauth/token":
			r.ParseForm()
			if r.PostFormValue("client_assertion") == "" { w.WriteHeader(401); return }
			switch r.PostFormValue("grant_type") {
			case "client_credentials":
				json.NewEncoder(w).Encode(map[string]any{"access_token": "svc-token", "expires_in": 600})
			case "password":
				if r.PostFormValue("password") != "right" { w.WriteHeader(400); json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"}); return }
				f.n++
				json.NewEncoder(w).Encode(map[string]any{"access_token": jwtFor(r.PostFormValue("username"), f.n), "refresh_token": fmt.Sprint("rt-", f.n), "expires_in": f.expiresIn})
			case "refresh_token":
				f.refreshes.Add(1)
				rt := r.PostFormValue("refresh_token")
				if f.used[rt] { w.WriteHeader(400); json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"}); return }
				f.used[rt] = true
				f.n++
				json.NewEncoder(w).Encode(map[string]any{"access_token": jwtFor("alice", f.n), "refresh_token": fmt.Sprint("rt-", f.n), "expires_in": f.expiresIn})
			}
		case "/oauth/logout":
			f.calls = append(f.calls, r)
			w.WriteHeader(204)
		default:
			f.calls = append(f.calls, r.Clone(r.Context()))
			if f.answer != nil { f.answer(w, r); return }
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOhCRUD) lastCall() *http.Request { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[len(f.calls)-1] }

// dashboard runs the management server against f, and returns a browser for it
func dashboard(t *testing.T, f *fakeOhCRUD) (*httptest.Server, *http.Client, *Server) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	srv, err := NewServer(NewClient(f.srv.URL, f.srv.URL+"/oauth/token", "management", "m-1", key), slog.New(slog.DiscardHandler), false)
	if err != nil { t.Fatal(err) }
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return ts, browser, srv
}

func signIn(t *testing.T, ts *httptest.Server, b *http.Client, password string) *http.Response {
	req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(url.Values{"username": {"alice"}, "password": {password}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	res, err := b.Do(req)
	if err != nil { t.Fatal(err) }
	res.Body.Close()
	return res
}

// csrfOf reads the page's CSRF token, as app.js does
func csrfOf(t *testing.T, ts *httptest.Server, b *http.Client) string {
	res, err := b.Get(ts.URL + "/")
	if err != nil { t.Fatal(err) }
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	_, rest, ok := strings.Cut(string(body), `name="csrf" content="`)
	if !ok { t.Fatalf("no csrf on the page: %d", res.StatusCode) }
	tok, _, _ := strings.Cut(rest, `"`)
	return tok
}

func call(t *testing.T, ts *httptest.Server, b *http.Client, method, path, csrf, origin string) *http.Response {
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(`{"x":1}`))
	if csrf != "" { req.Header.Set("X-CSRF-Token", csrf) }
	if origin != "" { req.Header.Set("Origin", origin) }
	req.Header.Set("Content-Type", "application/json")
	res, err := b.Do(req)
	if err != nil { t.Fatal(err) }
	return res
}

func TestSignIn(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)

	if res, _ := b.Get(ts.URL + "/"); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Errorf("without a session the dashboard sends you to sign in, got %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res := signIn(t, ts, b, "wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d", res.StatusCode)
	}
	res := signIn(t, ts, b, "right")
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign in: got %d", res.StatusCode)
	}
	cookie := res.Header.Get("Set-Cookie")
	for _, want := range []string{globals.MgmtCookieName + "=", "HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("cookie %q lacks %s", cookie, want)
		}
	}
	if strings.Contains(cookie, "svc-token") || strings.Contains(cookie, "rt-") {
		t.Errorf("tokens must never reach the browser: %s", cookie)
	}

	// Signing in from another site is refused
	req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader("username=alice&password=right"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site sign in: got %d", res.StatusCode)
	}
}

func TestAPIForwards(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	signIn(t, ts, b, "right")
	csrf := csrfOf(t, ts, b)

	res := call(t, ts, b, "GET", "/api/users?status=active&q=al", "", "")
	res.Body.Close()
	up := f.lastCall()
	if res.StatusCode != 200 || up.URL.Path != "/admin/v1/users" || up.URL.RawQuery != "status=active&q=al" {
		t.Fatalf("forwarded to %s?%s (%d)", up.URL.Path, up.URL.RawQuery, res.StatusCode)
	}
	if up.Header.Get("Authorization") != "Bearer svc-token" || !strings.HasPrefix(up.Header.Get("X-OhCrud-User"), "h.") {
		t.Errorf("expected the service and user tokens, got %q %q", up.Header.Get("Authorization"), up.Header.Get("X-OhCrud-User"))
	}

	// Changes need the page's CSRF token and this site's origin
	for name, c := range map[string]struct{ csrf, origin string; want int }{
		"no csrf":      {"", ts.URL, 403},
		"wrong csrf":   {"nope", ts.URL, 403},
		"other origin": {csrf, "https://evil.example", 403},
		"no origin":    {csrf, "", 403},
		"good":         {csrf, ts.URL, 200},
	} {
		res := call(t, ts, b, "POST", "/api/users", c.csrf, c.origin)
		res.Body.Close()
		if res.StatusCode != c.want {
			t.Errorf("%s: got %d, want %d", name, res.StatusCode, c.want)
		}
	}
	if up := f.lastCall(); up.Method != "POST" || up.URL.Path != "/admin/v1/users" {
		t.Errorf("the good change should have been forwarded, last was %s %s", up.Method, up.URL.Path)
	}

	// Only the admin API, by a clean path
	for _, p := range []string{"/api/../oauth/token", "/api/users/%2e%2e/x", "/api/users/%2E%2E/%2e%2e/%2e%2e/oauth/token",
		"/api/users%2fx", "/api/users/%5c..", "/api"} {
		res := call(t, ts, b, "GET", p, "", "")
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Errorf("%s should not be forwarded", p)
		}
	}
}

// Several requests arriving together with an expired access token must refresh once:
// Oh CRUD ends the whole session if a refresh token is used twice
func TestConcurrentRefreshUsesTheTokenOnce(t *testing.T) {
	f := newFake(t)
	f.expiresIn = 1 // expires within refreshEarly, so every request wants a refresh
	ts, b, _ := dashboard(t, f)
	signIn(t, ts, b, "right")
	f.expiresIn = 900

	var wg sync.WaitGroup
	var fails atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := call(t, ts, b, "GET", "/api/overview", "", "")
			res.Body.Close()
			if res.StatusCode != 200 { fails.Add(1) }
		}()
	}
	wg.Wait()
	if fails.Load() != 0 || f.refreshes.Load() != 1 {
		t.Errorf("expected 20 successes on one refresh, got %d failures and %d refreshes", fails.Load(), f.refreshes.Load())
	}
}

// A 401 (a role removed, say) gets new tokens and one retry; if that fails too the
// session is over and the browser is told to sign in
func TestUnauthorizedRetriesOnceThenSignsOut(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	signIn(t, ts, b, "right")

	var n atomic.Int32
	f.answer = func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 { w.WriteHeader(401); return }
		w.Write([]byte(`{"ok":true}`))
	}
	if res := call(t, ts, b, "GET", "/api/overview", "", ""); res.StatusCode != 200 || f.refreshes.Load() != 1 {
		t.Errorf("expected a refresh and a successful retry, got %d after %d refreshes", res.StatusCode, f.refreshes.Load())
	}

	f.answer = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }
	res := call(t, ts, b, "GET", "/api/overview", "", "")
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("expected signed out with the cookie cleared, got %d %q", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	if res, _ := b.Get(ts.URL + "/"); res.StatusCode != http.StatusSeeOther {
		t.Errorf("the session should be gone")
	}
}

// A step-up challenge is the page's to handle: passed through, not retried
func TestStepUpPassesThrough(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	signIn(t, ts, b, "right")
	csrf := csrfOf(t, ts, b)
	f.answer = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_user_authentication", max_age=600`)
		w.WriteHeader(401)
	}
	res := call(t, ts, b, "POST", "/api/users", csrf, ts.URL)
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), "insufficient_user_authentication") || f.refreshes.Load() != 0 {
		t.Errorf("expected the challenge passed through without a refresh, got %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if res, _ := b.Get(ts.URL + "/"); res.StatusCode != 200 {
		t.Errorf("a step-up challenge must not end the session")
	}
}

func TestLogout(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	signIn(t, ts, b, "right")
	csrf := csrfOf(t, ts, b)

	req, _ := http.NewRequest("POST", ts.URL+"/logout", strings.NewReader("csrf=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	if res, _ := b.Do(req); res.StatusCode != 403 {
		t.Errorf("logout without the CSRF token: got %d", res.StatusCode)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/logout", strings.NewReader("csrf="+csrf))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	res, _ := b.Do(req)
	if res.StatusCode != http.StatusSeeOther || f.lastCall().URL.Path != "/oauth/logout" || !strings.HasPrefix(f.lastCall().Header.Get("X-OhCrud-User"), "h.") {
		t.Errorf("expected Oh CRUD told of the logout, got %d %s", res.StatusCode, f.lastCall().URL.Path)
	}
	if res, _ := b.Get(ts.URL + "/"); res.StatusCode != http.StatusSeeOther {
		t.Errorf("signed out: the dashboard should redirect")
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	res, _ := b.Get(ts.URL + "/login")
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "frame-ancestors 'none'", "default-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %s", csp, want)
		}
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected nosniff")
	}
	for _, p := range []string{"/static/app.js", "/static/app.css", "/static/sky.js"} {
		if res, _ := b.Get(ts.URL + p); res.StatusCode != 200 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
}

func TestSessionExpiresWhenIdle(t *testing.T) {
	st := NewStore()
	s := st.Start(&Tokens{Access: jwtFor("alice", 1), ExpiresAt: time.Now().Add(time.Hour)})
	s.lastSeen = time.Now().Add(-globals.MgmtIdleTimeout - time.Minute)
	if _, ok := st.Get(s.ID); ok {
		t.Errorf("an idle session should be gone")
	}
}
