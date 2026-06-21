package models

// Defines a message from the client to alter its connection or request information
// Instruction - The high level category of actions that can be taken i.e "subscribe", "unsubscribe", "request"
// Topic - The table or declared function
// Action - What needs to be done 
// Status - The event status to listen for ()
//
// **Currently valid**
// Intructions: sub, unsub
// Topic: {tables}
// Actions: get, insert, update, delete, any
// Statuses: queued, start, success, warn, failed, error, all
//
// **Example**
// Instruction: manage
// Action
type ClientMessage struct {
	Instruction string `json:"instruction"`
	Topic string `json:"topic"`
	Action string `json:"action"`
	Status string `json:"status"`
}

