// Package management is the management platform: the admin dashboard and the
// backend-for-frontend (BFF) it runs on. It is an Oh CRUD service like any other,
// registered as "management" and listed in Oh CRUD's admin.services. It holds the
// service's private key, signs people in through Oh CRUD's password grant, keeps their
// tokens on the server, and forwards the dashboard's calls to /admin/v1. The browser
// only ever holds a session cookie.
package management

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Client talks to Oh CRUD as the management service
type Client struct {
	base      string // Oh CRUD's base URL, e.g. https://api.example.com
	audience  string // Oh CRUD's token-url, which every assertion names as its audience
	serviceID string
	kid       string
	key       ed25519.PrivateKey
	http      *http.Client

	mu        sync.Mutex
	svcToken  string
	svcExpiry time.Time
}

func NewClient(base, audience, serviceID, kid string, key ed25519.PrivateKey) *Client {
	return &Client{base: strings.TrimRight(base, "/"), audience: audience, serviceID: serviceID, kid: kid, key: key,
		http: &http.Client{Timeout: 30 * time.Second}}
}

// Tokens are a user's tokens from a login or a refresh
type Tokens struct {
	Access    string
	Refresh   string
	ExpiresAt time.Time
}

// Errors from signing in
var (
	ErrBadLogin = errors.New("those details didn't work")
	ErrSlowDown = errors.New("too many attempts")
	ErrExpired  = errors.New("session expired")
)

// assertion is the signed statement that proves this request comes from the service.
// Each is good for a minute and used once.
func (c *Client) assertion() (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Issuer: c.serviceID, Subject: c.serviceID, Audience: jwt.ClaimStrings{c.audience},
		ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
	})
	tok.Header["kid"] = c.kid
	return tok.SignedString(c.key)
}

// token posts a grant to Oh CRUD's token endpoint with a fresh assertion
func (c *Client) token(ctx context.Context, form url.Values) (int, map[string]any, error) {
	a, err := c.assertion()
	if err != nil { return 0, nil, err }
	form.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
	form.Set("client_assertion", a)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil { return 0, nil, err }
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil { return 0, nil, err }
	defer res.Body.Close()
	var body map[string]any
	json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body)
	return res.StatusCode, body, nil
}

func tokensFrom(body map[string]any) (*Tokens, error) {
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	expires, _ := body["expires_in"].(float64)
	if access == "" { return nil, errors.New("no access token in the answer") }
	return &Tokens{Access: access, Refresh: refresh, ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second)}, nil
}

// ServiceToken is the service's own token, fetched again a minute before it expires
func (c *Client) ServiceToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.svcToken != "" && time.Now().Before(c.svcExpiry.Add(-time.Minute)) { return c.svcToken, nil }
	code, body, err := c.token(ctx, url.Values{"grant_type": {"client_credentials"}})
	if err != nil { return "", err }
	if code != http.StatusOK { return "", fmt.Errorf("service token: Oh CRUD answered %d %v", code, body["error"]) }
	t, err := tokensFrom(body)
	if err != nil { return "", err }
	c.svcToken, c.svcExpiry = t.Access, t.ExpiresAt
	return c.svcToken, nil
}

// forgetServiceToken drops the cached service token, after Oh CRUD refused it
func (c *Client) forgetServiceToken() {
	c.mu.Lock()
	c.svcToken = ""
	c.mu.Unlock()
}

// Login signs a person in with their password, and their one-time code if they have one
func (c *Client) Login(ctx context.Context, username, password, otp string) (*Tokens, error) {
	form := url.Values{"grant_type": {"password"}, "username": {username}, "password": {password}}
	if otp != "" { form.Set("otp", otp) }
	code, body, err := c.token(ctx, form)
	switch {
	case err != nil:
		return nil, err
	case code == http.StatusTooManyRequests:
		return nil, ErrSlowDown
	case code == http.StatusBadRequest:
		return nil, ErrBadLogin
	case code != http.StatusOK:
		return nil, fmt.Errorf("login: Oh CRUD answered %d %v", code, body["error"])
	}
	return tokensFrom(body)
}

// Refresh swaps a refresh token for new tokens. Refresh tokens are single use, so the
// caller must use the new one next time, and never refresh the same one twice.
func (c *Client) Refresh(ctx context.Context, refresh string) (*Tokens, error) {
	code, body, err := c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	switch {
	case err != nil:
		return nil, err
	case code == http.StatusBadRequest:
		return nil, ErrExpired
	case code != http.StatusOK:
		return nil, fmt.Errorf("refresh: Oh CRUD answered %d %v", code, body["error"])
	}
	return tokensFrom(body)
}

// Logout ends the person's session at Oh CRUD
func (c *Client) Logout(ctx context.Context, access string) error {
	res, err := c.Do(ctx, http.MethodPost, "/oauth/logout", nil, "", access)
	if err != nil { return err }
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("logout: Oh CRUD answered %d", res.StatusCode)
	}
	return nil
}

// Do calls Oh CRUD as the service, for the person whose access token is user
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, contentType string, user string) (*http.Response, error) {
	svc, err := c.ServiceToken(ctx)
	if err != nil { return nil, err }
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil { return nil, err }
	req.Header.Set("Authorization", "Bearer "+svc)
	if user != "" { req.Header.Set("X-OhCrud-User", user) }
	if contentType != "" { req.Header.Set("Content-Type", contentType) }
	return c.http.Do(req)
}

// Claims are what the dashboard shows of the signed-in person, read from their access
// token. Oh CRUD signed it and handed it straight to this server, so it isn't
// re-verified here; Oh CRUD checks it on every call.
type Claims struct {
	Subject  string   `json:"sub"`
	Username string   `json:"preferred_username"`
	Roles    []string `json:"roles"`
	AMR      []string `json:"amr"`
	AuthTime int64    `json:"auth_time"`
}

func readClaims(access string) Claims {
	var c Claims
	parts := strings.Split(access, ".")
	if len(parts) != 3 { return c }
	if b, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil { json.Unmarshal(b, &c) }
	return c
}
