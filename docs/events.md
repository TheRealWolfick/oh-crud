# Events

Events tell a frontend what happened to a write it, or anyone else, made. Every
write request (`POST`, `PUT`, `DELETE`, single or group) becomes a **task** that
runs asynchronously, and the task publishes an event each time its status
changes.

Events are delivered three ways:

| Destination | What gets it |
|-------------|--------------|
| **Websockets** | Clients subscribed to the topic, action and status (`websockets.md`) |
| **Webhooks** | URLs configured under the model's `web-hooks` (`config-instructions.md`) |
| **`events` table** | Every table-topic event except `get`. Readable at `GET /events` |

Reads (`GET`) do not create tasks and publish no events.

---

## 1. The task lifecycle

A write request returns as soon as the task is queued:

```json
{ "task_type": "Update Resource", "task_id": "k2Xc9...32 chars", "successful_submission": true, ... }
```

The `task_id` is the key that ties the response to its events. A task then moves
through these statuses:

```
            ┌──────────► queued ──┐   (only if every worker is busy)
 request ───┤                     ▼
            └──────────────────► start ──► success | warn | failed | error
```

| Status | Meaning | Terminal |
|--------|---------|----------|
| `queued` | Waiting for a free worker | No |
| `start` | A worker picked it up | No |
| `success` | Every row succeeded | Yes |
| `warn` | Some rows succeeded, some failed | Yes |
| `failed` | No row succeeded | Yes |
| `error` | The task broke (e.g. a database error) | Yes |

Exactly one terminal event is published per task. `queued` is skipped when a
worker is free.

**Action** (`create`, `update`, `delete`) is the kind of write. It is used for
subscribing and for webhooks, and it appears in the payload as `task_type`.

> `task_type` in an **event** is the action (`create`, `update`, `delete`).
> `task_type` in the **HTTP response** is a description (`Add Resource`,
> `Update Bulk Resources`, …). Match the two on `task_id`, not `task_type`.

---

## 2. Payloads by status

All timestamps are RFC 3339 strings. `note` is the request's `X-User-Note`
header (empty string if none was sent).

### queued

```json
{ "task_id": "…", "task_type": "create", "task_status": "queued" }
```

### start

```json
{ "task_id": "…", "task_type": "create", "task_status": "start",
  "task_start": "2026-10-27T03:12:09.51Z", "note": "" }
```

### success and warn

```json
{
  "task_id": "…",
  "task_type": "update",
  "task_status": "warn",
  "task_start": "2026-10-27T03:12:09.51Z",
  "task_end": "2026-10-27T03:12:09.60Z",
  "note": "Quarterly import",
  "task_response": {
    "table_name": "asset_data",
    "total_count": 3,
    "success_count": 2,
    "success_items": [ … ],
    "failed_count": 1,
    "failed_items": [ { "row": { … }, "error": "…" } ]
  }
}
```

`success` and `warn` share this shape. `warn` means `failed_count > 0`.

### failed

Same shape, but the response is under **`task_error`** instead of
`task_response`. `success_count` is `0`.

```json
{
  "task_id": "…", "task_type": "delete", "task_status": "failed",
  "task_start": "…", "task_end": "…", "note": "",
  "task_error": {
    "table_name": "asset_data", "total_count": 1, "success_count": 0,
    "success_items": [], "failed_count": 1,
    "failed_items": [ { "row": { "asset_no": "CL999" },
                        "error": "No row was matched. Likely an invalid or wrong identifier" } ]
  }
}
```

### error

```json
{
  "task_id": "…", "task_type": "create", "task_status": "error",
  "task_start": "…", "task_end": "…", "note": "",
  "task_error": { "error": "<message>" }
}
```

---

## 3. Items by action

Row maps in events are keyed by **field name** (the keys under `fields:` in the
model config, which are also the keys `GET` returns), not by `json` name.

### create

`success_items` is a list of **batches**, each batch a list of the inserted
rows. Group inserts are split into smaller batches when a row fails, so one
request can produce several batches. Flatten them to get every inserted row.

```json
"success_items": [
  [ { "asset_no": "CL001", "description": "Pump A" },
    { "asset_no": "CL002", "description": "Pump B" } ],
  [ { "asset_no": "CL004", "description": "Valve" } ]
]
```

Values in these rows are the coerced values that were inserted. Database
defaults (serial ids, `now()` timestamps) are **not** included: refetch to get
them.

### update

```json
"success_items": [
  { "where_fields":   { "asset_no": "CL001" },
    "updated_values": { "description": "Pump A (replaced)" } }
]
```

`where_fields` is the key used to find the row (the primary key, or a unique
key). `updated_values` are the fields that were set.

### delete

```json
"success_items": [
  { "WhereFields": { "asset_no": "CL001" } }
]
```

Note the key is `WhereFields` here (not `where_fields`). For a `soft-delete`
model the row is kept and its `deleted_flag` set to `true`.

### failed_items (all actions)

```json
"failed_items": [ { "row": { … }, "error": "duplicate key value violates unique constraint …" } ]
```

`row` is the coerced row that was attempted (keyed by field name). It can be
`null` for task-wide failures.

---

## 4. Function events

Declarative functions (`func:{name}` topics) do not receive the data. When a
write on the function's bound model finishes with `success` or `warn` (at
least one row changed), every function bound to that model publishes:

```json
{
  "event_type": "data altered",
  "event_time": "2026-10-27T03:12:09.60Z",
  "function_name": "func:assets-by-building",
  "items_added": 0,
  "items_updated": 2,
  "items_removed": 0
}
```

It is delivered with the same action and status as the table event, so the
usual subscription filters apply. Refetch the function with your own
parameters when it arrives. Function events are not stored in the `events`
table.

---

## 5. Processing events in a frontend

1. **Correlate by `task_id`.** Store the `task_id` from each write response and
   resolve the pending UI state when that task's terminal event arrives.
   Events can arrive before the HTTP response has been processed, so buffer
   unknown `task_id`s briefly.
2. **Handle each terminal status:**
   - `success`: apply the change (or refetch) and clear pending state.
   - `warn`: apply the successes, show `failed_items` with their `error`.
   - `failed`: show `task_error.failed_items`.
   - `error`: show `task_error.error` as a system failure; nothing was written.
3. **Changes by other users** arrive the same way with `task_id`s you did not
   create. Merge `success_items` into cached data, or simply refetch.
4. **Refetch for anything the event does not carry**: server-side defaults,
   joined descriptions, function results.
5. **Treat events as hints, not a log.** Delivery is best-effort and there is
   no replay after a disconnect. Refetch the visible data on reconnect.

---

## 6. Reading past events

Table-topic events are stored in the `events` model, exposed at
`GET /events` (roles `user` and `report` by default). It supports the normal
filtering and paging (`api-guide.md`):

```text
GET /events?topic=table:asset/data&status=warn&sort_by=event_id~desc
```

| Field | Notes |
|-------|-------|
| `event_id` | Serial id |
| `action` | `create`, `update`, `delete` |
| `status` | One of the six statuses |
| `topic` | e.g. `table:asset/data` |
| `log` | The full event payload from section 2 |
| `time` | Event time. Filter with `?event_time=…` |
| `user` | Username that made the request. Filter with `?event_user=…` |

---

## 7. Webhooks

Webhooks receive the same payloads as websockets, as an HTTP `POST` with a JSON
body, for the actions and statuses configured in the model's `web-hooks`
(`config-instructions.md`). Delivery is fire-and-forget: a failed call or
non-`200` response is not retried.
