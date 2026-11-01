package models

// Defines a message from the client to alter its connection or request information
// Instruction - The high level category of actions that can be taken i.e "sub", "unsub"
// Service - The service that owns the topic, i.e "ohc" or "assets"
// Topic - The table, diff or declared function within that service
// Action - What needs to be done
// Status - The event status to listen for
//
// **Currently valid**
// Instructions: sub, unsub
// Service: {service ids}
// Topic: table:{end-point}, diff:{end-point}, func:{end-point}/{name}
// Actions: get, insert, update, delete, any
// Statuses: queued, start, success, warn, failed, error, all
//
// **Example**
// {"instruction": "sub", "service": "assets", "topic": "table:asset", "action": "update", "status": "success"}
type ClientMessage struct {
	Instruction string `json:"instruction"`
	Service     string `json:"service"`
	Topic       string `json:"topic"`
	Action      string `json:"action"`
	Status      string `json:"status"`
}

// Key is the event manager's key for the topic the message names
func (cm ClientMessage) Key() string { return TopicKey(cm.Service, cm.Topic) }
