package management

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"lotusforge.au/api-server/globals"
)

func hasCookie(b *http.Client, ts string, name string) bool {
	u, _ := url.Parse(ts + "/login/password")
	for _, c := range b.Jar.Cookies(u) { if c.Name == name { return true } }
	return false
}

func TestRequiredPasswordChange(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)

	res, body := postForm(t, ts, b, "/login", url.Values{"username": {"newbie"}, "password": {"right"}, "otp": {"123456"}})
	if res.StatusCode != 200 || !strings.Contains(body, "Choose a new password") { t.Fatalf("sign-in needing a change: %d", res.StatusCode) }
	if strings.Contains(body, `value="right"`) { t.Fatal("the password was written into the page") }
	if !hasCookie(b, ts.URL, globals.MgmtPendingCookieName) { t.Fatal("no pending cookie") }
	if res, _ := b.Get(ts.URL + "/"); res.StatusCode != http.StatusSeeOther { t.Fatal("signed in before choosing a password") }

	res, body = postForm(t, ts, b, "/login/password", url.Values{"new_password": {"a-long-new-password"}, "confirm": {"something-else-here"}})
	if res.StatusCode != 400 || !strings.Contains(body, "don&#39;t match") { t.Fatalf("mismatch: %d", res.StatusCode) }

	res, body = postForm(t, ts, b, "/login/password", url.Values{"new_password": {"short"}, "confirm": {"short"}})
	if res.StatusCode != 400 || !strings.Contains(body, "Use at least 12 characters.") { t.Fatalf("refused password: %d", res.StatusCode) }

	res, _ = postForm(t, ts, b, "/login/password", url.Values{"new_password": {"a-long-new-password"}, "confirm": {"a-long-new-password"}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" { t.Fatalf("change: %d", res.StatusCode) }
	got := f.logins[len(f.logins)-1]
	if got.Get("username") != "newbie" || got.Get("password") != "right" || got.Get("new_password") != "a-long-new-password" || got.Get("otp") != "123456" {
		t.Fatalf("login sent %v", got)
	}
	if hasCookie(b, ts.URL, globals.MgmtPendingCookieName) { t.Fatal("pending cookie kept") }
	if csrfOf(t, ts, b) == "" { t.Fatal("not signed in") }
}

func TestRequiredPasswordChangeFreshCode(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	postForm(t, ts, b, "/login", url.Values{"username": {"newbie"}, "password": {"right"}, "otp": {"111111"}})
	postForm(t, ts, b, "/login/password", url.Values{"new_password": {"a-long-new-password"}, "confirm": {"a-long-new-password"}, "otp": {"222222"}})
	if got := f.logins[len(f.logins)-1].Get("otp"); got != "222222" { t.Fatalf("sent code %q, want the fresh one", got) }
}

func TestRequiredPasswordChangeWithoutPending(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	res, body := postForm(t, ts, b, "/login/password", url.Values{"new_password": {"a-long-new-password"}, "confirm": {"a-long-new-password"}})
	if res.StatusCode != 401 || !strings.Contains(body, "Sign in again") { t.Fatalf("no pending sign-in: %d", res.StatusCode) }
	if len(f.logins) != 0 { t.Fatal("logged in without a held sign-in") }
}

func TestStartAgainDropsPending(t *testing.T) {
	f := newFake(t)
	ts, b, srv := dashboard(t, f)
	postForm(t, ts, b, "/login", url.Values{"username": {"newbie"}, "password": {"right"}})
	if len(srv.sessions.pending) != 1 { t.Fatal("not held") }
	b.Get(ts.URL + "/login")
	if len(srv.sessions.pending) != 0 { t.Fatal("start again kept the held sign-in") }
}

func changeOwn(t *testing.T, ts string, b *http.Client, csrf, body string) *http.Response {
	req, _ := http.NewRequest("POST", ts+"/account/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts)
	if csrf != "" { req.Header.Set("X-CSRF-Token", csrf) }
	res, err := b.Do(req)
	if err != nil { t.Fatal(err) }
	res.Body.Close()
	return res
}

func TestChangeOwnPassword(t *testing.T) {
	f := newFake(t)
	ts, b, _ := dashboard(t, f)
	signIn(t, ts, b, "right")
	csrf := csrfOf(t, ts, b)

	if res := changeOwn(t, ts.URL, b, "", `{"current_password":"right","new_password":"a-long-new-password"}`); res.StatusCode != 403 {
		t.Fatalf("without the CSRF token: %d", res.StatusCode)
	}
	if res := changeOwn(t, ts.URL, b, csrf, `{"current_password":"wrong","new_password":"a-long-new-password"}`); res.StatusCode != 400 {
		t.Fatalf("wrong current password: %d", res.StatusCode)
	}
	if res := changeOwn(t, ts.URL, b, csrf, `{"current_password":"right","new_password":"short"}`); res.StatusCode != 400 {
		t.Fatalf("refused new password: %d", res.StatusCode)
	}
	before := len(f.calls)
	if res := changeOwn(t, ts.URL, b, csrf, `{"current_password":"right","new_password":"a-long-new-password"}`); res.StatusCode != 204 {
		t.Fatalf("change: %d", res.StatusCode)
	}
	if got := f.logins[len(f.logins)-1]; got.Get("username") != "alice" || got.Get("new_password") != "a-long-new-password" { t.Fatalf("login sent %v", got) }
	if len(f.calls) != before+1 || f.lastCall().URL.Path != "/oauth/logout" { t.Fatal("old session not ended") }

	// The same session and CSRF token carry on, on the new tokens
	res := call(t, ts, b, "POST", "/api/users/x/enable", csrf, ts.URL)
	res.Body.Close()
	if res.StatusCode != 200 { t.Fatalf("after the change: %d", res.StatusCode) }
	if !strings.Contains(f.lastCall().Header.Get("X-OhCrud-User"), ".") || f.lastCall().Header.Get("X-OhCrud-User") != jwtFor("alice", f.n) {
		t.Fatal("not using the new login's token")
	}
}
