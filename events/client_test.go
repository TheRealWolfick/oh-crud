package events

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/reqctx"
)

// connect starts a server that hands each websocket to h on topic, and dials it
func connect(t *testing.T, h *EventManager, topic string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := reqctx.SetUserActor(r.Context(), &models.User{Username: "tester"})
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

func clientCount(h *EventManager) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() { return }
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A client that disconnects is removed, so events stop queuing for it
func TestClientRemovedOnDisconnect(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	h.EnableTopic("table:asset", nil, false, &models.DataModel{})

	conn := connect(t, h, "table:asset")
	waitFor(t, "the client to register", func() bool { return clientCount(h) == 1 })

	conn.Close()
	waitFor(t, "the client to be removed", func() bool { return clientCount(h) == 0 })
}

// A client whose buffer is full is closed instead of blocking the sender
func TestTrySendClosesClientThatIsNotReading(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	h.EnableTopic("table:asset", nil, false, &models.DataModel{})
	connect(t, h, "table:asset")
	waitFor(t, "the client to register", func() bool { return clientCount(h) == 1 })

	h.mu.RLock()
	var c *Client
	for client := range h.clients { c = client }
	h.mu.RUnlock()

	// Far more than the buffer holds. None of these may block.
	sent := make(chan struct{})
	go func() {
		for range cap(c.send) * 4 { c.trySend([]byte("x")) }
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("trySend blocked")
	}
}

// A socket upgraded for an invalid topic is closed rather than left open
func TestInvalidTopicClosesSocket(t *testing.T) {
	h := NewEventManager(1, 1, nil, nil)
	conn := connect(t, h, "table:missing")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected the server to close the socket, got %v", err)
	}
}
