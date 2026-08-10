# Oh CRUD — Frontend Integration Context

Single-file reference for building a frontend against this backend. Everything below is
derived from the current source, not from aspiration. Where the running code disagrees
with the other docs in `docs/`, this file follows the code and says so.

**Backend:** Go 1.25 HTTP server, module `lotusforge.au/api-server`, PostgreSQL via pgx.
**Listens on:** `:8080` (hardcoded, plain HTTP, no TLS).
**Shape:** configuration-driven. Data models are YAML files; the server reads them at
startup, syncs the Postgres schema with Atlas, generates a full REST surface per model,
opens a websocket topic per model, and hot-reloads on file change. No Go code is written
per model.

---

## 1. Mental model

```
config/default/*.yaml      ─┐
config/base-models/*.yaml  ─┼→ DataModel structs ─→ Atlas schema sync ─→ routes + ws topics
config/functions/*.yaml    ─┘                                          ─→ /{ep}/fn/{name}
config/server/server.yaml  ────────────────────────→ CORS + RBAC (hot reloaded)
```

Three facts that shape every frontend decision:

1. **Reads are synchronous, writes are not.** `POST`/`PUT`/`DELETE` validate, enqueue, and
   return a `task_id` immediately. The row is not in the database when the response
   arrives. To learn the outcome you must subscribe to the websocket (see §9 — the events
   table is currently not usable for this).
2. **The endpoint surface is discovered, not hardcoded.** Use `GET /openapi.json` and
   `GET /{endpoint}/fn/schema` to build tables, forms, and validation dynamically.
3. **A browser cannot talk to this API directly.** Auth is a custom header and websocket
   auth is a custom header; CORS preflight is only wired for one path per model. Build a
   server-side proxy (BFF). See §4 — this is the single most important integration
   constraint.

---

## 2. Authentication

One header, on every request except `/health` and `/openapi.json`:

```
X-API-Key: <uuid>
```

There is **no `X-Username` header** any more — earlier docs mention it; the code ignores it.
The key is looked up in the `users` table and resolves to a user record:

```
username, email, mobile, api_access (bool), roles (comma-separated string)
```

`401` is returned when the key is missing, unknown, or `api_access` is false.
The error body is plain text, not JSON.

### Roles and RBAC

`roles` is a comma-separated string on the user row, e.g. `"user,report,administrator"`.

Per model, `end-points-allowed` declares which methods exist and which roles may call them:

| YAML                    | Effect                                                        |
|-------------------------|---------------------------------------------------------------|
| key omitted             | Method disabled → `405` with an `Allow` header                |
| `GET: []`               | Method enabled, **admin role only** (empty list matches nobody)|
| `GET: ["user","report"]`| Those roles, plus admin                                       |

The admin role is global: `rbac.admin-role` in `config/server/server.yaml`
(currently `administrator`). A user holding it passes every role check everywhere.

Other role gates:

- **Admin/schema endpoints** check the model's own `admin-roles:` list (plus global admin).
- **Declarative functions** check `roles-allowed:` if set, otherwise inherit the bound
  model's `GET` list.
- **Websockets** check the model's **`POST`** list (not `GET` — quirk, but that's the code).

There is no login endpoint, no session, no JWT. API keys are provisioned out of band:
`POST /user` creates a user row, but `api_key` is a `private` field so it is never returned
by the API — it has to be read from the database. `rbac.default-user-role` currently has no
effect.

### Optional annotation header

Any write may carry a free-text note that is attached to the task and echoed in task events:

```
X-User-Note: Imported from external sync
```

---

## 3. Global endpoints

| Endpoint            | Auth | Notes                                              |
|---------------------|------|----------------------------------------------------|
| `GET /health`       | No   | Returns the literal text `OK`                      |
| `GET /openapi.json` | No   | OpenAPI 3.0.3, regenerated per request from loaded models. No CORS headers. |

The generated spec covers CRUD, `/group`, and `/diff` paths, with per-field query
parameters and request schemas. It does **not** describe `/fn/*`, `/history/*`, `/admin/*`,
or websockets, and it declares no security scheme — treat it as a field/route inventory,
not a complete contract.

---

## 4. CORS reality — build a BFF proxy

`config/server/server.yaml` has an origin allow-list and a header allow-list, and the CORS
middleware echoes an allowed `Origin` back with `Access-Control-Allow-*` headers. But:

- Only `OPTIONS /{endpoint}` is registered. `/{endpoint}/group`, `/{endpoint}/diff`,
  `/{endpoint}/fn/*`, `/{endpoint}/history/*`, and `/{endpoint}/admin/*` have **no OPTIONS
  route**, so browser preflight against them gets `405`.
- The diff routes and the declarative-function routes are registered **without** the CORS
  middleware, so even successful responses carry no CORS headers.
- `X-API-Key` is not a CORS-simple header, so *every* browser request — including plain
  `GET` — triggers a preflight.
- `Access-Control-Allow-Methods` on the one wired path emits `POST-GROUP` /
  `PUT-GROUP` / `DELETE-GROUP`, which are not HTTP methods and mean nothing to a browser.
- Browsers cannot set headers on a `WebSocket` connection at all, so `X-API-Key` can never
  be sent from browser JS.

**Therefore: the frontend must proxy through its own server.** Next.js route handlers,
an Express/Fastify layer, SvelteKit endpoints — any of these. The proxy holds the API key
server-side (never ship it to the browser), forwards REST calls, and terminates the
websocket on the server, re-broadcasting to the browser over its own socket or SSE.

```
browser ──(cookie session)──> your BFF ──(X-API-Key)──> :8080  REST
browser <──(SSE / own WS)──── your BFF <──(X-API-Key)── :8080  WS /ws/{ep}
```

If you do want direct browser access for reads, add the origin to `allowed-origins`, keep
to `GET /{endpoint}` only, and expect to patch the server for the missing OPTIONS routes.

---

## 5. Route map (per model)

`{ep}` is the model's `end-point:` value. It may contain slashes — e.g. `asset/data`
produces `/asset/data`, `/asset/data/group`, `/ws/asset/data`.

| Method | Path                          | Condition            | Purpose                          |
|--------|-------------------------------|----------------------|----------------------------------|
| OPTIONS| `/{ep}`                       | always               | 204 + CORS headers               |
| GET    | `/{ep}`                       | `GET` allowed        | List / filter / paginate         |
| POST   | `/{ep}`                       | `POST` allowed       | Create one                       |
| POST   | `/{ep}/group`                 | `POST-GROUP`         | Create many                      |
| PUT    | `/{ep}`                       | `PUT` allowed        | Update one                       |
| PUT    | `/{ep}/group`                 | `PUT-GROUP`          | Update many                      |
| DELETE | `/{ep}`                       | `DELETE` allowed     | Delete one                       |
| DELETE | `/{ep}/group`                 | `DELETE-GROUP`       | Delete many                      |
| GET    | `/{ep}/fn/{function}`         | `GET` allowed        | Built-in or declarative function |
| GET    | `/{ep}/history/{key}`         | `track-history: true`| Per-record audit log             |
| GET    | `/{ep}/diff`                  | `allow-diff: true`   | Read a stored diff               |
| POST   | `/{ep}/diff`                  | `allow-diff: true`   | Create a diff                    |
| PUT    | `/{ep}/diff?checksum=`        | `allow-diff: true`   | Action a diff → sync instructions|
| GET    | `/{ep}/admin/pending`         | always               | Pending destructive schema change|
| POST   | `/{ep}/admin/approve`         | always               | Approve / deny it                |
| GET    | `/ws/{ep}`                    | always               | Websocket upgrade                |

Models with `end-point: ""` (e.g. the internal `diffs` model) register nothing.

---

## 6. Reading data — `GET /{ep}`

### Response envelope

```json
{
  "task_type": "Get Resource",
  "page": 1,
  "page_size": 0,
  "total_count": 142,
  "data": [ { "asset_id": 1, "asset_no": "CL001" } ]
}
```

Two things to know about the envelope:

- **`page_size` in the response is actually the SQL OFFSET, not the page size.** On page 1
  it is always `0`. Do not use it. Compute paging from `total_count` and the `page_size`
  you requested.
- **`data` rows are keyed by database column name, not by the field's `json:` key.** Query
  parameters use the `json:` key; responses use the column name. These are identical in most
  models, but not all — in the `events` model the column `log` is exposed as the query
  parameter `event_log`, `time` as `event_time`, `user` as `event_user`. Use
  `/fn/schema` to build the mapping rather than assuming.

Fields marked `private: true` in the model are never selected or returned.

### Pagination

| Param       | Default | Notes                                                    |
|-------------|---------|----------------------------------------------------------|
| `page`      | `1`     | 1-indexed                                                 |
| `page_size` | `25`    | Non-integer / negative falls back to 25                   |

`page=all` disables LIMIT/OFFSET entirely and returns everything.

### Field selection

`?fields=asset_no,building` — restricts the SELECT list. Tokens resolve by field name,
`json:` key, or any `json-alias`. Unresolvable or private tokens are silently dropped.

### Sorting

`?sort_by=building~asc,condition_rating~desc` — comma separated, `~asc` / `~desc` suffix,
default ASC. Direction aliases are loose (`d`, `desc`, `descending` → DESC). Unresolvable
tokens are silently dropped.

### Filtering

Any query parameter matching a field's `json:` key becomes a WHERE clause. The operator is
inferred from the value prefix and the field's declared type:

| Prefix | Operator          | Works on          |
|--------|-------------------|-------------------|
| `>`    | greater than      | int, float, time  |
| `>=`   | greater or equal  | int, float, time  |
| `<`    | less than         | int, float, time  |
| `<=`   | less or equal     | int, float, time  |
| none   | see below         | all               |

Default (no prefix) behaviour by type:

| Type     | Becomes                                                              |
|----------|----------------------------------------------------------------------|
| `int`    | `field = <int>`                                                      |
| `float`  | `field = <float>`                                                    |
| `bool`   | `field = <bool>`                                                     |
| `time`   | `field = <parsed date>`; unparseable → `~*` fuzzy match              |
| `string` | `field ~* value` — **case-insensitive regex, i.e. substring search**  |
| `uuid`   | `field ~* value`                                                     |

`absolute-match: true` on a field forces `=` regardless. Note the string default: `?building=A`
matches anything *containing* `A`. Anchor it (`?building=^A$`) or set `absolute-match` on the
field when you need exact matching.

Multiple different fields AND together. The same field twice does **not** produce a range on
the standard GET endpoint — the second value overwrites the first (only the history endpoint
supports `from`/`to` ranges).

Accepted date formats, tried in order:

```
2006-01-02, 2006-01-02T15:04:05Z07:00, 2006-01-02T15:04:05,
2006-01-02 15:04:05, 01/02/2006, 02/01/2006, RFC3339, RFC3339Nano
```

### Soft delete

Models with `soft-delete: true` gain a server-managed `deleted_flag` boolean column
(exposed as `deleted`, aliases `is_deleted` / `deleted_flag`). GET silently filters
`deleted_flag = false` unless you pass `?deleted=all`. DELETE on such a model sets the flag
instead of removing the row.

### Examples

```
GET /asset/data?page=2&page_size=50&sort_by=asset_no~asc
GET /asset/data?condition_rating=>=3&asset_category=PLANT
GET /asset/data?install_date=>=2024-01-01
GET /asset/data?fields=asset_no,description,building
GET /building?deleted=all
```

---

## 7. Writing data

All writes are queued. The HTTP response tells you whether the payload was *accepted*, not
whether it was *applied*.

### `POST /{ep}` — create one

Body: a JSON object keyed by `json:` names (aliases accepted). Unknown keys are ignored.
Fields with `required-on-insert: true` must be present. Fields with `skip-insert: true`
(serials, `now()` / `gen_random_uuid()` defaults) must not be supplied.

Success (`200`):

```json
{
  "task_type": "Add Resource",
  "task_id": "aB3xK9...32 chars",
  "message": "successful submission of task"
}
```

Note the single-create success response carries **no** `rows_*` keys — only the group form
and the other verbs do.

Rejection (`400`), when nothing in the body survived validation:

```json
{
  "task_type": "Add Resource",
  "successful_submission": false,
  "rows_received": 1,
  "rows_valid": 0,
  "rows_invalid": 1,
  "invalid": [
    { "data": { "building": "X" }, "reasons": ["Missing field: db_domain"] }
  ]
}
```

`reasons` is an array of strings for missing-field / missing-key failures, and a single
string for type-coercion or rule-validation failures. Handle both.

### `POST /{ep}/group` — create many

Body: JSON array. Each row is validated independently. Response always includes
`task_id`, `successful_submission`, `rows_received`, `rows_valid`, `rows_invalid`,
`invalid[]`. Valid rows are queued even when some rows are invalid — a partial accept
returns `200`; only a wholly-invalid batch returns `400`.

The insert is a recursive split-batch: on a database error the batch halves and retries
until failing rows are isolated, so one bad row does not sink the batch.

### `PUT /{ep}` and `PUT /{ep}/group` — update

Body must identify the row by a complete key: the primary key, or **all** fields of any one
declared unique key. Everything else in the body is treated as the SET list. Only supplied
fields are written.

```json
{ "asset_no": "CL001", "description": "Pump A — replaced" }
```

Primary keys cannot be updated through the API. To change a unique-key value, identify the
row by primary key.

Single PUT is stricter than the others: if *any* row is invalid it returns `400` and queues
nothing. `rows_received` is hardcoded to `1`.

### `DELETE /{ep}` and `DELETE /{ep}/group` — delete

Body must contain an identifying key, same rule as PUT. Other fields are ignored.

```json
{ "asset_no": "CL001" }
```

On single DELETE, `rows_received` reports the number of keys in the body object, not a row
count. Ignore it.

### Validation rules

Rules declared per field in the model YAML run on every insert and update. Failures land in
`invalid[]` and the row is never written.

| Rule            | Applies to      | Meaning                                   |
|-----------------|-----------------|--------------------------------------------|
| `max-length: N` | string          | Maximum character length                   |
| `pattern: "re"` | string          | Must match the whole value (Go regexp)     |
| `enum: [a, b]`  | string/int/float| Must be one of the listed values           |
| `min: N`        | int, float      | Inclusive minimum (integer literal only)   |
| `max: N`        | int, float      | Inclusive maximum (integer literal only)   |

There is no `precision` rule (earlier docs mention one; it does not exist). `min`/`max` are
parsed as integers even for float fields.

---

## 8. Tracking task outcomes

A write returns a 32-character `task_id`. The task then moves through statuses:

```
queued ──> start ──> success | warn | failed | error
```

(`queued` is only emitted when every worker is busy; there are 5 workers. With a free
worker the task goes straight to `start`.)

| Status    | Meaning                                                        |
|-----------|-----------------------------------------------------------------|
| `queued`  | Accepted, waiting for a worker                                  |
| `start`   | A worker picked it up                                           |
| `success` | Completed, zero failed rows                                     |
| `warn`    | Completed, some rows failed                                     |
| `failed`  | Completed, no rows succeeded                                    |
| `error`   | Internal error, task aborted                                    |

**The only live way to observe this is the websocket.** `GET /events` exists and the model
is loaded, but the event-persistence INSERT in `tools/eventsManager.go` is malformed
(broken placeholders, wrong column count, reserved word `user` unquoted) and its error is
discarded — so nothing is written to the `events` table, and the table has no `task_id`
column to filter on anyway. Do not design a polling flow around `/events`. Subscribe to the
websocket before you POST, or accept fire-and-forget plus a manual refetch.

---

## 9. Websockets

```
ws://<host>:8080/ws/{endpoint}          e.g. ws://localhost:8080/ws/asset/data
```

Upgrade requires `X-API-Key` and the model's **`POST`** role list. Browser JS cannot set
that header — connect from your BFF (Node `ws`, Go, etc.) and fan out to browsers yourself.

You may pre-subscribe with a query parameter on the upgrade (`?status=error`), or send
subscription messages after connecting. Every message is JSON:

```json
{ "instruction": "sub", "topic": "table:asset/data", "action": "create", "status": "any" }
```

| Field         | Values                                                                    |
|---------------|---------------------------------------------------------------------------|
| `instruction` | `sub` \| `unsub` (anything else is silently ignored)                      |
| `topic`       | `table:{end-point}` or `func:{function-name}`                             |
| `action`      | `get` \| `create` \| `update` \| `delete` \| `any` \| `all` \| omitted    |
| `status`      | `queued` \| `start` \| `success` \| `warn` \| `failed` \| `error` \| `any` \| `all` |

**The action for a create is `create`, not `insert`.** The README and `docs/websockets.md`
show `"action": "insert"` — that is rejected. (`on-insert` *is* correct in webhook YAML; the
loader maps it to `create`.) Note also that the topic is built from the model's
**end-point**, not its table name: `table:asset/data`, not `table:asset_data`.

Malformed JSON is dropped silently. An invalid subscription gets a reply:

```json
{"subscribe": false, "reason": "invalid subscription request",
 "is_topic_valid": true, "is_action_valid": false, "is_status_valid": true}
```

A successful one echoes `{"subscribe": true, "topic": "...", "action": "...", "status": "..."}`.
Unsubscribes reply with `{"unsubscribe": true, ...}` and auto-prune empty topics.

The server pings every 27s and expects the pong within ~57s; a standard client library
handles this automatically.

### Event payloads

The `task_type` inside an event payload is the *operation* (`create` / `update` / `delete`),
not the human-readable `task_type` string in the HTTP response (`"Add Resource"`). Don't
correlate on it — correlate on `task_id`.

```jsonc
// status: queued
{ "task_id": "...", "task_type": "create", "task_status": "queued" }

// status: start
{ "task_id": "...", "task_type": "create", "task_status": "start",
  "task_start": "2026-08-03T09:14:02Z", "note": "<X-User-Note>" }

// status: success | warn
{ "task_id": "...", "task_type": "create", "task_status": "success",
  "task_start": "...", "task_end": "...", "note": "...",
  "task_response": {
    "total_count": 3, "success_count": 3, "failed_count": 0,
    "success_items": [ /* inserted rows, or {where_fields, updated_values} for updates */ ],
    "failed_items":  [ { "row": {...}, "error": "..." } ],
    "table_name": "asset_data"
  } }

// status: failed | error
{ "task_id": "...", "task_type": "create", "task_status": "error",
  "task_start": "...", "task_end": "...", "note": "...",
  "task_error": { "error": "..." } }
```

`success_items` shape by action:

- create → the inserted row objects
- update → `{ "where_fields": {...}, "updated_values": {...} }`
- delete → `{ "where_fields": {...} }`

`docs/events.md` documents a different (flatter) payload with top-level `success_items` —
those publisher functions exist in `queueManager.go` but are dead code. The shape above is
what actually goes over the wire.

### Function topics

Registering a declarative function creates a `func:{name}` topic that is also referenced by
its bound table topic. When a table event carries `success_count > 0`, a notification is
propagated to every function topic bound to that table:

```json
{ "event_type": "data altered", "event_time": "...", "function_name": "func:assets-by-building",
  "items_added": 3, "items_updated": 0, "items_removed": 0 }
```

It deliberately carries no data — the server can't know your parameter filters, and pushing
rows would bypass the caller's API-key scope. Treat it as "refetch this function".

Diff tasks do **not** produce websocket events (the task type `diff` isn't a valid action,
and an internal task-type mismatch marks the task errored even when the diff was written).

---

## 10. Functions — `/{ep}/fn/{name}`

Two kinds share the namespace: built-ins implemented in Go, and declarative functions loaded
from `config/functions/*.yaml`. An unknown name returns `400 invalid function`.

Function responses use the standard envelope plus `"function": "<name>"`.

### Built-in: `schema`

```
GET /{ep}/fn/schema
```

No parameters, no SQL. **This is the endpoint to drive dynamic tables and forms.** Private
fields are omitted.

```json
{
  "Name": "Building",
  "Version": "1.3.9",
  "Primary_key": "building_id",
  "Unique_keys": [ ["building"], ["building", "db_domain"] ],
  "Fields": {
    "building": {
      "Type": "string", "JSON": "building", "Required": true,
      "Skip_insert": false, "DB_type": "character varying(15)",
      "Nullable": false, "Default": "", "Rules": null
    },
    "building_description": {
      "Type": "string", "JSON": "building_description", "Required": false,
      "Skip_insert": false, "DB_type": "character varying(200)",
      "Nullable": true, "Default": "", "Rules": null
    }
  }
}
```

Reading it: `Required` = must be present to create. `Skip_insert` = server-generated, never
send it. To update, the body must satisfy `Primary_key` or one full entry of `Unique_keys`.
`Rules` mirrors the YAML validation rules and can be projected straight into client-side
form validation.

### Built-in: `aggregate`

```
GET /{ep}/fn/aggregate?group_by=building&aggregate=count,avg:condition_rating&sort_by=count~desc
```

| Param       | Form                             | Notes                                             |
|-------------|----------------------------------|---------------------------------------------------|
| `group_by`  | `col1,col2`                      | Added to both SELECT and GROUP BY                 |
| `aggregate` | `count,sum:f,avg:f,min:f,max:f`  | `count` → `count(*)`, takes no operand            |
| `sort_by`   | `count~desc,building~asc`        | Must already be in the SELECT list                |

At least one of the three must be supplied, else `400`. Pagination applies. Unresolvable or
private tokens are dropped silently.

### Declarative functions

A saved query with a closed response shape. Two consequences for callers:

- `?fields=` is **ignored** — the function owns its column list.
- Ad-hoc field filtering is **off**. Only parameters the YAML declares produce WHERE
  clauses. Anything else in the query string is ignored (except `page`, `page_size`,
  `sort_by`).

YAML `sort-by` is the primary ordering; a caller's `?sort_by=` appends as a tiebreaker,
skipping tokens the YAML already covers.

Example — `config/functions/assets-by-building.yaml`:

```yaml
---
name: assets-by-building
version: 1.0.0
bound-to: asset/data
description: Count assets per building, optionally filtered by category.

parameters:
  - name: category
    field: asset_category
  - name: status
    field: asset_status

fields: [building]
group-by: [building]
aggregate: [count, avg:condition_rating]
sort-by: [count~desc]
```

```
GET /asset/data/fn/assets-by-building
GET /asset/data/fn/assets-by-building?category=PLANT&page_size=10
```

---

## 11. History — `GET /{ep}/history/{key}`

Available when the model sets `track-history: true`. `{key}` is the value of
`track-history-field` (falling back to the primary key) — for `asset/data` that's the
`asset_no`, e.g. `GET /asset/data/history/CL001`. Note the path order:
`/history/{key}`, not `/{key}/history`.

| Param        | Notes                                                                    |
|--------------|---------------------------------------------------------------------------|
| `page`, `page_size` | Standard pagination                                                |
| `fields`     | Subset of `change_id`, `record`, `changed_by`, `changed_at`, `old_values`, `new_values` |
| `sort_by`    | Same columns; default `changed_at~desc`                                   |
| `changed_by` | Exact username match                                                      |
| `from`, `to` | Inclusive range on `changed_at`; standard date formats                    |

```json
{
  "task_type": "Get Resource History",
  "page": 1, "page_size": 0, "total_count": 42,
  "data": [
    {
      "change_id": 102,
      "record": "CL001",
      "changed_by": "tim",
      "changed_at": "2026-04-30T14:21:09Z",
      "old_values": { "description": "Pump A" },
      "new_values": { "description": "Pump A — replaced" }
    }
  ]
}
```

`old_values` / `new_values` contain only the fields that actually changed (decoded JSONB, not
base64). Inserts have `old_values: null`. Soft deletes appear as a change to `deleted_flag`.

---

## 12. Diff workflow

For models with `allow-diff: true`. Purpose: submit an external dataset, compare it to what
is stored, and get back sync instructions. The endpoints are `auth`-wrapped but **not**
CORS-wrapped — proxy them.

**1. Create.** `POST /{ep}/diff`, body a JSON array of records (same key rules as bulk
insert; each row must carry an identifying key). Returns `task_id`, `rows_received`,
`rows_valid`, `rows_invalid`, `invalid_resources`. The comparison runs asynchronously and
stores a row in `diffs` keyed by an MD5 `checksum`. No websocket event is emitted for this
(see §9), so poll step 2 by `task_id`.

**2. Read.** `GET /{ep}/diff?task_id=...` or `?checksum=...`. Returns a one-element array of
raw `diffs` rows: `diff_id, diff_type, task_id, missing_from_supplied, missing_from_stored,
diffs, generated_by_user, checksum, created, note, batched, batched_date`. An empty array
means the diff hasn't finished (or found no differences — the task completes with
`"no differences found"` and writes nothing).

**3. Action.** `PUT /{ep}/diff?checksum=...` (`checksum` required). Generates a batch code
and returns the sync instructions:

```json
{
  "batch_code": "...",
  "missing_from_supplied": [ /* in DB, absent from your data */ ],
  "missing_from_stored":   [ /* in your data, absent from DB */ ],
  "sync_stored":           [ /* supplied version of rows that differ */ ],
  "sync_supplied":         [ /* stored version of rows that differ */ ]
}
```

Actioning does not mutate anything — apply the instructions yourself via the normal
`/group` write endpoints.

Which fields participate is controlled per field by `include-in-diff` and `absolute-match`;
rows are matched on the model's `diff-comparator` field.

---

## 13. Schema approval — `/{ep}/admin/*`

Editing a model YAML triggers a live Atlas diff. Non-destructive changes apply immediately
and the routes hot-swap. Destructive ones (drops, type narrowing — widening a `varchar` is
not destructive) are held in an in-memory approval gate and the model is **not** reloaded
until resolved. This is a natural dashboard surface.

**`GET /{ep}/admin/pending`** — requires the model's `admin-roles` (or global admin):

```json
{
  "task_type": "Review Pending Table Changes",
  "table": "asset_data",
  "has_changes_pending": true,
  "all_pending_tables": ["asset_data", "buildings"],
  "pending": {
    "table": "asset_data",
    "changes": "human-readable description of the destructive operations",
    "approved": false
  }
}
```

`all_pending_tables` is global, so a single model's endpoint is enough to render a
server-wide queue — but approving happens per table, on that table's own endpoint.

**`POST /{ep}/admin/approve`** with `{"approve": true}` or `{"approve": false}`:

- `true` → marks approved and attempts to commit **every** approved change together.
  - `200 {"approved": true, "applied": true, "message": "approved changes applied"}`
  - `202 {"approved": true, "applied": false, "message": "<error>"}` — stays queued,
    typically waiting on an interdependent change on another table. Render this as
    "pending, blocked", not as a failure.
- `false` → discards it and reverts the YAML file to its last-good snapshot.
  `200 {"approved": false, "applied": false, "message": "change denied and config reverted to last-good"}`

`404` if there is no pending change for that table. `400` if `approve` is missing.

The gate is in-memory: a server restart drops pending changes (the edited YAML is still on
disk and will be re-detected at boot).

---

## 14. Error handling

| Code | When                                                                       |
|------|-----------------------------------------------------------------------------|
| 200  | Success, including partially-invalid batch writes                           |
| 202  | Schema change approved but not yet applied                                  |
| 204  | OPTIONS preflight                                                           |
| 400  | Bad JSON, no valid rows, bad query params, unknown `/fn/` name, missing checksum |
| 401  | Missing/invalid API key, or role not permitted for this endpoint            |
| 403  | Function called but the bound model has GET disabled and no `roles-allowed` |
| 404  | No pending change for this table                                            |
| 405  | Method not enabled for this model — `Allow` header lists what is            |
| 500  | Database or internal error                                                  |
| 503  | Function's bound model isn't loaded                                         |

Error bodies are **plain text** (`http.Error`), not JSON — except validation rejections,
which return the JSON envelope shown in §7. Check `Content-Type` before parsing.

---

## 15. Config files

### `config/server/server.yaml` — hot reloaded on write

```yaml
---
cors:
  allowed-origins:
    - http://localhost:3000        # add your frontend origin here
    - https://api.lotusforge.au
  allowed-headers:
    - X-API-Key
    - Content-Type
    - X-User-Note
  allow-credentials: true

rbac:
  admin-role: administrator        # bypasses every per-endpoint role check
  default-user-role: user          # currently has no effect
  custom-roles:
    - report
```

There is no `allowed-methods` key — allowed methods are derived per model from
`end-points-allowed`.

### `config/base-models/<model>.yaml` — one file per model

Gitignored; a fresh clone starts with none. Validated against
`config/schemas/datamodel.json` (point your editor's YAML LSP at it). Saving a file
revalidates, syncs the schema, and hot-swaps the routes.

```yaml
---
name: Building                    # unique; used in logs, HCL filenames, OpenAPI
version: 1.3.9                    # x.y.z — MUST increase for a hot reload to take effect
table-name: buildings
end-point: building               # URL segment; may contain slashes (asset/data)

track-history: true               # creates <table>_history and the /history/{key} route
track-history-field: building     # which field identifies a record in history
soft-delete: true                 # adds managed deleted_flag; DELETE flips it
allow-diff: false                 # enables /diff routes
diff-comparator: building         # required when allow-diff is true
admin-roles:                      # who may use /admin/pending and /admin/approve
  - admin_buildings

primary-key: building_id

foreign-keys:
  fk_buildings_domain:
    foreign-key-fields: [db_domain]
    foreign-key-target-table: domains
    foreign-key-target-fields: [domain_code]
    foreign-key-on-update: CASCADE      # CASCADE | SET NULL | SET DEFAULT | RESTRICT | NO ACTION
    foreign-key-on-delete: SET NULL

unique-keys:
  uk_buildings_building:
    unique-key-fields: [building]
  uk_buildings_building_domain:
    unique-key-fields: [building, db_domain]

end-points-allowed:               # omit a key to disable it; [] = admin only
  GET: [user, report]
  POST: []
  PUT: []
  DELETE: []
  POST-GROUP: []
  PUT-GROUP: []
  DELETE-GROUP: []

web-hooks:                        # POST the event payload to these URLs
  on-insert:                      # on-get | on-insert | on-update | on-delete | on-any
    success: ["https://hooks.example.com/created"]
    error:   ["https://hooks.example.com/alerts"]
  on-any:
    all:     ["https://hooks.example.com/firehose"]

fields:
  building_id:
    type: int                     # int | float | string | bool | json | time | uuid
    json: building_id             # request/query key; "" or omitted → field is unaddressable
    db-type: smallserial
    skip-insert: true             # server-generated, never accepted from a client
  building:
    type: string
    json: building
    json-alias: [bldg, building_code]
    db-type: character varying(15)
    required-on-insert: true
    absolute-match: true          # force = instead of the default ~* on filters
  building_description:
    type: string
    json: building_description
    db-type: character varying(200)
    nullable: true
    include-in-diff: false        # default true
    rules:
      max-length: 200
      pattern: "^[A-Za-z].*"
  condition_rating:
    type: int
    json: condition_rating
    db-type: smallint
    nullable: true
    rules:
      min: 0
      max: 5
  status:
    type: string
    json: status
    db-type: character varying(20)
    rules:
      enum: [ACTIVE, INACTIVE, PENDING_DISPOSAL]
  api_key:
    type: uuid
    json: ""                      # unaddressable
    db-type: uuid
    default: gen_random_uuid()
    private: true                 # never returned, never filterable, excluded from events
  created:
    type: time
    json: created
    db-type: timestamp without time zone
    default: now()
    skip-insert: true
    migration: alter              # Atlas strategy: alter | skip | recreate
```

Type mapping:

| YAML type | Typical `db-type`                          | JSON representation      |
|-----------|---------------------------------------------|--------------------------|
| `string`  | `character varying(n)`, `text`              | string                   |
| `int`     | `integer`, `bigint`, `smallint`, `serial`, `smallserial` | number      |
| `float`   | `numeric(p,s)`, `decimal`                   | number                   |
| `bool`    | `boolean`                                   | boolean                  |
| `time`    | `timestamp without time zone`, `timestamptz`, `date` | ISO 8601 string |
| `uuid`    | `uuid`                                      | string                   |
| `json`    | `jsonb`                                     | object or array          |

Validation rejects: missing `name` / `version` / `table-name` / `primary-key` / `fields`, a
non-`x.y.z` version, a primary key that names no field, unknown `type` / `db-type` /
`migration`, a nullable primary key, FK/UK entries referencing unknown fields, mismatched
FK field counts, and the reserved field name `deleted_flag`.

### `config/functions/<name>.yaml`

```yaml
---
name: open-disposals            # URL slug; cannot be aggregate, diff, history, group
version: 1.0.0                  # must strictly increase for a hot reload to swap
bound-to: asset/data            # must equal a model's end-point exactly
description: Assets flagged for disposal but not yet completed.
roles-allowed: []               # empty → inherit the bound model's GET list

where:                          # always-applied equality filters, keyed by field name
  asset_status: PENDING_DISPOSAL

parameters:                     # the only user-controllable filters
  - name: department            # ?department=...
    field: department
    op: "="                     # optional: = > >= < <= ~*   (default: type inference)
    required: false             # true → missing param yields 400

fields: [asset_no, description, department, disposal_date]
group-by: []
aggregate: []
sort-by: [disposal_date~asc]
```

Rejected at load: missing `name`/`version`/`bound-to`, a reserved name, a `bound-to` that
matches no model, a duplicate `(bound-to, name)`, any reference to an unknown or `private`
field, an unparseable aggregate token, a `sort-by` token not present in the output, or a
`fields` entry using the future `{name, expression}` calculated form.

Deleting a function or model file is **not** picked up — the route survives until restart.

### `config/default/` — system models

`users.yaml`, `events.yaml`, `diffs.yaml`. Loaded like any other model; don't edit them
without reason. `users` serves `/user` (GET/PUT/POST, admin-only as shipped), `events`
serves `/events` (GET for `user`/`report`), `diffs` has `end-point: ""` and serves nothing.

### Backend environment

```bash
DATABASE_USER=...     # postgres user
DATABASE_PWD=...      # postgres password
DATABASE_URL=...      # host:port/dbname
LOG_TYPE=dev          # "production" for JSON logs
LOG_LEVEL=info        # debug | info | warn | error
go run main.go        # :8080
```

---

## 16. Suggested frontend surface

Everything below is buildable against the API as it stands today.

**Model explorer / generic CRUD.** Fetch `/openapi.json` for the route inventory, then
`/{ep}/fn/schema` per model to generate a table (columns, types, nullability) and a
create/edit form (required fields, `skip-insert` exclusions, `Rules` → client validation).
Because keys are model-driven, one component set covers every model.

**Live task monitor (dashboard).** BFF holds websockets to every `table:{ep}` topic,
subscribed to `action: all, status: all`, and streams to the browser. Render a rolling
feed of task cards keyed by `task_id`: queued → start → terminal status, with
`task_response.success_count` / `failed_count` and the expandable `failed_items[].error`.
This is also the only reliable way to close the loop on an optimistic UI write.

**Throughput / error panel.** Aggregate the same websocket stream client-side: tasks per
minute by action, error rate by table, slowest tasks (`task_end - task_start`). Nothing is
persisted server-side, so keep a bounded in-memory ring buffer in the BFF if you want
history across page loads.

**Schema approval console.** Poll `GET /{ep}/admin/pending` on any admin-visible model for
`all_pending_tables`, then fetch each table's own `/admin/pending` for its `changes` text.
Approve/deny inline; surface `202 applied:false` as "blocked on another table" and offer
approving the rest of the set.

**Audit / history viewer.** `/{ep}/history/{key}` with `from`/`to`/`changed_by` filters,
rendered as a timeline diffing `old_values` against `new_values` per entry.

**Diff review workbench.** Upload a CSV/JSON extract → `POST /{ep}/diff` → poll
`GET /{ep}/diff?task_id=` → present the four buckets side by side with per-row accept
toggles → apply the selections through `/group` writes.

**Reporting views.** `/{ep}/fn/aggregate` for ad-hoc grouping; declarative functions for
saved reports. Function topics tell you when to refetch.

Design notes worth honouring: default string filters are substring regex, so debounce
search inputs and expect `total_count` to shift; write endpoints are eventually consistent,
so either subscribe or refetch after a short delay rather than assuming the row exists; and
`page_size` in responses is unusable, so drive pagination from your own request state.

---

## 17. Client sketches

BFF proxy (Next.js route handler):

```ts
// app/api/[...path]/route.ts
const API = process.env.API_BASE ?? "http://localhost:8080";

async function forward(req: Request, path: string[]) {
  const url = new URL(`${API}/${path.join("/")}`);
  new URL(req.url).searchParams.forEach((v, k) => url.searchParams.append(k, v));

  const res = await fetch(url, {
    method: req.method,
    headers: {
      "X-API-Key": process.env.API_KEY!,          // stays server-side
      "Content-Type": "application/json",
      ...(req.headers.get("x-user-note")
        ? { "X-User-Note": req.headers.get("x-user-note")! }
        : {}),
    },
    body: ["GET", "HEAD"].includes(req.method) ? undefined : await req.text(),
  });

  return new Response(await res.text(), {
    status: res.status,
    headers: { "Content-Type": res.headers.get("content-type") ?? "text/plain" },
  });
}

export const GET = (r: Request, c: { params: { path: string[] } }) => forward(r, c.params.path);
export const POST = GET, PUT = GET, DELETE = GET;
```

Response types:

```ts
type ListResponse<T> = {
  task_type: string;
  page: number;
  page_size: number;      // actually the SQL offset — ignore
  total_count: number;
  data: T[];
};

type InvalidRow = { data: Record<string, unknown>; reasons: string[] | string };

type WriteResponse = {
  task_type: string;
  task_id?: string;
  message?: string;
  successful_submission?: boolean;
  rows_received?: number;
  rows_valid?: number;
  rows_invalid?: number;
  invalid?: InvalidRow[];
};

type TaskEvent = {
  task_id: string;
  task_type: "create" | "update" | "delete" | "get";
  task_status: "queued" | "start" | "success" | "warn" | "failed" | "error";
  note?: string;
  task_start?: string;
  task_end?: string;
  task_response?: {
    total_count: number; success_count: number; failed_count: number;
    success_items: unknown[]; failed_items: { row: unknown; error: string }[];
    table_name: string;
  };
  task_error?: { error: string };
};
```

Websocket from the BFF (Node):

```ts
import WebSocket from "ws";

const ws = new WebSocket("ws://localhost:8080/ws/asset/data", {
  headers: { "X-API-Key": process.env.API_KEY! },   // impossible in browser JS
});

ws.on("open", () => {
  ws.send(JSON.stringify({
    instruction: "sub", topic: "table:asset/data", action: "all", status: "all",
  }));
});

ws.on("message", (raw) => {
  const msg = JSON.parse(raw.toString());
  if ("subscribe" in msg || "unsubscribe" in msg) return;   // ack, not an event
  broadcastToBrowsers(msg as TaskEvent);
});
```

Write-then-await-outcome:

```ts
async function createAsset(row: Record<string, unknown>) {
  const res = await fetch("/api/asset/data", { method: "POST", body: JSON.stringify(row) });
  const body: WriteResponse = await res.json();
  if (!res.ok || !body.task_id) throw new Error(formatInvalid(body.invalid));
  return awaitTask(body.task_id);   // resolves off the streamed TaskEvent for that task_id
}
```

---

## 18. Known quirks — read before debugging

1. `page_size` in every list response is the SQL offset, not the page size.
2. Response rows are keyed by database column; query params use the `json:` key. Reconcile
   via `/fn/schema`.
3. String filters default to case-insensitive regex (`~*`), not equality.
4. Websocket action for creates is `create`, not `insert` (README/docs say otherwise).
5. Websocket topics use the end-point, not the table name: `table:asset/data`.
6. Websocket upgrade authorises against the model's `POST` role list, not `GET`.
7. Nothing is written to the `events` table — the INSERT is malformed and its error is
   swallowed. Use websockets for task outcomes.
8. Diff tasks emit no websocket events and are internally marked errored even on success;
   confirm via `GET /{ep}/diff?task_id=`.
9. `/diff` and `/{ep}/fn/*` routes carry no CORS headers; only `/{ep}` has an OPTIONS route.
10. `end-points-allowed: GET: []` means admin-only, not "everyone".
11. Single POST success returns no `rows_*` fields; single PUT hardcodes `rows_received: 1`;
    single DELETE reports the number of body keys.
12. Error bodies are plain text except validation rejections.
13. A hot reload only takes effect if the file's `version:` strictly increases.
14. Deleting a model or function YAML does not remove its routes until restart.
15. There is no `precision` validation rule; `min`/`max` parse as integers even on floats.
16. `/{ep}/fn/<unknown>` returns `400`, not `404`.
17. Same-field range filters aren't supported on the standard GET (only `from`/`to` on
    history).
18. Pending schema approvals live in memory and are lost on restart.
