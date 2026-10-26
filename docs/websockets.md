# Websockets

Websockets give a client a persistent connection to Oh CRUD so it can be told
when data changes, instead of polling. A client subscribes to **topics** and
receives the **events** published on them. The event payloads themselves are
described in `events.md`.

---

## 1. Connecting

Every model with an `end-point` and a `GET` permission exposes a websocket at:

```
ws://<host>/ws/{end-point}
wss://<host>/ws/{end-point}      (behind TLS)
```

For example `wss://api.example.com/ws/asset/data`.

### 1.1 Authentication

The upgrade request goes through the same checks as any data endpoint
(`auth.md`):

- An `X-API-Key` header with a valid key is required.
- The user needs a role in the model's **`end-points-allowed.POST`** list, or
  the admin role. (`GET` must also be enabled on the model for the route to
  exist.)

Failures return the normal HTTP errors (`401`, `405`) before the upgrade.

**Browsers cannot set `X-API-Key` on a websocket.** The browser `WebSocket`
API does not allow custom headers. Until token-based auth is added, a browser
frontend has two options:

1. Connect through a backend (backend-for-frontend or reverse proxy) that adds
   the `X-API-Key` header to the upgrade request.
2. Run the websocket from a non-browser client (server, desktop, mobile) that
   can set headers.

```js
// Node.js, using the "ws" package
import WebSocket from "ws";
const sock = new WebSocket("wss://api.example.com/ws/asset/data", {
  headers: { "X-API-Key": apiKey },
});
```

### 1.2 Initial subscription

On connect the client is automatically subscribed to the endpoint's own table
topic, `table:{end-point}`:

| Connect URL | Subscribed to |
|-------------|---------------|
| `/ws/asset/data` | Every action and every status |
| `/ws/asset/data?status=error` | Every action, `error` status only |

Each subscription is confirmed with a message (see 3.1). A client that wants
nothing by default can unsubscribe straight after connecting.

### 1.3 Keep-alive

The server sends a websocket **ping every 27 seconds**. If it hears nothing
back (a pong or any message) for about **57 seconds**, it closes the
connection. Browsers and most websocket libraries answer pings automatically.

There is no resume: events published while a client is disconnected are not
replayed. After reconnecting, refetch the data you display. Persisted events
can also be read from `GET /events` (see `events.md`).

---

## 2. Talking to the server

After connecting, a client changes its subscriptions by sending JSON text
messages:

```json
{"instruction": "sub", "topic": "table:asset/data", "action": "update", "status": "success"}
```

| Key | Required | Values |
|-----|----------|--------|
| `instruction` | Yes | `sub` (subscribe) or `unsub` (unsubscribe) |
| `topic` | Yes | A topic name, see 2.1 |
| `action` | No | `get`, `create`, `update`, `delete`, or `any`/`all`. Missing means all |
| `status` | No | `queued`, `start`, `success`, `warn`, `failed`, `error`, or `any`/`all`. Missing means all |

Messages that are not valid JSON, or have another `instruction`, are silently
ignored.

### 2.1 Topics

| Topic | Published when | Example |
|-------|----------------|---------|
| `table:{end-point}` | A write task on that model changes state | `table:asset/data` |
| `func:{function-name}` | A write on the function's bound model succeeds | `func:assets-by-building` |

The table topic uses the model's **end-point**, not its table name.

A function topic carries a small "data altered" notice, not the data itself,
because the server cannot know which parameters each client is filtering by.
Refetch the function when it arrives. See `events.md`.

### 2.2 Actions and statuses

| Action | Triggered by |
|--------|--------------|
| `create` | `POST /{endpoint}` and `POST /{endpoint}/group` |
| `update` | `PUT /{endpoint}` and `PUT /{endpoint}/group` |
| `delete` | `DELETE /{endpoint}` and `DELETE /{endpoint}/group` |
| `get` | Accepted, but reads do not publish events, so it never fires |

| Status | Meaning |
|--------|---------|
| `queued` | Task accepted, waiting for a free worker |
| `start` | A worker began the task |
| `success` | Finished, every row succeeded |
| `warn` | Finished, some rows failed |
| `failed` | Finished, no row succeeded |
| `error` | The task itself failed (internal error) |

Subscriptions combine: subscribing to `action: update` then to
`status: error` gives every update event plus every error event. Unsubscribe
works on the same combinations.

---

## 3. Receiving messages

There are two kinds of message: **acknowledgements** of your instructions, and
**events**. Tell them apart by their keys: acknowledgements always have
`subscribe` or `unsubscribe`, and events always have `task_id` (table topics)
or `event_type` (function topics).

### 3.1 Acknowledgements

Subscribe:

```json
{"subscribe": true, "topic": "table:asset/data", "action": "update", "status": "all"}
{"subscribe": false, "reason": "invalid subscription request",
 "is_topic_valid": true, "is_action_valid": false, "is_status_valid": true}
```

A subscribe to `any`/`all` actions is echoed back as `"action": "all"`.

Unsubscribe:

```json
{"unsubscribe": true, "topic": "table:asset/data"}
{"unsubscribe": true, "topic": "table:asset/data", "action": "update"}
{"unsubscribe": true, "topic": "table:asset/data", "note": "auto unsub from topic. No subbed actions remained"}
{"unsubscribe": false, "topic": "table:asset/data", "reason": "you are not subscribed to this topic"}
```

Other `reason` values: `invalid topic`, `invalid action`, `invalid status`,
`you are not subscribed to this action`, `you are not subscribed to this status`.

### 3.2 Events

Each event is sent as a single JSON object: the event payload itself, with
**no envelope**. The topic is not included, and the action only appears as
`task_type` (`create`, `update`, `delete`). Payloads per status are documented
in `events.md`.

```json
{
  "task_id": "Xq3...32 chars",
  "task_type": "update",
  "task_status": "success",
  "task_start": "2026-10-27T03:12:09.51Z",
  "task_end": "2026-10-27T03:12:09.53Z",
  "note": "Bulk relabel",
  "task_response": { "success_count": 2, "...": "..." }
}
```

If one connection subscribes to more than one table topic, it cannot tell
from the payload which table an event came from. Use one connection per
table, or correlate on `task_id` (returned by the write request).

---

## 4. Typical client flow

```js
const sock = new WebSocket(url /* via a proxy that adds X-API-Key */);

sock.onopen = () => {
  // Already subscribed to everything on table:asset/data. Narrow it down:
  sock.send(JSON.stringify({ instruction: "unsub", topic: "table:asset/data" }));
  sock.send(JSON.stringify({ instruction: "sub", topic: "table:asset/data", status: "success" }));
  sock.send(JSON.stringify({ instruction: "sub", topic: "table:asset/data", status: "warn" }));
  sock.send(JSON.stringify({ instruction: "sub", topic: "func:assets-by-building" }));
};

sock.onmessage = (msg) => {
  const m = JSON.parse(msg.data);
  if ("subscribe" in m || "unsubscribe" in m) return handleAck(m);
  if (m.event_type === "data altered") return refetchFunction(m.function_name);
  if (m.task_id) return handleTaskEvent(m);   // see events.md
};

sock.onclose = () => scheduleReconnect();     // then refetch visible data
```
