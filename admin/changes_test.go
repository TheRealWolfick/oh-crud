package admin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/models/modelstest"
)

func TestKeyInputDecode(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	for name, enc := range map[string]*base64.Encoding{"raw url": base64.RawURLEncoding, "url": base64.URLEncoding, "std": base64.StdEncoding, "raw std": base64.RawStdEncoding} {
		got, err := KeyInput{KID: "web-1", PublicKey: enc.EncodeToString(pub)}.decode()
		if err != nil || !reflect.DeepEqual(got, []byte(pub)) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, k := range map[string]KeyInput{
		"short key":   {KID: "k", PublicKey: base64.StdEncoding.EncodeToString(pub[:31])},
		"not base64":  {KID: "k", PublicKey: "!!!"},
		"bad kid":     {KID: "has space", PublicKey: base64.StdEncoding.EncodeToString(pub)},
		"long kid":    {KID: strings.Repeat("k", 33), PublicKey: base64.StdEncoding.EncodeToString(pub)},
	} {
		if _, err := k.decode(); err == nil {
			t.Errorf("%s: expected refused", name)
		}
	}
}

func TestCleanRoles(t *testing.T) {
	got, err := cleanRoles([]string{" user ", "inventory:editor", "user", "administrator"})
	if err != nil || !reflect.DeepEqual(got, []string{"administrator", "inventory:editor", "user"}) {
		t.Errorf("got %v %v", got, err)
	}
	if got, _ := cleanRoles(nil); got == nil {
		t.Errorf("no roles is an empty list, not null")
	}
	for _, bad := range []string{"", "has space", ":admin", strings.Repeat("r", 65)} {
		if _, err := cleanRoles([]string{bad}); err == nil {
			t.Errorf("%q: expected refused", bad)
		}
	}
}

func TestRoleService(t *testing.T) {
	for in, want := range map[string]string{
		"inventory:editor":                  "inventory",
		"inventory:editor,inventory:admin":  "inventory",
		"inventory:editor,permits:editor":   "ohc",
		"user":                              "ohc",
		"inventory:editor,user":             "ohc",
	} {
		if got := roleService(strings.Split(in, ",")); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func asUser(r *http.Request, c *models.Claims) *http.Request {
	return r.WithContext(auth.ContextWithPrincipal(r.Context(), &auth.Principal{User: c}))
}

func TestCanGrant(t *testing.T) {
	a := testAPI(&modelstest.FakeDB{})
	r := httptest.NewRequest("PUT", "/", nil)

	platform := asUser(r, user("administrator"))
	for _, role := range []string{"user", "inventory:editor", "permits:admin", "administrator"} {
		if !a.canGrant(platform, role) {
			t.Errorf("a platform admin may grant %q", role)
		}
	}
	svcAdmin := asUser(r, user("inventory:admin"))
	for role, want := range map[string]bool{"inventory:editor": true, "inventory:admin": true, "permits:editor": false, "user": false, "administrator": false, ":editor": false} {
		if got := a.canGrant(svcAdmin, role); got != want {
			t.Errorf("inventory's admin granting %q: got %v", role, got)
		}
	}
	if !a.isAdminRole("administrator") || !a.isAdminRole("inventory:admin") || a.isAdminRole("inventory:editor") || a.isAdminRole(":admin") {
		t.Errorf("isAdminRole")
	}
}

func TestStepUp(t *testing.T) {
	a := testAPI(&modelstest.FakeDB{})
	ran := false
	h := a.changes(func(w http.ResponseWriter, r *http.Request) { ran = true })
	call := func(c *models.Claims) *httptest.ResponseRecorder {
		ran = false
		w := httptest.NewRecorder()
		h(w, asUser(httptest.NewRequest("POST", "/", nil), c))
		return w
	}
	recent := jwt.NewNumericDate(time.Now().Add(-2 * time.Minute))
	stale := jwt.NewNumericDate(time.Now().Add(-20 * time.Minute))
	noOTP := &models.Claims{AMR: []string{"pwd"}, AuthTime: recent}

	// Off by default: anyone the route admits may change things
	if call(noOTP); !ran {
		t.Fatalf("with step-up off, the change runs")
	}

	a.d.Config.Get().Admin.Step_up_minutes = 10
	for name, c := range map[string]*models.Claims{
		"no otp":       noOTP,
		"stale otp":    {AMR: []string{"pwd", "otp"}, AuthTime: stale},
		"no auth_time": {AMR: []string{"pwd", "otp"}},
	} {
		w := call(c)
		if ran || w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), `insufficient_user_authentication", max_age=600`) {
			t.Errorf("%s: expected 401 asking for a recent second factor, got %d %q", name, w.Code, w.Header().Get("WWW-Authenticate"))
		}
	}
	if call(&models.Claims{AMR: []string{"pwd", "otp"}, AuthTime: recent}); !ran {
		t.Errorf("a recent OTP login passes")
	}
}
