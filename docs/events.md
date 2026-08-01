# Events

Events occur during the program runtime and are used to alert of actions.
They run again insert, update, and delete events and trigger with each
status. 

Webhooks can be configured to create an alert of an event, and a websocket
can be used to subscribe to events.

The event handler manages all events, including processing webhooks and
notifying clients subscribed via websocks.

---

## queued
A queued event is when a task is received and there is no available workers to
in the queuemanager to complete the work.

- task_id
- task_type

## start
A start event occurs when a task begins work. This could be on task creation,
or when a free worker picks up the task and starts it.

- task_id
- task_type
- start_time

## success
A success event occurs when a task is completed with no failed data rows. This
event reports the rows different depending on the action (insert, update, delete).

Rows are reported to webhooks and websockets, but are not persisted to the database
events table.

- task_id
- task_type
- start_time
- complete_time

In addition the event will be modified based on the status

### insert
- success_items []map[string]any

### update
- success_items []updated
```
updated = {
    "where_fields": map[string]any
    "updated_values": map[string]any
}
```

### delete
- success_items []deleted
```
deleted = {
    "where_fields": map[string]any
}
```

## warn
A warn event occurs when a task is completed but there were failed rows. Similar
to success events, rows are reported to webhooks and websocks only.

- task_id
- task_type
- start_time
- complete_time

### insert
- success_items []map[string]any
- failed_items []failed
```
failed = {
    "row": map[string]any
    "error": string
}
```

### update
- success_items []map[string]updated
```
updated = {
    "where_fields": map[string]any
    "updated_values": map[string]any
}
```
- failed_items []failed
```
failed = {
    "row": map[string]any
    "error": string
}
```

### delete
- success_items []map[string]deleted
```
deleted = {
    "where_fields": map[string]any
}
```
- failed_items []failed
```
failed = {
    "row": map[string]any
    "error": string
}
```

## failed
A failed event occurs when there were no successful inserts or updates. Similar
to success events, rows are reported to webhooks and websocks only.
 
- task_id
- task_type
- start_time
- complete_time
- failed_items []failed
```
failed = {
    "row": map[string]any
    "error": string
}
```

## error
An error event occurs when there is an internal processing error and the task
has to be cancelled.

- task_id
- task_type
- start_time
- complete_time
- error
