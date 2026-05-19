package tools

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventHub struct {
	clients  map[*Client][]*Topic
	targets      map[string]*Instructions
	mu           sync.RWMutex
	validTopics  map[string]bool
	sink         chan []byte
	ping_sec int
	timeout_sec int
}


type Instructions struct {
	webhook_url    string
	persist_to_db  bool
	db             *pgxpool.Pool
	clients        map[*Client]bool
}

type Topic struct {
	Topic string   
	Table string
	Type  string
}

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
		clients: map[*Client][]*Topic{},
		targets: map[string]*Instructions{},
		ping_sec: ping_sec,
		timeout_sec: timeout_sec,
		validTopics: map[string]bool{},
		sink: make(chan []byte, 256),
	}
}

// Expects the topic to be passed in with the format of `type:table` or `type:func`
// depending on if it is a table or function topic.
func NewTopic(typ string, tar string, tab string) *Topic {
	return &Topic{
		Topic: fmt.Sprintf("%s:%s", typ, tar),
		Table: tab,
		Type: typ,
	}
}

// Add a topic as a valid topic
func (h *EventHub) EnableTopic(topic string) {
	h.validTopics[topic] = true
}

// Register a new client connection. It is expected to call this via a go function
func (h *EventHub) HandleClient(conn *websocket.Conn, topic string) {
	client := h.register(conn)
	h.targets[topic].clients[client] = true

	// Write loop, handles sending data to the client, including ping-pong initiation. Called via go
	go client.writeLoop()

	// Read loop, handles reads from the client
	client.readLoop()

	// Unregister the client
	h.unregister(client)
}

// Register the client in the eventhub
func (h *EventHub) register(conn *websocket.Conn) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Create the client
	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
		ping_sec: h.ping_sec,
		timout_sec: h.timeout_sec,
	}

	// Add the client to the hub
	h.clients[client] = []*Topic{}
	return client
}

// Unregister the client from the eventhub and clean up
func (h *EventHub) unregister(client *Client) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Sanity check to make sure the client exists
	topics, ok := h.clients[client]
	if !ok { return fmt.Errorf("Tried to unregister a client who isn't in the subscribers list: %v", client) }

	// Remove client from all the topics
	for _, t := range topics {
		delete(h.targets[t.Topic].clients, client)
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
func (eh *EventHub) Broadcast(message []byte) {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	for c, _ := range eh.clients {
		c.send <- message
	}
}
