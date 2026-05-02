# Asset Data API — Integration Reference

This document covers everything needed to build a frontend against this API and to define new data models via configuration files.

---

## Overview

This is a configuration-driven REST API. Data models are declared as YAML files — the server reads them at startup, auto-generates HTTP endpoints, validates the database schema, and hot-reloads when YAML files change. No code changes are needed to add a new table and its endpoints.

The server runs on port `8080`.

---

## Authentication

Every request (except `/health`) requires two headers:

| Header | Value |
|---|---|
| `X-Username` | The user's username |
| `X-API-Key` | The user's API key |

A `401` is returned for missing, invalid, or unauthorised credentials.

---

## Response patterns

### GET — synchronous

Data is returned immediately:

```json
{
  "task_type": "Get Resource",
  "page": 1,
  "page_size": 25,
  "total_count": 142,
  "data": [
    { "id": "...", "field": "value" }
  ]
}
```

### POST / PUT / DELETE — asynchronous

All write operations are queued. The API returns a `task_id` immediately and processes the operation in the background:

```json
{
  "task_type": "Add Resource",
  "task_id": "a1b2c3d4...",
  "successful_submission": true,
  "rows_received": 1,
  "rows_valid": 1,
  "rows_invalid": 0,
  "invalid": []
}
```

To check whether a task completed or errored, query the `events` endpoint and filter by `task_id`. Events of type `TASK_CREATE`, `TASK_COMPLETE`, and `TASK_ERROR` are written for every task.

### Validation failures

If rows fail field validation they are returned in the `invalid` array with reasons. The request still returns `200` — check `rows_invalid` and `successful_submission`.

### Optional note header

Any write request can carry a free-text annotation attached to the task log:

```
X-User-Note: Imported from external sync
```

---

## CRUD endpoints

`{endpoint}` is the `end-point` value in the model's YAML config.

---

### `GET /{endpoint}`

Returns paginated records. Supports filtering, sorting, and field selection.

**Pagination**

| Parameter | Default | Notes |
|---|---|---|
| `page` | `1` | Page number |
| `page_size` | `25` | Records per page |

Set `page=all` to return all records with no limit.

**Sorting**

```
?sort_by=field~asc,other_field~desc
```

Multiple fields separated by commas. Direction must be `asc` or `desc`.

**Field selection**

```
?fields=id,name,status
```

Only the listed fields are returned. Private fields are always excluded.

**Filtering**

Any field name can be used as a query parameter:

```
?status=active&building=A
```

---

### `GET /{endpoint}/aggregate`

Returns aggregated/grouped data. Count is an independant aggregation and does not accept any additional field parameters 

| Parameter | Required | Example |
|---|---|---|
| `group_by` | No | `building` |
| `aggregate` | Yes | `count,avg:rating,sum:cost,min:floor,max:floor` |
| `sort_by` | No | `count~desc` |

Supported functions: `count`, `avg`, `sum`, `min`, `max`.

```
GET /assets/aggregate?group_by=building&aggregate=count,avg:condition_rating&sort_by=count~desc
```

---

### `POST /{endpoint}`

Create a single record. Body is a JSON object.

```json
{ "name": "Widget A", "status": "active", "rating": 8.5 }
```

Required fields are defined per model. Supplying unknown or private fields is ignored.

---

### `POST /{endpoint}/group`

Create multiple records. Body is a JSON array. Each item is validated individually.

```json
[
  { "name": "Widget A", "status": "active" },
  { "name": "Widget B", "status": "inactive" }
]
```

---

### `PUT /{endpoint}`

Update a single record. The body must include the primary key or a declared unique key to identify the row. Only supplied fields are updated.

```json
{ "id": "uuid-here", "status": "inactive" }
```

---

### `PUT /{endpoint}/group`

Update multiple records. Body is a JSON array. Each item must include an identifying key.

---

### `DELETE /{endpoint}`

Delete a single record. The body must include the primary key or a declared unique key.

```json
{ "id": "uuid-here" }
```

---

### `DELETE /{endpoint}/group`

Delete multiple records. Body is a JSON array. Each item must include an identifying key.

---

## Diff endpoints

Available only on models with `allow-diff: true` in their YAML config. Used to compare a supplied dataset against what is stored and produce sync instructions.

### `POST /{endpoint}/diff`

Submit a dataset to diff against stored records. Returns a `task_id` and (once complete) a `checksum` that identifies this diff.

Body: JSON array of records (same format as bulk insert).

### `GET /{endpoint}/diff`

Retrieve a stored diff.

| Parameter | Description |
|---|---|
| `task_id` | Filter by the task that created the diff |
| `checksum` | Filter by specific diff checksum |

### `PUT /{endpoint}/diff?checksum={checksum}`

Action a stored diff — returns sync instructions. `checksum` is required.

```json
{
  "batch_code": "...",
  "missing_from_supplied": [...],
  "missing_from_stored": [...],
  "sync_stored": [...],
  "sync_supplied": [...]
}
```

- `missing_from_supplied` — records in the database not present in the submitted data
- `missing_from_stored` — records in the submitted data not present in the database
- `sync_stored` / `sync_supplied` — records that exist in both but have field differences, one copy per side

---

## System endpoints

| Endpoint | Auth required | Description |
|---|---|---|
| `GET /health` | No | Returns `OK` |
| `GET /openapi.json` | No | OpenAPI 3.0.3 spec generated dynamically from all loaded models |

---

## Creating a model config (YAML)

Model files live in:

- `config/base-models/` — standard user-defined models
- `config/special-models/` — models that need to coexist without affecting each other
- `config/default/` — system models (users, events, diffs, event_types) — do not edit

On save, the server validates the file, syncs the database schema (non-destructive changes are applied automatically; destructive changes require manual approval), and hot-reloads the endpoints.

The valid schema file lives in:
- `config/schemas`

### Minimal example

```yaml
name: widget
version: 1.0.0
table-name: widgets
end-point: widgets
primary-key: id

end-points-allowed:
  GET: true
  POST: true
  PUT: true
  DELETE: true

fields:
  id:
    type: uuid
    json: id
    db-type: uuid
    default: gen_random_uuid()
    skip-insert: true
    nullable: false

  name:
    type: string
    json: name
    db-type: varchar(255)
    required-on-insert: true
    nullable: false
```

### Full field reference

```yaml
name: my-model                  # Human-readable model name (used in logs and OpenAPI)
version: 1.0.0                  # Semver — increment when changing fields
table-name: my_table            # PostgreSQL table to read/write
end-point: my-model             # URL path segment (e.g. /my-model)
primary-key: id                 # Field name of the primary key
allow-diff: false               # Set true to enable diff endpoints
diff-comparator: id             # Field used to match records when diffing

foreign-keys:                   # Optional — declares FK relationships
  site_id: sites.id

unique-keys:                    # Optional — fields that are unique per row
  code: true

end-points-allowed:
  GET: true
  POST: true
  PUT: true
  DELETE: true
  POST_GROUP: true              # Enable bulk create
  PUT_GROUP: true               # Enable bulk update
  DELETE_GROUP: true            # Enable bulk delete

fields:
  id:
    type: uuid
    json: id                    # JSON key used in requests and responses
    db-type: uuid
    required-on-insert: false
    nullable: false
    default: gen_random_uuid()
    skip-insert: true           # Exclude from INSERT (server-generated values)

  name:
    type: string
    json: name
    db-type: varchar(255)
    required-on-insert: true
    nullable: false
    rules:
      max-length: 255
      pattern: "^[A-Za-z].*"   # Regex the value must match

  rating:
    type: float
    json: rating
    db-type: numeric(4,2)
    nullable: true
    rules:
      min: 0
      max: 10
      precision: 2              # Max decimal places

  status:
    type: string
    json: status
    db-type: varchar(50)
    nullable: false
    rules:
      enum: [active, inactive, pending]   # Value must be one of these

  created_at:
    type: time
    json: created_at
    db-type: timestamptz
    nullable: false
    default: now()
    skip-insert: true

  metadata:
    type: json
    json: metadata
    db-type: jsonb
    nullable: true

  include-in-diff: true         # Include this field when comparing diffs
  absolute-match: false         # Use exact equality in diffs (vs type-aware comparison)
  migration: alter              # Atlas migration strategy: alter | skip | recreate
```

### Field types

| YAML type | PostgreSQL type | JSON representation |
|---|---|---|
| `string` | `varchar(n)`, `text` | string |
| `int` | `integer`, `bigint` | number |
| `float` | `numeric(p,s)`, `real` | number |
| `bool` | `boolean` | boolean |
| `time` | `timestamptz` | string (ISO 8601) |
| `uuid` | `uuid` | string |
| `json` | `jsonb` | object or array |

### Validation rules

Rules are evaluated on every insert and update. Rows that fail are returned in `invalid` and are not written to the database.

**Strings**

| Rule | Description |
|---|---|
| `max-length: N` | Maximum character length |
| `pattern: "regex"` | Value must match this regular expression |
| `enum: [a, b, c]` | Value must be one of the listed options |

**Numbers (int and float)**

| Rule | Description |
|---|---|
| `min: N` | Minimum value (inclusive) |
| `max: N` | Maximum value (inclusive) |
| `precision: N` | Maximum decimal places (floats only) |

---

## Server configuration

`config/server/server.yaml` controls server-level settings and is hot-reloaded on save.

```yaml
cors:
  allowed-origins:
    - "https://myapp.example.com"
    - "http://localhost:3000"
  allowed-methods:
    - GET
    - POST
    - PUT
    - DELETE
```
