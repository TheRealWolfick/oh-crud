package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"lotusforge.au/api-server/auth"
	"lotusforge.au/api-server/logging"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/reqctx"
	"lotusforge.au/api-server/safe"
	"lotusforge.au/api-server/tools"
)

// -----------------------------------------------------------------
//            Package variables
// -----------------------------------------------------------------

var ValidActions  = []string{"get", "create", "update", "delete", "any"}
var ValidStatuses = []string{"queued", "start", "success", "warn", "failed", "error"}

// -----------------------------------------------------------------
//            Package types and type creations
// -----------------------------------------------------------------

type EventManager struct {
	clients      map[*Client]client_topic
	targets      map[string]*Instructions
	mu           sync.RWMutex
	validTopics  map[string]bool
	sink         chan []byte
	ping_sec int
	timeout_sec int
	upgrader     websocket.Upgrader
	db           models.DBExecQuery
	auth         *auth.Auth
}


type Instructions struct {
	webhook_url    webhook_action
	persist_to_db  bool
	clients        instruct_action
	functions      function_references
	is_func        bool
	config         *models.DataModel
	permission     string             // The model permission a client needs to subscribe
}

type client_topic map[string]client_action
type client_action map[string]client_status
type client_status map[string]bool

type instruct_action map[string]instruct_status
type instruct_status map[string]instruct_clients
type instruct_clients map[*Client]bool

type webhook_action map[string]webhook_status
type webhook_status map[string][]string

type function_references []string

type Client struct {
	conn *websocket.Conn
	send chan []byte
	ping_sec int
	timeout_write_sec int
	timeout_read_sec int
	actor *models.User
	tokens []*models.Claims // the tokens the socket was opened with, so a revocation can find it
	done chan struct{}
	closeOnce sync.Once
}

// CloseCodeRevoked is the websocket close code sent when the token a socket was opened
// with is revoked. It mirrors HTTP 401: reconnecting with the same token is refused.
const CloseCodeRevoked = 4401

// tokensFrom are the tokens on the upgrade request: the user's, and the service's when
// a service called on the user's behalf
func tokensFrom(ctx context.Context) []*models.Claims {
	p := auth.PrincipalFrom(ctx)
	if p == nil { return nil }
	var out []*models.Claims
	if p.User != nil { out = append(out, p.User) }
	if p.Service != nil { out = append(out, p.Service) }
	return out
}

// CloseRevoked closes every socket opened with a token the revocation covers. It is
// registered with the auth blocklist (BlockList.OnRevoke), so it runs once for each
// revocation, as it is made on this instance or as a reload finds it from another,
// instead of every socket checking the blocklist. It returns how many it closed.
func (h *EventManager) CloseRevoked(key string, at time.Time) int {
	h.mu.RLock()
	var hit []*Client
	for c := range h.clients {
		for _, t := range c.tokens {
			if auth.RevocationCovers(key, at, t) { hit = append(hit, c); break }
		}
	}
	h.mu.RUnlock()

	// Closing writes to the socket, so it happens outside the lock and without
	// holding up the revoking request
	for _, c := range hit {
		safe.Go(logging.Basic(), "websocket revoke", func() { c.closeWith(CloseCodeRevoked, "token revoked") })
	}
	if len(hit) > 0 {
		logging.Basic().Info("Closed websockets for a revocation", "key", key, "sockets", len(hit))
	}
	return len(hit)
}

// closeWith tells the client why, then closes. WriteControl may run alongside the
// write loop, and a client that doesn't read the frame in time is closed anyway.
func (c *Client) closeWith(code int, reason string) {
	c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	c.close()
}

// webhookClient posts webhooks. The timeout stops a slow receiver from holding a
// goroutine open forever.
var webhookClient = &http.Client{Timeout: 10 * time.Second}

// Stop the client's write loop and close its connection, which also ends its read loop.
// Safe to call more than once and from any goroutine.
func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// Queue a message for the client without blocking. A client whose buffer is full is not
// reading, so it is closed instead of stalling every publish behind it.
func (c *Client) trySend(msg []byte) {
	select {
	case c.send <- msg:
	default:
		logging.Basic().Warn("Closing websocket client that is not reading its messages")
		c.close()
	}
}

// Returns a new event manager. It will build a registry of all websockets upon registering
// endpoints via the h.EnableTopic function. It will also upgrade any connections for websocks
// and manage them throughout their lifecycle.
// 
// Any events passed published to the event manager will be distributed to all registered
// webhooks and websocks at the time of the event.
//
// Currently, every event is set to persist to the db
//
// Events are categorized by action (insert, update etc) and status (start, success, fail etc).
// An event must have a valid action and status to be published
func NewEventManager(ping_sec int, timeout_sec int, db models.DBExecQuery, auth *auth.Auth) *EventManager {
	return &EventManager{
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
		auth: auth,
	}
}

func (h *EventManager) ValidTopics() map[string]bool { return h.validTopics }
func (h *EventManager) CurrentTargets() map[string]*Instructions { return h.targets }

// -----------------------------------------------------------------
//            Topic Registration Management
// -----------------------------------------------------------------

// This function is used for registering a function. It will enable the function as a topic and
// also reference it in the table topic. Both are topic keys (models.TopicKey). If the table is
// not a valid topic, which should never happen, the function will not register and will log an error.
func (h *EventManager) RegisterFunction(table_topic string, topic string, hooks *models.EventAction, cfg *models.DataModel) {
	h.mu.Lock()
	defer h.mu.Unlock()

	_, valid := h.validTopics[table_topic]
	if !valid {
		logging.BasicDebug().Error(fmt.Sprintf(`Attempted to register the function topic {%s} against
		table {%s}, but the table is an invalid topic`, topic, table_topic))
		return
	}

	h.enableTopicUnsafe(topic, hooks, false, cfg)
	h.targets[topic].is_func = true
	h.targets[table_topic].functions = append(h.targets[table_topic].functions, topic)
}

// Add a topic as a valid topic
func (h *EventManager) EnableTopic(topic string, hooks *models.EventAction, persist_to_db bool, cfg *models.DataModel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enableTopicUnsafe(topic, hooks, persist_to_db, cfg)
}
// Add a topic for a model's diffs. Diff events persist to the db, and subscribing needs
// the model's DIFF role, the same as reading its diffs. The model's webhooks are for
// table changes, so none are attached.
func (h *EventManager) EnableDiffTopic(topic string, cfg *models.DataModel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enableTopicUnsafe(topic, nil, true, cfg)
	h.targets[topic].permission = "diff"
}
// Add a topic as a valid topic UNSAFE
func (h *EventManager) enableTopicUnsafe(topic string, hooks *models.EventAction, persist_to_db bool, cfg *models.DataModel) {
	h.validTopics[topic] = true

	// Register instructions for topics
	h.targets[topic] = &Instructions{
		webhook_url: webhook_action{},
		persist_to_db: persist_to_db,
		clients: instruct_action{},
		functions: function_references{},
		is_func: false,
		config: cfg,
		permission: "get",
	}

	i := h.targets[topic]

	// Build structures
	for _, a := range ValidActions {
		// Action
		if i.webhook_url[a] == nil { i.webhook_url[a] = webhook_status{} }
		if i.clients[a] == nil { i.clients[a] = instruct_status{} }

		// Status
		for _, s := range ValidStatuses {
			if i.webhook_url[a][s] == nil { i.webhook_url[a][s] = []string{} }
			if i.clients[a][s] == nil { 
				i.clients[a][s] = instruct_clients{} 
			}
		}
	}

	// Helper func for mapping status from config to eventhub
	fn := func(action string, hooks *models.EventStatus) {
		for k, v := range tools.GetStructAsDict(hooks) {
			i.webhook_url[action][strings.ToLower(k)] = v.([]string)
		}
	}

	// Map any config webhooks into the data structure
	if hooks != nil {
		if hooks.On_get != nil { fn("get", hooks.On_get) }
		if hooks.On_delete != nil { fn("delete", hooks.On_delete) }
		if hooks.On_insert != nil { fn("create", hooks.On_insert) }
		if hooks.On_update != nil { fn("update", hooks.On_update) }
		if hooks.On_any != nil { fn("any", hooks.On_any) }
	}
}

// -----------------------------------------------------------------
//              Registering / Upgrading Connections
// -----------------------------------------------------------------

// Upgrade and manage a new websocket based on the topic and action. 
// Websock registers for all statuses of the passed in topic
func (h *EventManager) RegisterTopicAction(ctx context.Context, w http.ResponseWriter, r *http.Request, topic string, action []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	if actor, ok := reqctx.GetActor(ctx); ok { go h.handleClientTopicAction(conn, actor, tokensFrom(ctx), topic, action) } else { conn.Close() }

	return nil
}
// Upgrade and manage a new websocket based on the topic.
// Websock register for the specified statuses and actions.
func (h *EventManager) RegisterTopicStatusAction(ctx context.Context, w http.ResponseWriter, r *http.Request, topic string, action []string, status []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	if actor, ok := reqctx.GetActor(ctx); ok { go h.handleClientTopicStatusAction(conn, actor, tokensFrom(ctx), topic, action, status) } else { conn.Close() }

	return nil
}
// Upgrade and manage a new websocket based on the topic and status, it registers for all actions
func (h *EventManager) RegisterTopicStatus(ctx context.Context, w http.ResponseWriter, r *http.Request, topic string, status []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	if actor, ok := reqctx.GetActor(ctx); ok { go h.handleClientTopicStatus(conn, actor, tokensFrom(ctx), topic, status) } else { conn.Close() }

	return nil
}
// Upgrade and manage a new websocket based on the topic. It registers for all actions and statuses
func (h *EventManager) RegisterTopicOnly(ctx context.Context, w http.ResponseWriter, r *http.Request, topic string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }
	if actor, ok := reqctx.GetActor(ctx); ok { go h.HandleClientTopic(conn, actor, tokensFrom(ctx), topic) } else { conn.Close() }

	return nil
}

// -----------------------------------------------------------------
//              Client Management
// -----------------------------------------------------------------

// Register a new client connection to specific topic
func (h *EventManager) HandleClientTopic(conn *websocket.Conn, actor *models.User, tokens []*models.Claims, topic string) {
	h.mu.RLock()
	valid := h.validTopics[topic]
	h.mu.RUnlock()
	if !valid { conn.Close(); return }
	h.handleClient(conn, actor, tokens, topic, ValidActions, ValidStatuses)
}
// Register a new client connection to specific topic statuses
func (h *EventManager) handleClientTopicStatus(conn *websocket.Conn, actor *models.User, tokens []*models.Claims, topic string, status []string) {
	h.handleClient(conn, actor, tokens, topic, ValidActions, status)
}
// Register a new client connection to specific topic actions
func (h *EventManager) handleClientTopicAction(conn *websocket.Conn, actor *models.User, tokens []*models.Claims, topic string, action []string) {
	h.handleClient(conn, actor, tokens, topic, action, ValidStatuses)
}
// Register a new client connection to specific topic actions and statuses. Statuses apply over all actions
func (h *EventManager) handleClientTopicStatusAction(conn *websocket.Conn, actor *models.User, tokens []*models.Claims, topic string, action []string, status []string) {
	h.handleClient(conn, actor, tokens, topic, action, status)
}
// Register a new client connection. It is expected to call this via a go function
func (h *EventManager) handleClient(conn *websocket.Conn, actor *models.User, tokens []*models.Claims, topic string, action []string, status []string) {
	// Create the client
	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
		ping_sec: h.ping_sec,
		timeout_write_sec: h.timeout_sec,
		timeout_read_sec: int(float64(h.ping_sec) * 2.1),
		done: make(chan struct{}),
		actor: actor,
		tokens: tokens,
	}

	// However the client ends, even in a panic, remove it so events stop queuing for it.
	// Deferred calls run last first, so the panic is recovered before the clean up.
	defer h.unhandleClient(client)
	defer safe.Recover(logging.Basic(), "websocket client", nil)

	// Set the pong handler I guess
	conn.SetPongHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(time.Duration(client.timeout_read_sec) * time.Second))
		return nil
	})

	// Register the client into the register first
	h.registerClient(client, topic, action, status)

	// Write loop, handles sending data to the client, including ping-pong initiation
	safe.Go(logging.Basic(), "websocket write", client.writeLoop)

	// Read loop, handles reads from the client
	h.readLoop(client)
}

// Register a new client against each action and status of its topic
func (h *EventManager) registerClient(c *Client, topic string, action []string, status []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, a := range action {
		for _, s := range status {
			h.registerUnsafe(c, topic, a, s)
		}
	}
}

// Register the client in the eventhub UNSAFE
func (h *EventManager) registerUnsafe(c *Client, topic string, action string, status string) {
	// Ensure topic, action and status are valid
	_, valid_topic := h.targets[topic]
	valid_action := action == "" || action == "any" || action == "all" || slices.Contains(ValidActions, action)
	valid_status := status == "" || status == "any" || status == "all" || slices.Contains(ValidStatuses, status)

	if valid_topic == false || valid_action == false || valid_status == false {
		c.NotifyJson(map[string]any{"subscribe": false, "service": serviceOf(topic), "topic": topicOf(topic), "reason": "invalid subscription request", "is_topic_valid": valid_topic, "is_action_valid": valid_action, "is_status_valid": valid_status})
		return
	}

	// clean all statuses
	if action == "" || action == "any" { action = "all" }
	if status == "" || status == "any" { status = "all" }

	// Init clients[c]
	if h.clients[c] == nil {
		h.clients[c] = client_topic{}
	}
	// Init clients[c][topic]
	if h.clients[c][topic] == nil {
		h.clients[c][topic] = client_action{}
	}
	// Init clients[c][topic][action]
	if action == "all" {
		// Process all actions
		for _, action_reg := range ValidActions {
			// Process all statuses
			for _, status_reg := range ValidStatuses {
				if h.clients[c][topic][action_reg] == nil {
					h.clients[c][topic][action_reg] = client_status{}
				}
				h.clients[c][topic][action_reg][status_reg] = true
				h.targets[topic].clients[action_reg][status_reg][c] = true
			}
		}
	} else {
		// Process specific action
		if h.clients[c][topic][action] == nil {
			h.clients[c][topic][action] = client_status{}
		}
		if status == "all" {
			// Process all statuses
			for _, status_reg := range ValidStatuses {
				h.clients[c][topic][action][status_reg] = true
				h.targets[topic].clients[action][status_reg][c] = true
			}
		} else {
			// Process specific action
			h.clients[c][topic][action][status] = true
			h.targets[topic].clients[action][status][c] = true
		}
	}

	c.NotifyJson(map[string]any{"subscribe": true, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "status": status})
}

// Register the client in the eventhub safely
func (h *EventManager) unregister(c *Client, topic string, action string, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unregisterUnsafe(c, topic, action, status)
}
// Register the client in the eventhub UNSAFE
func (h *EventManager) unregisterUnsafe(c *Client, topic string, action string, status string) {
	// Process a topic as valid and can be unsubscribed from
	_, ok := h.targets[topic]; if !ok { 
		c.NotifyJson(map[string]any{"unsubscribe": false, "service": serviceOf(topic), "topic": topicOf(topic), "reason": "invalid topic"})
		return 
	}
	_, ok = h.clients[c][topic]; if !ok { 
		c.NotifyJson(map[string]any{"unsubscribe": false, "service": serviceOf(topic), "topic": topicOf(topic), "reason": "you are not subscribed to this topic"})
		return 
	}

	// --------------------------------
	// Process topic
	// --------------------------------
	if action == "" || action == "any" || action == "all" {
		// --------------------------------
		// Process all actions
		// --------------------------------
		if status == "" || status == "any" || status == "all" {
			// ----------------------------------
			// Process all statuses
			// ----------------------------------
			for action_reg := range h.clients[c][topic] {
				for status_reg := range h.clients[c][topic][action_reg] {
					// Delete the client from the instructions register
					delete(h.targets[topic].clients[action_reg][status_reg], c)
				}
			}
			// Delete the topic from the client data (no actions remaining)
			delete(h.clients[c], topic)

			c.NotifyJson(map[string]any{"unsubscribe": true, "service": serviceOf(topic), "topic": topicOf(topic)})
			return
		} else {
			// ----------------------------------
			// Process a specific status
			// ----------------------------------
			for action_reg := range h.clients[c][topic] {
				// Ensure the status is valid and can be unsubbed from
				_, ok = h.clients[c][topic][action_reg][status]; if !ok { continue }
				delete(h.targets[topic].clients[action_reg][status], c)
				delete(h.clients[c][topic], action_reg)
				c.NotifyJson(map[string]any{"unsubscribe": true, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "status": status})
			}
			return
		}
	} else {
		// --------------------------------
		// Process a specific action
		// --------------------------------
		// Validate the action
		if !slices.Contains(ValidActions, action) {
			c.NotifyJson(map[string]any{"unsubscribe": false, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "reason": "invalid action"})
			return 
		}
		_, ok := h.clients[c][topic][action]; if !ok {
			c.NotifyJson(map[string]any{"unsubscribe": false, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "reason": "you are not subscribed to this action"})
			return 
		}
		if status == "" || status == "any" || status == "all" {
			// ----------------------------------
			// Process all statuses
			// ----------------------------------
			for status_reg := range h.clients[c][topic][action] {
				// Delete the client from the instructions register
				delete(h.targets[topic].clients[action][status_reg], c)
			}
			// Delete the action from the client data (no statuses remaining)
			delete(h.clients[c][topic], action)
			// Check if no actions remain in the topic
			if len(h.clients[c][topic]) == 0 { 
				delete(h.clients[c], topic) 
				c.NotifyJson(map[string]any{"unsubscribe": true, "service": serviceOf(topic), "topic": topicOf(topic), "note": "auto unsub from topic. No subbed actions remained"})
			} else {
				c.NotifyJson(map[string]any{"unsubscribe": true, "service": serviceOf(topic), "topic": topicOf(topic), "action": action})
			}

			return
		} else {
			// ----------------------------------
			// Process a specific status
			// ----------------------------------
			// Ensure the status is valid and can be unsubbed from
			if !slices.Contains(ValidStatuses, status) {
				c.NotifyJson(map[string]any{"unsubscribe": false, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "status": status, "reason": "invalid status"})
				return 
			}
			_, ok = h.clients[c][topic][action][status]; if !ok { 
				c.NotifyJson(map[string]any{"unsubscribe": false, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "status": status, "reason": "you are not subscribed to this status"})
				return
			}
			delete(h.targets[topic].clients[action][status], c)
			delete(h.clients[c][topic][action], status)
			if len(h.clients[c][topic][action]) == 0 {
				delete(h.clients[c][topic], action)
				if len(h.clients[c][topic]) == 0 { 
					delete(h.clients[c], topic) 
					c.NotifyJson(map[string]any{"unsubscribe": true, "service": serviceOf(topic), "topic": topicOf(topic), "note": "auto unsub from topic. No subbed statuses and actions remained"})
					return
				} else {
					c.NotifyJson(map[string]any{"unsubscribe": true, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "note": "auto unsub from action. No subbes statuses remained"})
					return
				}
			}
			c.NotifyJson(map[string]any{"unsubscribe": true, "service": serviceOf(topic), "topic": topicOf(topic), "action": action, "status": status})
			return
		}
	} 
}

// Unregister the client from the eventhub and clean up
func (h *EventManager) unhandleClient(client *Client) error {
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

	client.close()

	return nil
}

// This handles writing to the client 
func (c *Client) writeLoop() {
	// Ticker for sending a connection test
	ticker := time.NewTicker(time.Second * time.Duration(c.ping_sec))
	defer ticker.Stop()
	// A failed write means the connection is gone, so close it rather than waiting for the read timeout
	defer c.close()
	var err error

	for {
		// Write to the client
		select {
		case msg, ok := <- c.send:
			if !ok { err = fmt.Errorf("Error reading from the send buffer") }
			logging.BasicDebug().Debug("Sending message to websocket", "msg", msg)
			c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.timeout_write_sec) * time.Second))
			err = c.conn.WriteMessage(websocket.TextMessage, msg)
			if err != nil { err = fmt.Errorf("Error writing to the client: %s", err.Error()) }
		case <- ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.timeout_write_sec) * time.Second))
			err = c.conn.WriteMessage(websocket.PingMessage, []byte{})
			if err != nil { err = fmt.Errorf("Error sending ping to the client: %s", err.Error()) }
		case <- c.done:
			return
		}
		if err != nil { logging.BasicDebug().Debug(err.Error()); return }
	}
}

func (h *EventManager) readLoop(c *Client) {
	for {
		c.conn.SetReadDeadline(time.Now().Add(time.Duration(c.timeout_read_sec) * time.Second))
		_, msg, err := c.conn.ReadMessage()
		if err != nil { logging.BasicDebug().Debug(err.Error()); break }

		// Unmarshal the message
		cm := models.ClientMessage{
			Action: "",
			Status: "",
		}
		err = json.Unmarshal(msg, &cm)

		if err != nil { continue }

		// Process the user request
		switch cm.Instruction {
		case "sub":
			h.subscribe(c, cm)
		case "unsub":
			h.unregister(c, cm.Key(), cm.Action, cm.Status)
		}

		// msg actions currently unimplemented
		logging.BasicDebug().Debug("Received message from client", "msg", cm)
	}
	c.close()
}

// Subscribe a client to a topic its actor is allowed to see. The look up and register
// happen under one lock, so a hot-reload cannot swap the topic in between.
func (h *EventManager) subscribe(c *Client, cm models.ClientMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	inst, ok := h.targets[cm.Key()]
	if ok && h.auth.CheckUserAgainstConfig(c.actor, inst.config, inst.permission) {
		h.registerUnsafe(c, cm.Key(), cm.Action, cm.Status)
	}
}

// serviceOf and topicOf split a topic key for messages to clients, which see the
// service and the topic as separate fields
func serviceOf(key string) string { s, _ := models.SplitTopicKey(key); return s }
func topicOf(key string) string   { _, t := models.SplitTopicKey(key); return t }

// -----------------------------------------------------------------
//              Event Handling / Publishing
// -----------------------------------------------------------------

// Send a bytes array to the client
func (c *Client) Notify(msg string) {
	c.trySend([]byte(msg))
}

// Send a bytes array to the client
func (c *Client) NotifyJson(msg map[string]any) {
	j, err := json.Marshal(msg)
	if err == nil { c.trySend(j) }
}

// Broadcase a bytes array across all clients
func (h *EventManager) Broadcast(i *Instructions, action string, status string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range i.clients[action][status] {
		c.trySend(data)
	}
}

// This is a function that handles webhooks. Currently, it only supports webhooks as per the config file,
// but maybe user / frontend defined in the future via an api function call?
func (h *EventManager) Callback(topic string, action string, status string, data []byte) []error {
	var errs []error

	// Copy the urls under the lock, then post without holding it
	h.mu.RLock()
	var urls []string
	if i, ok := h.targets[topic]; ok { urls = slices.Clone(i.webhook_url[action][status]) }
	h.mu.RUnlock()

	// Different reporting routes
	for _, url := range urls {
		res, err := webhookClient.Post(url, "application/json", bytes.NewReader(data))
		if err != nil { errs = append(errs, fmt.Errorf("Error (webhook) with topic {%s}, url {%s}, error: {%s}", topic, url, err.Error())); continue }
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode > 299 { errs = append(errs, fmt.Errorf("Error (webhook) with topic {%s}, url {%s}, error_status: {%s}", topic, url, res.Status)) }
	}

	if len(errs) > 0 { return errs }
	return nil
}

// Pushish a message without supplying a timestamp
func (h *EventManager) PublishNoTimestamp(ctx context.Context, action string, status string, topic string, payload map[string]any, success_count int) error {
	return h.Publish(ctx, action, status, time.Now(), topic, payload, success_count)
}
// Publish a message
func (h *EventManager) Publish(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload map[string]any, success_count int) error {
	// Extract and prep data
	pl, err := json.Marshal(payload)
	logging.BasicDebug().Debug("Received message",
	"action", action,
	"status", status,
	"topic", topic,
	"payload", payload,
)
if err != nil { return err }
return h.publish(ctx, action, status, timestamp, topic, pl, success_count)
}

// Publish a message across all end points as per the instructions.
func (h *EventManager) publish(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload []byte, success_count int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Read the instructions for this topic
	instructions, ok := h.targets[topic]

	// Validate the event status
	if !ok {return fmt.Errorf("Invalid topic {%s} for an event", topic)}
	if !slices.Contains(ValidStatuses, status) {return fmt.Errorf("Invalid status {%s} called for event", status)}
	user, found := reqctx.GetActor(ctx)
	if !found { return fmt.Errorf("Publish called from a non user!") }

	// Publish to the database
	if instructions.persist_to_db && action != "get" {
		service, short_topic := models.SplitTopicKey(topic)
		_, err := h.db.Exec(ctx, `INSERT INTO ohc_events(action, status, service, topic, log, time, "user") VALUES ($1, $2, $3, $4, $5, $6, $7)`, action, status, service, short_topic, payload, timestamp, user.Username)
		if err != nil { logging.Basic().Error("Failed to log event to db", "error", err) }
	}

	// Publish to webhooks
	safe.Go(logging.Basic(), "webhook callback", func() {
		for _, err := range h.Callback(topic, action, status, payload) { logging.Basic().Warn("WEBHOOK_FAILED", "error", err) }
	})

	// Publish to clients
	safe.Go(logging.Basic(), "websocket broadcast", func() { h.Broadcast(instructions, action, status, payload) })

	// Publish to each function
	if (len(instructions.functions) > 0 && success_count > 0) {
		for _, func_topic := range instructions.functions {
			// Build the update message for this function
			// Clients see the service and topic separately, and the function's bare name
			func_service, func_short := models.SplitTopicKey(func_topic)
			update_message := map[string]any{
				"event_type": "data altered",
				"event_time": timestamp,
				"service": func_service,
				"topic": func_short,
				"function_name": func_short[strings.LastIndex(func_short, "/")+1:],
				"items_added": 0,
				"items_updated": 0,
				"items_removed": 0,
			}
			switch action {
			case "create":
				update_message["items_added"] = success_count
			case "update":
				update_message["items_updated"] = success_count
			case "delete":
				update_message["items_removed"] = success_count
			}
			pl, err := json.Marshal(update_message)

			if err != nil {logging.BasicDebug().Error(fmt.Sprintf("Error parsing update_message for function {%s}\nError: {%s}", func_topic, err.Error()))}
			// Publish this data
			safe.Go(logging.Basic(), "function publish", func() {
				if err := h.publish(ctx, action, status, timestamp, func_topic, pl, success_count); err != nil { logging.Basic().Warn("Failed to publish function event", "function", func_topic, "error", err) }
			})
		}
	}

	return nil
}
