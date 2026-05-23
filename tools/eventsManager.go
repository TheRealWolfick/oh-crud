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
	"lotusforge.au/api-server/models"
)

type EventHub struct {
	clients      map[*Client]client_topic
	targets      map[string]*Instructions
	mu           sync.RWMutex
	validTopics  map[string]bool
	sink         chan []byte
	ping_sec int
	timeout_sec int
	upgrader     websocket.Upgrader
	db           *pgxpool.Pool
}


type Instructions struct {
	webhook_url    webhook_action
	persist_to_db  bool
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
	timeout_write_sec int
	timeout_read_sec int
	done chan struct{}
}

// Defines a message from the client to alter its connection or request information
// Action - What needs to be done i.e "subscribe", "unsubscribe", "request"
// Direction - A pointer for the actions ""
type ClientMessage struct {
	Instruct string `json:"instruct"`
	Topic string `json:"topic"`
	Action string `json:"action"`
	Status string `json:"status"`
}

func NewEventHub(ping_sec int, timeout_sec int, db *pgxpool.Pool) *EventHub {
	return &EventHub{
		clients: map[*Client]client_topic{},
		targets: map[string]*Instructions{},
		ping_sec: ping_sec,
		timeout_sec: timeout_sec,
		validTopics: map[string]bool{},
		sink: make(chan []byte, 256),
		upgrader: websocket.Upgrader{
			ReadBufferSize: 1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		db: db,
	}
}

// Upgrade and manage a new websocket based on the topic and action. It registers for all statuses
func (h *EventHub) RegiterTopicAction(w http.ResponseWriter, r *http.Request, topic string, action []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	go h.handleClientTopicAction(conn, topic, action)
	
	return nil
}
// Upgrade and manage a new websocket based on the topic, status and action.
func (h *EventHub) RegiterTopicStatusAction(w http.ResponseWriter, r *http.Request, topic string, action []string, status []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	go h.handleClientTopicStatusAction(conn, topic, action, status)
	
	return nil
}
// Upgrade and manage a new websocket based on the topic and status, it registers for all actions
func (h *EventHub) RegiterTopicStatus(w http.ResponseWriter, r *http.Request, topic string, status []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	go h.handleClientTopicStatus(conn, topic, status)
	
	return nil
}
// Upgrade and manage a new websocket based on the topic. It registers for all actions and statuses
func (h *EventHub) RegiterTopicOnly(w http.ResponseWriter, r *http.Request, topic string) error {
	GetBasicDebugLogger().Debug("Upgrading conn")
	conn, err := h.upgrader.Upgrade(w, r, nil)
	GetBasicDebugLogger().Debug("upgraded conn", "topic", topic, "valid_topics", h.validTopics)
	if err != nil { return err }

	go h.HandleClientTopic(conn, topic)
	
	return nil
}

// Add a topic as a valid topic
func (h *EventHub) EnableTopic(topic string, cfg *models.DataModel) {
	h.validTopics[topic] = true

	// Register instructions for topics
	if h.targets[topic] == nil {
		h.targets[topic] = &Instructions{
			webhook_url: webhook_action{},
			persist_to_db: true,
			clients: instruct_action{},
		}
	}

	// Build structures
	for _, a := range []string{"insert", "update", "delete"} {
		// Action
		if h.targets[topic].webhook_url[a] == nil { h.targets[topic].webhook_url[a] = webhook_status{} }
		if h.targets[topic].clients[a] == nil { h.targets[topic].clients[a] = instruct_status{} }

		// Status
		for _, s := range []string{"queued", "start", "success", "warn", "fail", "error"} {
			if h.targets[topic].webhook_url[a][s] == nil { h.targets[topic].webhook_url[a][s] = []string{} }
			if h.targets[topic].clients[a][s] == nil { 
				h.targets[topic].clients[a][s] = instruct_clients{} 
			}
		}
	}

	// Populate webhook urls



}

// Register a new client connection to specific topic
func (h *EventHub) HandleClientTopic(conn *websocket.Conn, topic string) {
	if !h.validTopics[topic] { return }
	GetBasicDebugLogger().Debug("Topic is valid!")
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
		timeout_write_sec: h.timeout_sec,
		timeout_read_sec: int(float64(h.ping_sec) * 2.1),
		done: make(chan struct{}),
	}

	// Set the pong handler I guess
	conn.SetPongHandler(func(appData string) error {
      conn.SetReadDeadline(time.Now().Add(time.Duration(client.timeout_read_sec) * time.Second))
			GetBasicDebugLogger().Debug("Pong read")
      return nil
  })

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
	// Init clients[c]
	if h.clients[c] == nil {
		h.clients[c] = client_topic{}
	}
	// Init clients[c][topic]
	if h.clients[c][topic] == nil {
		h.clients[c][topic] = client_action{}
	}
	// Init clients[c][topic][action]
	if h.clients[c][topic][action] == nil {
		h.clients[c][topic][action] = client_status{}
	}
	h.clients[c][topic][action][status] = true

	// Init targets[topic].clients[action]
	if h.targets[topic].clients[action] == nil {
		h.targets[topic].clients[action] = instruct_status{}
	}
	// Init targets[topic].clients[action][status]
	if h.targets[topic].clients[action][status] == nil {
		h.targets[topic].clients[action][status] = map[*Client]bool{}
	}
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
			if !ok { GetBasicDebugLogger().Debug("Error sending message") }
			GetBasicDebugLogger().Debug("Sending message to websocket", "msg", msg)
			c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.timeout_write_sec) * time.Second))
			err = c.conn.WriteMessage(websocket.TextMessage, msg)
			if err != nil { GetBasicDebugLogger().Debug("Write error", "err", err) }
		case <- ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.timeout_write_sec) * time.Second))
			GetBasicDebugLogger().Debug("Sending ping")
			err = c.conn.WriteMessage(websocket.PingMessage, []byte{})
			if err != nil { GetBasicDebugLogger().Debug("Write error", "err", err) }
		case <- c.done:
			return
		}
		if err != nil { break }
	}
	ticker.Stop()
}

func (c *Client) readLoop() {
	GetBasicDebugLogger().Debug("started readloop")
	for {
		c.conn.SetReadDeadline(time.Now().Add(time.Duration(c.timeout_read_sec) * time.Second))
		_, msg, err := c.conn.ReadMessage()
		if err != nil { GetBasicDebugLogger().Debug("Error with read", "err", err); break }

		// Unmarshal the message
		var cm ClientMessage
		err = json.Unmarshal(msg, &cm)

		if err != nil { GetBasicDebugLogger().Debug("Error with decode", "err", err); continue }

		// msg actions currently unimplemented
		GetBasicDebugLogger().Debug("Received message from client", "msg", cm)
	}
	GetBasicDebugLogger().Debug("closed readloop")
	close(c.done)
}

// Broadcase a bytes array across all clients
func (h *EventHub) Broadcast(i *Instructions, action string, status string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	GetBasicDebugLogger().Debug("Broadcasting data to clients", 
	"data", data,
	"action", action,
	"status", status,
	"client_count", len(i.clients[action][status]),
	"all_clients", i.clients)

	for c, _ := range i.clients[action][status] {
		GetBasicDebugLogger().Debug("Found client", "c", c)
		c.send <- data
	}
}

// This is a function that handles webhooks. Currently, it only supports webhooks as per the config file,
// but maybe user / frontend defined in the future?
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
func (h *EventHub) PublishNoTimestamp(ctx context.Context, action string, status string, topic string, payload map[string]any) error {
	return h.Publish(ctx, action, status, time.Now(), topic, payload)
}
// Pushish a message without supplying a timestamp
func (h *EventHub) PublishNoTimestampPayload(ctx context.Context, action string, status string, topic string, payload []byte) error {
	return h.publish(ctx, action, status, time.Now(), topic, payload)
}
// Publish a message
func (h *EventHub) Publish(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload map[string]any) error {
	// Extract and prep data
	pl, err := json.Marshal(payload)
	if err != nil { return err }
	return h.publish(ctx, action, status, timestamp, topic, pl)
}
// Publish a message
func (h *EventHub) PublishPayload(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload []byte) error {
	// Extract and prep data
	return h.publish(ctx, action, status, timestamp, topic, payload)
}

// Publish a message across all end points as per the instructions.
func (h *EventHub) publish(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	GetBasicDebugLogger().Debug("Called the websocket publish")

	instructions := h.targets[topic]
	user, found := middleware.GetUser(ctx)
	if !found { return fmt.Errorf("Publish called from a non user!") }

	// Publish to the database
	if instructions.persist_to_db {
		h.db.Exec(ctx, "INSERT INTO events(action, status, topic, log, time, user) VALUES ($, $, $, $, $)", action, status, topic, payload, timestamp, user.Username)
	}

	// Publish to webhooks
	go h.Callback(topic, action, status, payload)

	// Publish to clients
	go h.Broadcast(instructions, action, status, payload)

	return nil
}
