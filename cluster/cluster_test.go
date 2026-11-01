package cluster

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var secret = []byte("0123456789abcdef0123456789abcdef")

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// peer is a bus behind a real HTTP server, recording what its handler receives
type peer struct {
	bus  *Bus
	srv  *httptest.Server
	mu   sync.Mutex
	got  []string
	hits atomic.Int32
}

func newPeer(t *testing.T, s []byte) *peer {
	p := &peer{bus: New(s, func() []string { return nil }, quiet())}
	p.bus.Handle("test.note", func(d json.RawMessage) error {
		p.mu.Lock(); defer p.mu.Unlock()
		p.got = append(p.got, string(d))
		return nil
	})
	inner := p.bus.Handler()
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *peer) received() []string { p.mu.Lock(); defer p.mu.Unlock(); return append([]string{}, p.got...) }

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() { return }
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPublishReachesEveryPeer(t *testing.T) {
	b1, b2 := newPeer(t, secret), newPeer(t, secret)
	sender := New(secret, func() []string { return []string{b1.srv.URL, b2.srv.URL + "/"} }, quiet())

	sender.Publish("test.note", map[string]string{"user": "u1"})

	for _, p := range []*peer{b1, b2} {
		waitUntil(t, "delivery", func() bool { return len(p.received()) == 1 })
		if p.received()[0] != `{"user":"u1"}` {
			t.Errorf("unexpected data %v", p.received())
		}
	}
}

// A message signed with a different secret (not one of ours) is refused
func TestWrongSecretRefused(t *testing.T) {
	p := newPeer(t, secret)
	stranger := New([]byte("ffffffffffffffffffffffffffffffff"), func() []string { return []string{p.srv.URL} }, quiet())
	stranger.Publish("test.note", "x")

	waitUntil(t, "the attempt", func() bool { return p.hits.Load() >= 1 })
	time.Sleep(50 * time.Millisecond)
	if len(p.received()) != 0 {
		t.Errorf("a message with a bad signature must not be handled")
	}
	if p.hits.Load() != 1 {
		t.Errorf("a refusal (401) must not be retried, got %d attempts", p.hits.Load())
	}
}

// post sends body to p signed by b, and returns the status
func post(t *testing.T, b *Bus, p *peer, body []byte, sig string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.srv.URL, bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sig)
	res, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	res.Body.Close()
	return res.StatusCode
}

func TestReceiverChecks(t *testing.T) {
	p := newPeer(t, secret)
	other := New(secret, nil, quiet())
	msg := func(m Message) []byte { b, _ := json.Marshal(m); return b }
	fresh := Message{Origin: other.ID(), Sent: time.Now(), Channel: "test.note", Data: json.RawMessage(`"hi"`)}

	cases := []struct {
		name   string
		body   []byte
		sig    func([]byte) string
		status int
	}{
		{"good", msg(fresh), other.sign, http.StatusNoContent},
		{"tampered", msg(fresh), func(b []byte) string { return other.sign(append(b, ' ')) }, http.StatusUnauthorized},
		{"no signature", msg(fresh), func([]byte) string { return "" }, http.StatusUnauthorized},
		{"stale", msg(Message{Origin: other.ID(), Sent: time.Now().Add(-2 * time.Minute), Channel: "test.note", Data: json.RawMessage(`1`)}), other.sign, http.StatusBadRequest},
		{"future", msg(Message{Origin: other.ID(), Sent: time.Now().Add(2 * time.Minute), Channel: "test.note", Data: json.RawMessage(`1`)}), other.sign, http.StatusBadRequest},
		{"unknown channel", msg(Message{Origin: other.ID(), Sent: time.Now(), Channel: "nope", Data: json.RawMessage(`1`)}), other.sign, http.StatusNotFound},
		{"own message", msg(Message{Origin: p.bus.ID(), Sent: time.Now(), Channel: "test.note", Data: json.RawMessage(`"mine"`)}), other.sign, http.StatusNoContent},
	}
	for _, tc := range cases {
		if got := post(t, other, p, tc.body, tc.sig(tc.body)); got != tc.status {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.status)
		}
	}
	// Only the good message reached the handler: not the stale ones, nor the instance's own
	if got := p.received(); len(got) != 1 || got[0] != `"hi"` {
		t.Errorf("expected only the good message handled, got %v", got)
	}
}

// A peer that fails (restarting, say) gets the message on a retry
func TestDeliveryRetries(t *testing.T) {
	p := newPeer(t, secret)
	var calls atomic.Int32
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 { http.Error(w, "down", http.StatusServiceUnavailable); return }
		p.bus.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(flaky.Close)

	sender := New(secret, func() []string { return []string{flaky.URL} }, quiet())
	sender.retry = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	sender.Publish("test.note", "late")
	waitUntil(t, "the third attempt to succeed", func() bool { return len(p.received()) == 1 })
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", calls.Load())
	}
}

// With no peers configured (a single instance), publishing does nothing
func TestNoPeers(t *testing.T) {
	New(secret, func() []string { return nil }, quiet()).Publish("test.note", "x")
}
