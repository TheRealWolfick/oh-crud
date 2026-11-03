// Package cluster lets Oh CRUD instances tell each other things directly, instead of
// each one asking the database whether anything changed. An instance publishes a
// message on a named channel ("auth.revocation"), it is posted to every peer, and each
// peer hands it to whatever handles that channel.
//
// Peers are listed in server.yaml for now (cluster.peers); discovering them is a later
// step. Messages are signed with a key derived from OH_CRUD_SECRET, which every
// instance already shares to decrypt the signing keys, so only another Oh CRUD
// instance can send one, and the signature covers the whole message.
//
// Delivery is best effort with retries: a peer that stays unreachable misses the
// message, and learns the state again from the database when it next starts. Every
// handler must be idempotent, as a retry can deliver a message twice.
package cluster

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/secrets"
)

// retryDelays are the waits before each retry of a failed send
var retryDelays = []time.Duration{time.Second, 3 * time.Second}

// Message is what travels between instances
type Message struct {
	Origin  string          `json:"origin"`  // the sending instance, so it ignores its own messages
	Sent    time.Time       `json:"sent"`
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

// Bus sends and receives messages for one instance
type Bus struct {
	id     string
	key    []byte
	peers  func() []string // base URLs, read on every publish so server.yaml changes apply
	client *http.Client
	lg     *slog.Logger
	retry  []time.Duration // waits before each retry; retryDelays unless a test sets them

	mu       sync.RWMutex
	handlers map[string]func(json.RawMessage) error
}

// New makes the bus for this instance. secret is OH_CRUD_SECRET.
func New(secret []byte, peers func() []string, lg *slog.Logger) *Bus {
	id, _ := secrets.RandomString(16)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("oh-crud peer messages v1"))
	return &Bus{
		id: id, key: mac.Sum(nil), peers: peers, lg: lg,
		client:   &http.Client{Timeout: globals.ClusterSendTimeout},
		retry:    retryDelays,
		handlers: map[string]func(json.RawMessage) error{},
	}
}

// ID is this instance's id for the life of the process
func (b *Bus) ID() string { return b.id }

// Handle sets the function for messages on channel. It must be idempotent.
func (b *Bus) Handle(channel string, fn func(data json.RawMessage) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[channel] = fn
}

// Publish sends data on channel to every peer, in the background with retries. It
// never blocks the caller, and never delivers to this instance: apply it here first.
func (b *Bus) Publish(channel string, data any) {
	raw, err := json.Marshal(data)
	if err != nil { b.lg.Error("cluster message not sent: data is not JSON", "channel", channel, "error", err); return }
	peers := b.peers()
	if len(peers) == 0 { return }

	body, _ := json.Marshal(Message{Origin: b.id, Sent: time.Now().UTC(), Channel: channel, Data: raw})
	sig := b.sign(body)
	for _, peer := range peers {
		go b.deliver(strings.TrimRight(peer, "/")+globals.ClusterPath, body, sig, channel)
	}
}

func (b *Bus) deliver(url string, body []byte, sig string, channel string) {
	var err error
	for attempt := 0; ; attempt++ {
		if err = b.post(url, body, sig); err == nil { return }
		if attempt >= len(b.retry) { break }
		time.Sleep(b.retry[attempt])
	}
	b.lg.Error("cluster message not delivered", "peer", url, "channel", channel, "error", err)
}

func (b *Bus) post(url string, body []byte, sig string) error {
	ctx, cancel := context.WithTimeout(context.Background(), globals.ClusterSendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil { return err }
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(globals.ClusterSignatureHeader, sig)
	res, err := b.client.Do(req)
	if err != nil { return err }
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	// 4xx means the peer understood and refused: retrying the same message won't help
	if res.StatusCode >= 400 && res.StatusCode < 500 { return fmt.Errorf("refused: %s", res.Status) }
	if res.StatusCode >= 300 { return fmt.Errorf("peer answered %s", res.Status) }
	return nil
}

func (b *Bus) sign(body []byte) string {
	mac := hmac.New(sha256.New, b.key)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Receiver errors, by the status they answer with
var (
	errBadSignature = errors.New("bad signature")
	errStale        = errors.New("message too old or from the future")
	errUnknown      = errors.New("no handler for channel")
)

// Handler receives messages from peers: 204 when handled (or sent by this instance),
// 401 for a bad signature, 400 for a stale or malformed message, 404 for an unknown
// channel, 500 when the handler failed (the sender retries)
func (b *Bus) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, globals.ClusterMaxBody+1))
		if err != nil || len(body) > globals.ClusterMaxBody { http.Error(w, "bad request", http.StatusBadRequest); return }

		m, err := b.open(body, r.Header.Get(globals.ClusterSignatureHeader), time.Now())
		switch {
		case errors.Is(err, errBadSignature):
			b.lg.Warn("cluster message with a bad signature refused", "ip", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		case err != nil:
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		case m.Origin == b.id:
			w.WriteHeader(http.StatusNoContent) // this instance's own message, listed as a peer
			return
		}

		b.mu.RLock()
		fn := b.handlers[m.Channel]
		b.mu.RUnlock()
		if fn == nil { http.Error(w, errUnknown.Error(), http.StatusNotFound); return }
		if err := fn(m.Data); err != nil {
			b.lg.Error("cluster message handler failed", "channel", m.Channel, "from", m.Origin, "error", err)
			http.Error(w, "handler failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// open checks the signature, then the message's age
func (b *Bus) open(body []byte, sig string, now time.Time) (*Message, error) {
	want, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(want, mustHex(b.sign(body))) { return nil, errBadSignature }
	var m Message
	if err := json.Unmarshal(body, &m); err != nil || m.Channel == "" || m.Origin == "" { return nil, errors.New("malformed message") }
	if d := now.Sub(m.Sent); d > globals.ClusterMaxAge || d < -globals.ClusterMaxAge { return nil, errStale }
	return &m, nil
}

func mustHex(s string) []byte { b, _ := hex.DecodeString(s); return b }
