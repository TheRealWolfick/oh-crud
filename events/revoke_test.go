package events

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/reqctx"
)

// connectWith opens a socket as the middleware would leave it: a principal holding
// the tokens, and the actor
func connectWith(t *testing.T, h *EventManager, topic string, p *auth.Principal) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithPrincipal(r.Context(), p)
		id := ""
		if p.User != nil { id = p.User.Subject } else { id = p.Service.Subject }
		ctx = reqctx.SetUserActor(ctx, &models.User{ID: id, Username: id})
		if err := h.RegisterTopicOnly(ctx, w, r, topic); err != nil {
			t.Errorf("RegisterTopicOnly: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func userClaims(user, sid string, iat time.Time) *models.Claims {
	return &models.Claims{TokenUse: "user", SID: sid, RegisteredClaims: jwt.RegisteredClaims{Subject: user, IssuedAt: jwt.NewNumericDate(iat)}}
}

func serviceClaims(svc string, iat time.Time) *models.Claims {
	return &models.Claims{TokenUse: "service", RegisteredClaims: jwt.RegisteredClaims{Subject: "svc:" + svc, IssuedAt: jwt.NewNumericDate(iat)}}
}

// closeCode reads until the socket closes and returns the close code, or -1 if it is
// still open after a short wait
func closeCode(conn *websocket.Conn, wait time.Duration) int {
	conn.SetReadDeadline(time.Now().Add(wait))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil { continue }
		var ce *websocket.CloseError
		if errors.As(err, &ce) { return ce.Code }
		if strings.Contains(err.Error(), "timeout") { return -1 }
		return 0
	}
}

func TestCloseRevokedClosesOnlyCoveredSockets(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	topic := models.TopicKey("inventory", "table:asset")
	h.EnableTopic(topic, nil, false, &models.DataModel{})

	issued := time.Now().Add(-time.Minute)
	web := serviceClaims("web", issued)
	revoked := connectWith(t, h, topic, &auth.Principal{Service: web, User: userClaims("u1", "fam1", issued)})
	sameUser := connectWith(t, h, topic, &auth.Principal{Service: web, User: userClaims("u1", "fam2", issued)})
	otherSvc := connectWith(t, h, topic, &auth.Principal{Service: serviceClaims("reports", issued)})
	waitFor(t, "three clients", func() bool { return clientCount(h) == 3 })

	if n := h.CloseRevoked("sid:fam1", time.Now()); n != 1 {
		t.Fatalf("expected one socket closed, got %d", n)
	}
	if code := closeCode(revoked, 2*time.Second); code != globals.CloseCodeRevoked {
		t.Errorf("expected close code %d, got %d", globals.CloseCodeRevoked, code)
	}
	waitFor(t, "the revoked client to be removed", func() bool { return clientCount(h) == 2 })

	// The user's other session and the other service are untouched
	for name, c := range map[string]*websocket.Conn{"same user, other session": sameUser, "other service": otherSvc} {
		if code := closeCode(c, 300*time.Millisecond); code != -1 {
			t.Errorf("%s: expected the socket to stay open, got close %d", name, code)
		}
	}

	// Revoking the user closes their remaining session too
	if n := h.CloseRevoked("user:u1", time.Now()); n != 1 {
		t.Errorf("expected the user's other socket closed, got %d", n)
	}
	waitFor(t, "only the other service left", func() bool { return clientCount(h) == 1 })
}

// A socket opened with a token issued after the revocation is a new login: left open
func TestCloseRevokedLeavesLaterTokens(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	topic := models.TopicKey("inventory", "table:asset")
	h.EnableTopic(topic, nil, false, &models.DataModel{})

	revokedAt := time.Now().Add(-time.Minute)
	conn := connectWith(t, h, topic, &auth.Principal{User: userClaims("u1", "fam9", time.Now())})
	waitFor(t, "the client", func() bool { return clientCount(h) == 1 })

	if n := h.CloseRevoked("user:u1", revokedAt); n != 0 {
		t.Errorf("expected nothing closed, got %d", n)
	}
	if code := closeCode(conn, 300*time.Millisecond); code != -1 {
		t.Errorf("expected the socket to stay open, got close %d", code)
	}
}

// Revoking a service closes the sockets it opened for its users as well as its own
func TestCloseRevokedService(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	topic := models.TopicKey("inventory", "table:asset")
	h.EnableTopic(topic, nil, false, &models.DataModel{})

	issued := time.Now().Add(-time.Minute)
	connectWith(t, h, topic, &auth.Principal{Service: serviceClaims("web", issued), User: userClaims("u1", "fam1", issued)})
	connectWith(t, h, topic, &auth.Principal{Service: serviceClaims("web", issued)})
	connectWith(t, h, topic, &auth.Principal{Service: serviceClaims("reports", issued)})
	waitFor(t, "three clients", func() bool { return clientCount(h) == 3 })

	if n := h.CloseRevoked("svc:web", time.Now()); n != 2 {
		t.Errorf("expected both of web's sockets closed, got %d", n)
	}
	waitFor(t, "only reports left", func() bool { return clientCount(h) == 1 })
}
