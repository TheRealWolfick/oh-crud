package events

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/reqctx"
)

// connectAs is connect with a user holding roles
func connectAs(t *testing.T, h *EventManager, topic string, roles []string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := reqctx.SetUserActor(r.Context(), &models.User{Username: "tester", Roles: roles})
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

// readUntil reads messages until one matches, failing after a short wait
func readUntil(t *testing.T, conn *websocket.Conn, match func(map[string]any) bool) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("no matching message: %v", err)
		}
		var msg map[string]any
		if json.Unmarshal(raw, &msg) == nil && match(msg) {
			return msg
		}
	}
}

func readable(service string) *models.DataModel {
	return &models.DataModel{
		Service:            &service,
		End_points_allowed: &models.End_pointsAllowed{GET: []string{"reader"}},
	}
}

// Two services can each own a topic with the same name, and a client picks one with "service"
func TestSubscribeUsesServiceAndTopic(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	h.EnableTopic(models.TopicKey("inventory", "table:asset"), nil, false, readable("inventory"))
	h.EnableTopic(models.TopicKey("permits", "table:asset"), nil, false, readable("permits"))

	conn := connectAs(t, h, models.TopicKey("inventory", "table:asset"), []string{"reader"})
	waitFor(t, "the client to register", func() bool { return clientCount(h) == 1 })

	sub := models.ClientMessage{Instruction: "sub", Service: "permits", Topic: "table:asset", Action: "update", Status: "success"}
	if err := conn.WriteJSON(sub); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	// The reply names the service and the topic separately
	reply := readUntil(t, conn, func(m map[string]any) bool { return m["subscribe"] == true && m["service"] == "permits" })
	if reply["topic"] != "table:asset" {
		t.Errorf("expected topic table:asset in the reply, got %v", reply)
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if !h.clients[c][models.TopicKey("permits", "table:asset")]["update"]["success"] {
			t.Errorf("expected a subscription to permits' table:asset")
		}
	}
}

// A topic named without its service matches nothing, rather than a topic in any service
func TestSubscribeWithoutServiceIsRefused(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	h.EnableTopic(models.TopicKey("inventory", "table:asset"), nil, false, readable("inventory"))
	h.EnableTopic(models.TopicKey("permits", "table:asset"), nil, false, readable("permits"))

	conn := connectAs(t, h, models.TopicKey("inventory", "table:asset"), []string{"reader"})
	waitFor(t, "the client to register", func() bool { return clientCount(h) == 1 })

	if err := conn.WriteJSON(models.ClientMessage{Instruction: "sub", Topic: "table:asset"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	// Unsubscribing from a topic in no service names it as invalid, with the service field present
	if err := conn.WriteJSON(models.ClientMessage{Instruction: "unsub", Topic: "table:asset"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	reply := readUntil(t, conn, func(m map[string]any) bool { return m["unsubscribe"] == false })
	if reply["reason"] != "invalid topic" || reply["service"] != "" || reply["topic"] != "table:asset" {
		t.Errorf("expected an invalid topic reply naming no service, got %v", reply)
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if _, ok := h.clients[c][models.TopicKey("permits", "table:asset")]; ok {
			t.Errorf("a sub without a service must not reach another service's topic")
		}
	}
}

func TestTopicKeyRoundTrip(t *testing.T) {
	cases := [][2]string{{"ohc", "table:user"}, {"inventory", "table:asset/data"}, {"inventory", "func:asset/data/by-building"}}
	for _, c := range cases {
		svc, topic := models.SplitTopicKey(models.TopicKey(c[0], c[1]))
		if svc != c[0] || topic != c[1] {
			t.Errorf("TopicKey(%q, %q) split back to %q, %q", c[0], c[1], svc, topic)
		}
	}
}
