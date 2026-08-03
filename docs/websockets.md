# Websockets
Webhooks are effectively a component in themselves. They provide a persistent connection to the backend which, in its most simple form, allows events on tables to be listened to.

Connecting to a websocket can be done by making a request to ws://<url>/ws/<end_point>. This will upgrade the connection and register it as an active client for management

## Talking
Websockets are communicated with in a very specific json structure. These are:
- instruction
- topic
- action
- status

All communications must be passed as valid json. If the instruction is not in this format, it is silently ignored. A valid request may be

`{"instruction": "sub", "topic": "table:building", "status": "error"}`

The above will subscribe the client to whenever there is an error event on the table `buildings`

### Instruction
This is the instruction you wish to have the server do with your connection. Valid instructions to pass are:
- sub    (subscribe)
- unsub  (unsubscribe)

A valid action must be passed to the server for action to be taken

### Topic
This is the area the client's instruction will apply to. This can be either a table, or declared function (WIP).

For a table, the structure of the topic is `table:table_name`. Likewise, a declared function is `function:function_name`

### Action
This is the event action that is occuring the websockets is listening to. Valid actions are:
- get
- insert
- update
- delete
- any/all

As a special note, `any` or `all` will apply the instruction across all valid actions. Not passing in an action will be treated as `any`/`all`.

### Status
This is the event status you are listening to. A client may only wish to listen to errors, or failures, or task start events to get an idea of errors / traffic. Valid statuses are:
- queued
- start
- success
- warn
- failed
- error
- any/all

The same as actions, `any` or `all` are applicable here to apply across all valid statuses. Not passing a status is treated as the former for the instruction.

## listening
Whenever an event occurs, it will be broadcast to all clients currently registererd as listening to that topic, action, and status. The structure of this message is predictable but can vary depending on the status. The below are the event structures


## End Point
All connections to webooks are started the websocket interface of a table:
```
ws://url/ws/{end_point}
```

An example of this is `ws://localhost:8080/ws/asset/data`
