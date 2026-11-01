package management

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

//go:embed ui
var uiFiles embed.FS

// CookieName holds the session id; it is all the browser gets
const CookieName = "ohc_mgmt"

// Server serves the dashboard and its API
type Server struct {
	client       *Client
	sessions     *Store
	lg           *slog.Logger
	secureCookie bool // false only for local development over http
	pages        *template.Template
}

func NewServer(client *Client, lg *slog.Logger, secureCookie bool) (*Server, error) {
	pages, err := template.ParseFS(uiFiles, "ui/index.html", "ui/login.html")
	if err != nil { return nil, err }
	return &Server{client: client, sessions: NewStore(), lg: lg, secureCookie: secureCookie, pages: pages}, nil
}

// Handler is every route, behind the security headers
func (srv *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(uiFiles, "ui/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /login", srv.loginPage)
	mux.HandleFunc("POST /login", srv.login)
	mux.HandleFunc("POST /logout", srv.logout)
	mux.HandleFunc("/api/", srv.api)
	mux.HandleFunc("GET /{$}", srv.app)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })
	return headers(mux)
}

// headers sets what every answer carries: no framing, no sniffing, and only this
// server's scripts and styles (plus the design's web fonts)
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' https://fonts.googleapis.com; "+
			"font-src https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// session finds the request's session, if it has one
func (srv *Server) session(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil { return nil, false }
	return srv.sessions.Get(c.Value)
}

func (srv *Server) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: srv.secureCookie, SameSite: http.SameSiteStrictMode})
}

// sameOrigin reports whether a changing request came from this site's own pages. With
// the SameSite cookie and the CSRF token it keeps other sites from acting for a person.
func sameOrigin(r *http.Request) bool {
	src := r.Header.Get("Origin")
	if src == "" { src = r.Header.Get("Referer") }
	u, err := url.Parse(src)
	return err == nil && src != "" && u.Host == r.Host
}

// ── Pages ────────────────────────────────────────────────────────────────

type loginView struct {
	Error    string
	Username string
}

func (srv *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := srv.session(r); ok { http.Redirect(w, r, "/", http.StatusSeeOther); return }
	srv.render(w, "login.html", http.StatusOK, loginView{})
}

func (srv *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) { http.Error(w, "forbidden", http.StatusForbidden); return }
	username := strings.TrimSpace(r.PostFormValue("username"))
	t, err := srv.client.Login(r.Context(), username, r.PostFormValue("password"), strings.TrimSpace(r.PostFormValue("otp")))
	switch {
	case errors.Is(err, ErrBadLogin):
		srv.render(w, "login.html", http.StatusUnauthorized, loginView{Error: "Those details didn't work. Check them, and your one-time code if you use one.", Username: username})
		return
	case errors.Is(err, ErrSlowDown):
		srv.render(w, "login.html", http.StatusTooManyRequests, loginView{Error: "Too many attempts. Wait a few minutes, then try again.", Username: username})
		return
	case err != nil:
		srv.lg.Error("login failed", "error", err)
		srv.render(w, "login.html", http.StatusBadGateway, loginView{Error: "Oh CRUD couldn't be reached. Try again shortly.", Username: username})
		return
	}
	s := srv.sessions.Start(t)
	srv.setCookie(w, s.ID, int(idleTimeout.Seconds()))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (srv *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) { http.Error(w, "forbidden", http.StatusForbidden); return }
	if s, ok := srv.session(r); ok {
		if r.PostFormValue("csrf") != s.CSRF { http.Error(w, "forbidden", http.StatusForbidden); return }
		if err := srv.client.Logout(r.Context(), s.current()); err != nil { srv.lg.Warn("logout at Oh CRUD failed", "error", err) }
		srv.sessions.End(s.ID)
	}
	srv.setCookie(w, "", -1)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type appView struct {
	CSRF     string
	Username string
	Roles    string
	Factor   string
}

// app is the dashboard shell; app.js fills it from /api
func (srv *Server) app(w http.ResponseWriter, r *http.Request) {
	s, ok := srv.session(r)
	if !ok { http.Redirect(w, r, "/login", http.StatusSeeOther); return }
	c := s.Claims()
	factor := "Password only"
	if slicesContains(c.AMR, "otp") { factor = "Password and one-time code" }
	if c.AuthTime > 0 { factor += ", since " + time.Unix(c.AuthTime, 0).Format("15:04") }
	srv.render(w, "index.html", http.StatusOK, appView{CSRF: s.CSRF, Username: c.Username, Roles: strings.Join(c.Roles, ", "), Factor: factor})
}

func (srv *Server) render(w http.ResponseWriter, name string, status int, v any) {
	var buf bytes.Buffer
	if err := srv.pages.ExecuteTemplate(&buf, name, v); err != nil {
		srv.lg.Error("render page", "page", name, "error", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// ── API ──────────────────────────────────────────────────────────────────

// api forwards /api/<path> to Oh CRUD's /admin/v1/<path> for the signed-in person
func (srv *Server) api(w http.ResponseWriter, r *http.Request) {
	s, ok := srv.session(r)
	if !ok { signedOut(w); return }

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if !sameOrigin(r) || r.Header.Get("X-CSRF-Token") != s.CSRF {
			apiProblem(w, http.StatusForbidden, "Refused", "This change didn't come from the dashboard.")
			return
		}
	}

	// Only the admin API is reachable, and only by a clean path. The check is on the
	// decoded path, and encoded dots, slashes and backslashes are refused outright:
	// Oh CRUD decodes them, so %2e%2e could otherwise climb out of /admin/v1.
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/api")
	decoded := strings.TrimPrefix(r.URL.Path, "/api")
	lower := strings.ToLower(escaped)
	if decoded == "" || path.Clean(decoded) != decoded || strings.Contains(decoded, "..") ||
		strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		apiProblem(w, http.StatusBadRequest, "Bad path", "")
		return
	}
	target := "/admin/v1" + escaped
	if r.URL.RawQuery != "" { target += "?" + r.URL.RawQuery }

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil { apiProblem(w, http.StatusBadRequest, "Bad body", ""); return }

	res, err := srv.forward(r, s, target, body, false)
	if err == nil && res.StatusCode == http.StatusUnauthorized && !strings.Contains(res.Header.Get("WWW-Authenticate"), "insufficient_user_authentication") {
		// The user's token was refused (a role removed, say) or the service's was: get
		// new ones and try once more
		res.Body.Close()
		srv.client.forgetServiceToken()
		res, err = srv.forward(r, s, target, body, true)
	}
	if errors.Is(err, ErrExpired) || (err == nil && res.StatusCode == http.StatusUnauthorized && !strings.Contains(res.Header.Get("WWW-Authenticate"), "insufficient_user_authentication")) {
		if res != nil { res.Body.Close() }
		srv.sessions.End(s.ID)
		srv.setCookie(w, "", -1)
		signedOut(w)
		return
	}
	if err != nil {
		srv.lg.Error("admin API call failed", "path", target, "error", err)
		apiProblem(w, http.StatusBadGateway, "Oh CRUD couldn't be reached", "Try again shortly.")
		return
	}
	defer res.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Disposition", "X-Request-Id", "WWW-Authenticate", "Cache-Control"} {
		if v := res.Header.Get(h); v != "" { w.Header().Set(h, v) }
	}
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}

func (srv *Server) forward(r *http.Request, s *Session, target string, body []byte, force bool) (*http.Response, error) {
	access, err := s.Access(r.Context(), srv.client, force)
	if err != nil { return nil, err }
	var rd io.Reader
	if len(body) > 0 { rd = bytes.NewReader(body) }
	return srv.client.Do(r.Context(), r.Method, target, rd, r.Header.Get("Content-Type"), access)
}

func apiProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": title, "status": status, "detail": detail})
}

// signedOut tells the page to go to the sign-in screen
func signedOut(w http.ResponseWriter) {
	apiProblem(w, http.StatusUnauthorized, "Signed out", "Sign in again.")
}

func slicesContains(s []string, v string) bool {
	for _, x := range s { if x == v { return true } }
	return false
}
