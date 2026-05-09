package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lotusforge.au/api-server/models"
)

func makeServerConf(origins []string, headers []string) *models.SwappableServerConfig {
	allowCreds := true
	return models.NewSwappableServerConfig(&models.ServerConfig{
		CORS: &models.CorsConfig{
			Allowed_Origins:   origins,
			Allowed_Headers:   headers,
			Allow_Credentials: &allowCreds,
		},
	})
}

func makeDataModel() *models.DataModel {
	name := "test"
	endpoint := "test"
	version := "1.0.0"
	return &models.DataModel{
		Name:     &name,
		End_point: &endpoint,
		Version:  &version,
		End_points_allowed: &models.End_pointsAllowed{
			GET:    []string{},
			POST:   []string{},
			PUT:    []string{},
			DELETE: []string{},
		},
	}
}

func TestCors_AllowedOrigin(t *testing.T) {
	origin := "http://192.168.2.93:3000"
	cfg := makeDataModel()
	serverConf := makeServerConf([]string{origin}, []string{"Content-Type", "X-API-Key"})

	handler := Cors(cfg, serverConf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", origin)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", origin, got)
	}
}

func TestCors_DisallowedOrigin(t *testing.T) {
	cfg := makeDataModel()
	serverConf := makeServerConf([]string{"http://allowed.example.com"}, []string{"Content-Type"})

	handler := Cors(cfg, serverConf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no Access-Control-Allow-Origin header, got %q", got)
	}
}

// TestCors_PreflightNoAuth verifies that a preflight OPTIONS request without auth
// headers still receives a 200 with the CORS headers — the fix for the original bug
// where the OPTIONS handler was wrapped in auth, causing preflight to fail.
func TestCors_PreflightNoAuth(t *testing.T) {
	origin := "http://192.168.2.93:3000"
	cfg := makeDataModel()
	serverConf := makeServerConf([]string{origin}, []string{"Content-Type", "X-API-Key"})

	// emptyResponse mirrors the actual handler used for OPTIONS — just 200, no auth.
	preflightHandler := Cors(cfg, serverConf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	rr := httptest.NewRecorder()
	preflightHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected preflight status 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", origin, got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Access-Control-Allow-Methods to be set")
	}
}

// TestCors_PreflightWithAuthBlocks shows what the old behaviour was: OPTIONS through
// auth returns 401 even for a legitimate origin.
func TestCors_PreflightWithAuthBlocks(t *testing.T) {
	origin := "http://192.168.2.93:3000"
	cfg := makeDataModel()
	serverConf := makeServerConf([]string{origin}, []string{"Content-Type", "X-API-Key"})

	// Simulate the old broken handler: CORS wrapping a fake auth that always rejects.
	alwaysRejectAuth := func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
	brokenPreflight := Cors(cfg, serverConf)(alwaysRejectAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", origin)
	rr := httptest.NewRecorder()
	brokenPreflight.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected old broken behaviour to return 401, got %d", rr.Code)
	}
}
