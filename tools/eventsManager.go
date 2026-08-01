package tools

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
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
)

// -----------------------------------------------------------------
//            Package variables
// -----------------------------------------------------------------

var ValidActions  = []string{"get", "create", "update", "delete"}
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
}


type Instructions struct {
	webhook_url    webhook_action
	persist_to_db  bool
	clients        instruct_action
	functions      function_references
	is_func        bool
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
	done chan struct{}
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
func NewEventManager(ping_sec int, timeout_sec int, db models.DBExecQuery) *EventManager {
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
	}
}

// -----------------------------------------------------------------
//            Topic Registration Management
// -----------------------------------------------------------------

// This function is used for registering a function. It will enable the function as a topic and
// also reference it in the table topic. If the table is not a valid topic, which should never 
// happen, the function will not register and will log an error.
func (h *EventManager) RegisterFunction(table string, topic string, hooks *models.EventAction) {
  table_topic := fmt.Sprintf("table:%s", table)
	_, valid := h.validTopics[table_topic]
	if !valid {
		GetBasicDebugLogger().Error(fmt.Sprintf(`Attempted to register the function topic {%s} against
		table {%s}, but the table is an invalid topic`, topic, table))
		return
	}

	h.EnableTopic(topic, hooks)
	h.targets[topic].is_func = true
	h.targets[table_topic].functions = append(h.targets[table_topic].functions, topic)
}

// Add a topic as a valid topic
func (h *EventManager) EnableTopic(topic string, hooks *models.EventAction) {
	h.validTopics[topic] = true

	// Register instructions for topics
	if h.targets[topic] == nil {
		h.targets[topic] = &Instructions{
			webhook_url: webhook_action{},
			persist_to_db: true,
			clients: instruct_action{},
			functions: function_references{},
			is_func: false,
		}
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
		for k, v := range GetStructAsDict(hooks) {
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
func (h *EventManager) RegiterTopicAction(w http.ResponseWriter, r *http.Request, topic string, action []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	go h.handleClientTopicAction(conn, topic, action)
	
	return nil
}
// Upgrade and manage a new websocket based on the topic.
// Websock register for the specified statuses and actions.
func (h *EventManager) RegiterTopicStatusAction(w http.ResponseWriter, r *http.Request, topic string, action []string, status []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	go h.handleClientTopicStatusAction(conn, topic, action, status)
	
	return nil
}
// Upgrade and manage a new websocket based on the topic and status, it registers for all actions
func (h *EventManager) RegiterTopicStatus(w http.ResponseWriter, r *http.Request, topic string, status []string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	go h.handleClientTopicStatus(conn, topic, status)
	
	return nil
}
// Upgrade and manage a new websocket based on the topic. It registers for all actions and statuses
func (h *EventManager) RegiterTopicOnly(w http.ResponseWriter, r *http.Request, topic string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil { return err }

	go h.HandleClientTopic(conn, topic)
	
	return nil
}

// -----------------------------------------------------------------
//              Client Management
// -----------------------------------------------------------------

// Register a new client connection to specific topic
func (h *EventManager) HandleClientTopic(conn *websocket.Conn, topic string) {
	if !h.validTopics[topic] { return }
	h.handleClient(conn, topic, ValidActions, ValidStatuses)
}
// Register a new client connection to specific topic statuses
func (h *EventManager) handleClientTopicStatus(conn *websocket.Conn, topic string, status []string) {
	h.handleClient(conn, topic, ValidActions, status)
}
// Register a new client connection to specific topic actions
func (h *EventManager) handleClientTopicAction(conn *websocket.Conn, topic string, action []string) {
	h.handleClient(conn, topic, action, ValidStatuses)
}
// Register a new client connection to specific topic actions and statuses. Statuses apply over all actions
func (h *EventManager) handleClientTopicStatusAction(conn *websocket.Conn, topic string, action []string, status []string) {
	h.handleClient(conn, topic, action, status)
}
// Register a new client connection. It is expected to call this via a go function
func (h *EventManager) handleClient(conn *websocket.Conn, topic string, action []string, status []string) {
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
	h.readLoop(client)

	// Unregister the client
	h.unhandleClient(client)
}

// Register the client in the eventhub safely
func (h *EventManager) register(c *Client, topic string, action string, status string) {
	h.mu.Lock()
	h.registerUnsafe(c, topic, action, status)
	h.mu.Unlock()
}
// Register the client in the eventhub UNSAFE
func (h *EventManager) registerUnsafe(c *Client, topic string, action string, status string) {
	// Ensure topic, action and status are valid
	_, valid_topic := h.targets[topic]
	valid_action := action == "" || action == "any" || action == "all" || slices.Contains(ValidActions, action)
	valid_status := status == "" || status == "any" || status == "all" || slices.Contains(ValidStatuses, status)
	if valid_topic == false || valid_action == false || valid_status == false {
		c.NotifyJson(map[string]any{"subscribe": false, "reason": "invalid subscription request", "is_topic_valid": valid_topic, "is_action_valid": valid_action, "is_status_valid": valid_status})
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

	c.NotifyJson(map[string]any{"subscribe": true, "topic": topic, "action": action, "status": status})
}

// Register the client in the eventhub safely
func (h *EventManager) unregister(c *Client, topic string, action string, status string) {
	h.mu.Lock()
	h.unregisterUnsafe(c, topic, action, status)
	h.mu.Unlock()
}
// Register the client in the eventhub UNSAFE
func (h *EventManager) unregisterUnsafe(c *Client, topic string, action string, status string) {
	// Process a topic as valid and can be unsubscribed from
	_, ok := h.targets[topic]; if !ok { 
		c.NotifyJson(map[string]any{"unsubscribe": false, "topic": topic, "reason": "invalid topic"})
		return 
	}
	_, ok = h.clients[c][topic]; if !ok { 
		c.NotifyJson(map[string]any{"unsubscribe": false, "topic": topic, "reason": "you are not subscribed to this topic"})
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

			c.NotifyJson(map[string]any{"unsubscribe": true, "topic": topic})
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
				c.NotifyJson(map[string]any{"unsubscribe": true, "topic": topic, "action": action, "status": status})
			}
			return
		}
	} else {
		// --------------------------------
		// Process a specific action
		// --------------------------------
		// Validate the action
		if !slices.Contains(ValidActions, action) {
			c.NotifyJson(map[string]any{"unsubscribe": false, "topic": topic, "action": action, "reason": "invalid action"})
			return 
		}
		_, ok := h.clients[c][topic][action]; if !ok {
			c.NotifyJson(map[string]any{"unsubscribe": false, "topic": topic, "action": action, "reason": "you are not subscribed to this action"})
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
				c.NotifyJson(map[string]any{"unsubscribe": true, "topic": topic, "note": "auto unsub from topic. No subbed actions remained"})
			} else {
				c.NotifyJson(map[string]any{"unsubscribe": true, "topic": topic, "action": action})
			}

			return
		} else {
			// ----------------------------------
			// Process a specific status
			// ----------------------------------
			// Ensure the status is valid and can be unsubbed from
			if !slices.Contains(ValidStatuses, status) {
				c.NotifyJson(map[string]any{"unsubscribe": false, "topic": topic, "action": action, "status": status, "reason": "invalid status"})
				return 
			}
			_, ok = h.clients[c][topic][action][status]; if !ok { 
				c.NotifyJson(map[string]any{"unsubscribe": false, "topic": topic, "action": action, "status": status, "reason": "you are not subscribed to this status"})
				return
			}
			delete(h.targets[topic].clients[action][status], c)
			delete(h.clients[c][topic][action], status)
			if len(h.clients[c][topic][action]) == 0 {
				delete(h.clients[c][topic], action)
				if len(h.clients[c][topic]) == 0 { 
					delete(h.clients[c], topic) 
					c.NotifyJson(map[string]any{"unsubscribe": true, "topic": topic, "note": "auto unsub from topic. No subbed statuses and actions remained"})
					return
				} else {
					c.NotifyJson(map[string]any{"unsubscribe": true, "topic": topic, "action": action, "note": "auto unsub from action. No subbes statuses remained"})
					return
				}
			}
			c.NotifyJson(map[string]any{"unsubscribe": true, "topic": topic, "action": action, "status": status})
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
			if !ok { err = fmt.Errorf("Error reading from the send buffer") }
			GetBasicDebugLogger().Debug("Sending message to websocket", "msg", msg)
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
		if err != nil { break }
	}
	ticker.Stop()
}

func (h *EventManager) readLoop(c *Client) {
	for {
		c.conn.SetReadDeadline(time.Now().Add(time.Duration(c.timeout_read_sec) * time.Second))
		_, msg, err := c.conn.ReadMessage()
		if err != nil { GetBasicDebugLogger().Debug(err.Error()); break }

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
			h.register(c, cm.Topic, cm.Action, cm.Status)
		case "unsub":
			h.unregister(c, cm.Topic, cm.Action, cm.Status)
		}

		// msg actions currently unimplemented
		GetBasicDebugLogger().Debug("Received message from client", "msg", cm)
	}
	close(c.done)
}

// -----------------------------------------------------------------
//              Event Handling / Publishing
// -----------------------------------------------------------------

// Send a bytes array to the client
func (c *Client) Notify(msg string) {
	c.send <- []byte(msg)
}

// Send a bytes array to the client
func (c *Client) NotifyJson(msg map[string]any) {
	j, err := json.Marshal(msg)
	if err == nil { c.send <- []byte(j) }
}

// Broadcase a bytes array across all clients
func (h *EventManager) Broadcast(i *Instructions, action string, status string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c, _ := range i.clients[action][status] {
		c.send <- data
	}
}

// This is a function that handles webhooks. Currently, it only supports webhooks as per the config file,
// but maybe user / frontend defined in the future via an api function call?
func (h *EventManager) Callback(topic string, action string, status string, data []byte) []error {
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
func (h *EventManager) PublishNoTimestamp(ctx context.Context, action string, status string, topic string, payload map[string]any, success_count int) error {
	return h.Publish(ctx, action, status, time.Now(), topic, payload, success_count)
}
// Publish a message
func (h *EventManager) Publish(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload map[string]any, success_count int) error {
	// Extract and prep data
	pl, err := json.Marshal(payload)
	GetBasicDebugLogger().Debug("Received message",
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
	user, found := middleware.GetUser(ctx)
	if !found { return fmt.Errorf("Publish called from a non user!") }

	// Publish to the database
	if instructions.persist_to_db && action != "get" {
		h.db.Exec(ctx, "INSERT INTO events(action, status, topic, log, time, user) VALUES ($, $, $, $, $)", action, status, topic, payload, timestamp, user.Username)
	}

	// Publish to webhooks
	go h.Callback(topic, action, status, payload)

	// Publish to clients
	go h.Broadcast(instructions, action, status, payload)

	// Publish to each function
	if (len(instructions.functions) > 0 && success_count > 0) {
		for _, func_topic := range instructions.functions {
			// Build the update message for this function
			update_message := map[string]any{
				"event_type": "data altered",
				"event_time": timestamp,
				"function_name": func_topic,
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

			if err != nil {GetBasicDebugLogger().Error(fmt.Sprintf("Error parsing update_message for function {%s}\nError: {%s}", func_topic, err.Error()))}
			// Publish this data
			go h.publish(ctx, action, status, timestamp, func_topic, pl, success_count)
		}
	}

	return nil
}
