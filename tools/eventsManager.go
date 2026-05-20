package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/middleware"
)

type EventHub struct {
	clients      map[*Client]client_topic
	targets      map[string]*Instructions
	mu           sync.RWMutex
	validTopics  map[string]bool
	sink         chan []byte
	ping_sec int
	timeout_sec int
}


type Instructions struct {
	webhook_url    webhook_action
	persist_to_db  bool
	db             *pgxpool.Pool
	clients        instruct_action
}

type client_topic map[string]client_action
type client_action map[string]client_status
type client_status map[string]bool

type instruct_action map[string]instruct_status
type instruct_status map[string]instruct_clients
type instruct_clients map[*Client]bool

type webhook_action map[string]webhook_status
type webhook_status map[string][]string

type Client struct {
	conn *websocket.Conn
	send chan []byte
	ping_sec int
	timout_sec int
	done chan struct{}
}

// Defines a message from the client to alter its connection or request information
// Action - What needs to be done i.e "subscribe", "unsubscribe", "request"
// Direction - A pointer for the actions ""
type ClientMessage struct {
	Action string `json:"action"`
	Direction string `json:"direction"`
}

func NewEventHub(ping_sec int, timeout_sec int) *EventHub {
	return &EventHub{
		clients: map[*Client]client_topic{},
		targets: map[string]*Instructions{},
		ping_sec: ping_sec,
		timeout_sec: timeout_sec,
		validTopics: map[string]bool{},
		sink: make(chan []byte, 256),
	}
}

// Add a topic as a valid topic
func (h *EventHub) EnableTopic(topic string) {
	h.validTopics[topic] = true
}

// Register a new client connection to specific topic
func (h *EventHub) HandleClientTopic(conn *websocket.Conn, topic string) {
	h.handleClient(conn, topic, []string{"insert", "update", "delete"}, []string{"queued", "start", "success", "warn", "fail", "error"})
}
// Register a new client connection to specific topic statuses
func (h *EventHub) handleClientTopicStatus(conn *websocket.Conn, topic string, status []string) {
	h.handleClient(conn, topic, []string{"insert", "update", "delete"}, status)
}
// Register a new client connection to specific topic actions
func (h *EventHub) handleClientTopicAction(conn *websocket.Conn, topic string, action []string) {
	h.handleClient(conn, topic, action, []string{"queued", "start", "success", "warn", "fail", "error"})
}
// Register a new client connection to specific topic actions and statuses. Statuses apply over all actions
func (h *EventHub) handleClientTopicStatusAction(conn *websocket.Conn, topic string, action []string, status []string) {
	h.handleClient(conn, topic, action, status)
}
// Register a new client connection. It is expected to call this via a go function
func (h *EventHub) handleClient(conn *websocket.Conn, topic string, action []string, status []string) {
	h.mu.Lock()
	// Create the client
	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
		ping_sec: h.ping_sec,
		timout_sec: h.timeout_sec,
	}

	// Register the client into the register first
	for _, a := range action {
		for _, s := range status {
			h.registerUnsafe(client, topic, a, s)
		}
	}
	h.mu.Unlock()

	// Write loop, handles sending data to the client, including ping-pong initiation. Called via go
	go client.writeLoop()

	// Read loop, handles reads from the client
	client.readLoop()

	// Unregister the client
	h.unhandleClient(client)
}

// Register the client in the eventhub safely
func (h *EventHub) register(c *Client, topic string, action string, status string) {
	h.mu.Lock()
	h.registerUnsafe(c, topic, action, status)
	h.mu.Unlock()
}
// Register the client in the eventhub UNSAFE
func (h *EventHub) registerUnsafe(c *Client, topic string, action string, status string) {
	h.clients[c][topic][action][status] = true
	h.targets[topic].clients[action][status][c] = true
}

// Register the client in the eventhub safely
func (h *EventHub) unregister(c *Client, topic string, action string, status string) {
	h.mu.Lock()
	h.unregisterUnsafe(c, topic, action, status)
	h.mu.Unlock()
}
// Register the client in the eventhub UNSAFE
func (h *EventHub) unregisterUnsafe(c *Client, topic string, action string, status string) {
	delete(h.targets[topic].clients[action][status], c)
}

// Unregister the client from the eventhub and clean up
func (h *EventHub) unhandleClient(client *Client) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Read through all the topics and their actions that the client is subscribed to
	for topicName, actions := range h.clients[client] {
		for actionName, statuses := range actions {
			for statusName, listening := range statuses {
				if listening {
					h.unregisterUnsafe(client, topicName, actionName, statusName)
				}
			}
		}
	}

	// Remove client for the clients list
	delete(h.clients, client)

	client.conn.Close()

	return nil
}

// This handles writing to the client 
func (c *Client) writeLoop() {
	// Ticker for sending a connection test
	ticker := time.NewTicker(time.Second * time.Duration(c.ping_sec))
	var err error

	for {
		// Write to the client
		select {
		case msg, ok := <- c.send:
			if !ok { return }
			c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.timout_sec)))
			err = c.conn.WriteMessage(websocket.TextMessage, msg)
		case <- ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.timout_sec)))
			err = c.conn.WriteMessage(websocket.PingMessage, []byte{})
		case <- c.done:
			return
		}
		if err != nil { break }
	}
}

func (c *Client) readLoop() {
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil { break }

		// Unmarshal the message
		var cm ClientMessage
		err = json.Unmarshal(msg, &cm)

		if err != nil { break }

		// msg currently unimplemented
		fmt.Print("Received message from client: ", cm)
	}
	close(c.done)
}

// Broadcase a bytes array across all clients
func (h *EventHub) Broadcast(i *Instructions, action string, status string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c, _ := range i.clients[action][status] {
		c.send <- data
	}
}

func (h *EventHub) Callback(topic string, action string, status string, data []byte) []error {
	var res *http.Response
	var err error
	var errs []error

	// Different reporting routes
	for _, url := range h.targets[topic].webhook_url[action][status] {
		res, err = http.Post(url, "application/json", bytes.NewReader(data))
		if err != nil            { errs = append(errs, fmt.Errorf("Error (webhook) with topic {%s}, url {%s}, error: {%s}", topic, url, err.Error())); continue }
		if res.StatusCode != 200 { errs = append(errs, fmt.Errorf("Error (webhook) with topic {%s}, url {%s}, error_status: {%s}", topic, url, res.Status)) }
	}

	if len(errs) > 0 { return errs }
	return nil
}

// Pushish a message without supplying a timestamp
func (h *EventHub) PublishNoTimestamp(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload map[string]any) error {
	return h.Publish(ctx, action, status, time.Now(), topic, payload)
}
// Publish a message across all end points as per the instructions.
func (h *EventHub) Publish(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload map[string]any) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Extract and prep data
	pl, err := json.Marshal(payload)
	if err != nil { return err }
	user, found := middleware.GetUser(ctx)
	if !found { return fmt.Errorf("Publish called from a non user!") }
	instructions := h.targets[topic]

	// Publish to the database
	if instructions.persist_to_db {
		go instructions.db.Exec(ctx, "INSERT INTO events(action, status, topic, log, time, user) VALUES ($, $, $, $, $)", action, status, topic, pl, timestamp, user.Username)
	}

	// Publish to webhooks
	go h.Callback(topic, action, status, pl)

	// Publish to clients
	go h.Broadcast(instructions, action, status, pl)

	return nil
}
