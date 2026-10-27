# API Guide

A reference for building clients against the Oh CRUD REST API. Oh CRUD
generates its endpoints from YAML model configs (`config-instructions.md`), so
every model exposes the same shape of API. This guide covers how to discover
what's available, read, filter and aggregate data, write data, use the history
and diff workflows, and author declarative functions.

Related docs:

- `auth.md`: API keys, roles, and service tokens. **Read first**: every data
  endpoint needs an `X-API-Key` header.
- `websockets.md` and `events.md`: live updates after writes.
- `config-instructions.md`: how models, fields and permissions are configured.

---

## 1. Getting started

### 1.1 Discovering the API

A client does not need to hard-code models. Use these to find what exists and
what each model looks like:

| Method | Path | Auth | Returns |
|--------|------|------|---------|
| GET | `/openapi.json` | None | OpenAPI 3.0.3 spec of every model endpoint and declarative function, with request/response schemas |
| GET | `/{endpoint}/fn/schema` | API key | The model's fields, types, rules, keys and foreign keys (section 6.2). Use it to build tables and forms |
| GET | `/health` | None | `OK` |

A typical frontend loads `/openapi.json` once to list the models, then calls
`/{endpoint}/fn/schema` for each model it renders.

### 1.2 Every request

```http
GET /asset/data?page_size=50 HTTP/1.1
X-API-Key: <user key>
```

- Send `X-API-Key` on every data request (`auth.md`).
- Send JSON bodies with `Content-Type: application/json`.
- Optionally send `X-User-Note: <text>` on writes. It is stored with the task
  and appears as `note` in events and diffs.

---

## 2. URL conventions

Every model with an `end-point:` exposes the routes below. `{endpoint}` is the
model's `end-point:` value, for example `asset/data`, so the live URL is
`/asset/data`. Each route also needs the matching permission in the model's
`end-points-allowed` (`auth.md`); a route the model doesn't enable answers `405`.

| Method | Path | Purpose | Permission |
|--------|------|---------|------------|
| GET    | `/{endpoint}` | List / filter records | `GET` |
| GET    | `/{endpoint}/fn/aggregate` | Ad-hoc grouped query | `GET` |
| GET    | `/{endpoint}/fn/schema` | Model schema | `GET` |
| GET    | `/{endpoint}/fn/{function}` | Declarative function | function `roles-allowed`, else `GET` |
| GET    | `/{endpoint}/history/{key}` | Per-record audit log (if `track-history`) | `GET` |
| POST   | `/{endpoint}` | Create one record | `POST` |
| POST   | `/{endpoint}/group` | Create many | `POST-GROUP` |
| PUT    | `/{endpoint}` | Update one record | `PUT` |
| PUT    | `/{endpoint}/group` | Update many | `PUT-GROUP` |
| DELETE | `/{endpoint}` | Delete one record | `DELETE` |
| DELETE | `/{endpoint}/group` | Delete many | `DELETE-GROUP` |
| GET    | `/{endpoint}/diff` | List stored diffs (if `allow-diff`) | `DIFF` |
| POST   | `/{endpoint}/diff` | Create a diff against supplied rows | `POST` |
| PUT    | `/{endpoint}/diff?checksum=…` | Action a diff | `PUT` |
| GET    | `/{endpoint}/admin/pending` | Pending schema changes | model `admin-roles` |
| POST   | `/{endpoint}/admin/approve` | Approve / deny a schema change | model `admin-roles` |
| GET    | `/ws/{endpoint}` | Websocket (`websockets.md`) | `POST` |

System models are exposed the same way: `/events` (event log, `events.md`) and
`/user` (user management, admin only).

---

## 3. Request and response conventions

### 3.1 Field names: what you send vs. what you get

Each field in a model has a **field name** (its key under `fields:` in the
config, which is also the database column) and a **`json` name** (plus optional
`json-alias` names). They are often the same, but not always.

| Direction | Keys used |
|-----------|-----------|
| Request bodies (POST / PUT / DELETE) | The field's `json` name or any `json-alias`. The field name is **not** accepted unless it is also the `json` name |
| URL filters, `fields=`, `sort_by=` | Field name, `json` name, or alias |
| Response rows (`data`, events) | The **field name** |

The schema (`/fn/schema`) gives both: `Fields` is keyed by field name, and each
entry's `JSON` is the name to send. A field whose `JSON` is empty cannot be set
or filtered by clients.

**Private fields** (`private: true`) are never returned and cannot be selected,
filtered or sorted on. They do not appear in the schema.

### 3.2 Value formats

| Field `Type` | Send as | Notes |
|--------------|---------|-------|
| `string` | JSON string | |
| `int` | JSON number or numeric string | |
| `float` | JSON number or numeric string | |
| `bool` | `true`/`false` or `"true"`/`"false"` | |
| `time` | RFC 3339 string, e.g. `"2026-10-27T03:12:09Z"` | Writes accept RFC 3339 only. Filters accept more formats (Appendix) |
| `uuid` | 36-character string | |
| `json` | JSON object/array, or a string containing JSON | |
| `byte` | String | Stored as the string's raw bytes |
| `[]string` | JSON array of strings, or a list string such as `"[a,b]"`, `"{a,b}"` or `"a,b"` | Returned as a JSON array. Updates can also add or remove values (section 8.3) |

An empty string for `time`, `uuid` or `json` is stored as `NULL`.

### 3.3 Soft-deleted rows

On a model with `soft-delete: true`, deleting a row sets its `deleted_flag`
instead of removing it, and every GET hides deleted rows. Add `?deleted=all` to
include them.

### 3.4 Errors

| Status | Body | When |
|--------|------|------|
| `400` | Plain text, or a JSON write result (section 8) | Bad URL parameters, bad body, or no valid rows |
| `401` | Plain text | Bad/missing API key, or the user's roles don't allow the operation (`auth.md`) |
| `404` | Plain text | Unknown route |
| `405` | `Not allowed` | Operation not enabled on this model. `Allow` header lists enabled operations |
| `500` | Plain text | Server or query error |

Most errors are plain text (`http.Error`), so check `Content-Type` before
parsing JSON.

---

## 4. Standard URL parameters (GET)

`GET /{endpoint}` returns:

```json
{
  "task_type": "Get Resource",
  "page": 1,
  "page_size": 25,
  "total_count": 312,
  "data": [ { "asset_no": "CL001", "description": "Pump A" } ]
}
```

`data` rows are keyed by field name (section 3.1). `total_count` counts every
matching row, not just this page.

These parameters are honoured by **every** GET endpoint (including
`/{endpoint}` and `/{endpoint}/fn/{function}`), unless a function YAML opts
out of a particular one.

### Pagination

| Parameter   | Default | Notes                                                          |
|-------------|---------|----------------------------------------------------------------|
| `page`      | `1`     | 1-indexed. `page=all` disables pagination entirely.            |
| `page_size` | `25`    | Negative or non-integer values fall back to default. `0` behaves like an absent value. |

The response always includes `page`, `page_size`, and `total_count` keys so
the caller can compute "has more pages?" without re-issuing the request.
`page_size` echoes the applied row limit (`0` when the query is unpaginated).

For a query that returns a **set of distinct rows** — `group_by`, or a bare
`aggregate=distinct:…` with no `group_by` — `total_count` is the number of those
rows (groups / distinct values), not the number of underlying table rows, so the
page math is meaningful. Such queries are also given an implicit `ORDER BY` over
the group-by / distinct columns (appended after any caller `sort_by` as a
tiebreaker) so that `LIMIT`/`OFFSET` paging visits each row exactly once. A bare
scalar aggregate (`count`, `sum:…`, …) with no `group_by` is a single row, so
`total_count` is `1`.

### Field selection

`?fields=col_a,col_b` restricts the SELECT list to the listed model fields.
Tokens that don't resolve (or reference a `private` field) are silently
dropped. **Function endpoints ignore this parameter** — a function declares
its own response shape.

### Sorting

`?sort_by=field~asc,field2~desc` — comma-separated tokens, optional
`~asc`/`~desc` suffix (default `ASC`). Aliases for the suffix are loose:
`d`, `D`, `desc`, `DESC`, `descending` all map to `DESC`. Tokens that don't
resolve are silently dropped.

For function endpoints, the YAML's `sort-by` is the **primary** sort and
your URL `sort_by` appends as a secondary tiebreaker. Tokens that duplicate
a YAML default (by base name) are skipped.

---

## 5. Filter parameters

For the standard GET endpoint, any URL parameter that matches a model field's
`json:` name becomes a WHERE clause. The operator is inferred from the value
prefix and the field's type.

### Operator prefixes

| Prefix | Meaning              | Field types it works for         |
|--------|----------------------|----------------------------------|
| `>`    | greater than         | int, float, time                 |
| `>=`   | greater or equal     | int, float, time                 |
| `<`    | less than            | int, float, time                 |
| `<=`   | less or equal        | int, float, time                 |
| `~`    | contains any of      | []string                         |
| `!`    | contains all of      | []string                         |
| (none) | equality / fuzzy     | depends on field — see below     |

### Default behaviour by type

| Field type | `?field=value` becomes              |
|------------|-------------------------------------|
| `int`      | `field = value` (parsed as integer) |
| `float`    | `field = value` (parsed as float)   |
| `bool`     | `field = value` (parsed as bool)    |
| `time`     | `field = parsedDate`; if not parseable, falls back to `~*` (case-insensitive regex) |
| `string`   | `field ~* value` (case-insensitive regex match)                     |
| `[]string` | `field && list`: rows containing **any** of the listed values       |

For `[]string` fields the value is a comma-separated list, with or without
brackets (`a,b`, `[a,b]` or `{a,b}`). Matching is exact and case-sensitive.
Prefix it with `!` to match rows containing **all** of the values. A list with
unbalanced brackets is ignored.

If the field has `absolute-match: true` set in its model YAML, the operator
is forced to `=` regardless.

### Null / not-null filters

`#NULL` and `#NOTNULL` are reserved sigil values, honored for every field
type and regardless of `absolute-match`:

| Value      | Becomes         |
|------------|------------------|
| `#NULL`    | `field IS NULL`     |
| `#NOTNULL` | `field IS NOT NULL` |

For a non-string field, any other value starting with `#` is dropped (not a
valid int/float/bool/time/uuid literal anyway). For a string field, a value
starting with `#` that isn't exactly `#NULL`/`#NOTNULL` is still searched as
free text via `~*` (e.g. `?tag=#urgent` matches literal `#urgent`) — only the
two exact sigil values are reserved.

### Examples

```text
GET /asset/data?asset_no=CL001
GET /asset/data?condition_rating=>=3
GET /asset/data?install_date=>=2024-01-01&install_date=<=2024-12-31
GET /asset/data?description=pump
GET /asset/data?install_date=#NULL
GET /asset/data?description=#NOTNULL
GET /asset/data?tags=pump,urgent        # tagged pump or urgent
GET /asset/data?tags=![pump,urgent]     # tagged pump and urgent
```

---

## 6. The `/fn/{function}` namespace

Per-endpoint functions live under `/{endpoint}/fn/{function}`. Two kinds of
functions are dispatched by this route:

- **Built-in** — implemented in code (`aggregate`, `schema`).
- **Declarative** — defined in YAML files under `config/functions/` and
  reloaded automatically when the file changes.

When the path component doesn't match a built-in or a registered declarative
function, the response is a `400` (`invalid function`).

---

### 6.1 Built-in: `aggregate`

`GET /{endpoint}/fn/aggregate` runs an ad-hoc grouped query. It's most
useful when you need a one-off summary that doesn't justify creating a
declarative function.

#### URL parameters

| Parameter  | Form                              | Notes                                                |
|------------|-----------------------------------|------------------------------------------------------|
| `group_by` | `col1,col2`                       | Required when `aggregate` is empty, else optional.   |
| `aggregate`| `count,sum:value,avg:rating,...`  | At least one of `group_by`/`aggregate`/`sort_by` must be supplied. |
| `sort_by`  | `count~desc,col~asc`              | Sortable column must already appear in SELECT.       |

#### Aggregate function syntax

| Token        | Renders to                |
|--------------|---------------------------|
| `count`      | `COUNT(*)`                |
| `sum:field`  | `SUM(field)`              |
| `avg:field`  | `AVG(field)`              |
| `min:field`  | `MIN(field)`              |
| `max:field`  | `MAX(field)`              |
| `distinct:field`            | `DISTINCT(field)`         |
| `distinct:field1~field2~…`  | `DISTINCT(field1,field2,…)`|

`field` must resolve via the same rules as `?fields=` (model field name or
JSON alias, not `private`).

`distinct` also accepts a `~`-separated list of fields — e.g.
`distinct:building~floor` renders `distinct(building,floor)`. Fields that
don't resolve are dropped; the token is kept as long as at least one
resolves.

A `distinct:` token used **without** `group_by` produces one row per distinct
value (or per distinct tuple, for the `~` form). Such a query is treated like a
grouped one: it gets an implicit `ORDER BY` on the distinct column(s) so
`page`/`page_size` walk the distinct rows deterministically, and `total_count`
is the number of distinct rows. `page=all` returns them all.

#### Example

```text
GET /asset/data/fn/aggregate?group_by=building&aggregate=count,avg:condition_rating&sort_by=count~desc
```

Builds:

```sql
SELECT building, count(*), avg(condition_rating)
FROM asset_data
GROUP BY building
ORDER BY count DESC, building ASC
LIMIT 25;
```

(`building ASC` is the implicit tiebreaker; `LIMIT 25` is the default page.)

Standard pagination params (`page`, `page_size`) apply on top, defaulting to
`page_size=25`. The grouped query carries an implicit `ORDER BY building` (all
group-by columns) so paging is deterministic, and `total_count` reports the
number of buildings rather than the number of asset rows. Pass `page=all` to
retrieve every group in one response.

---

### 6.2 Built-in: `schema`

`GET /{endpoint}/fn/schema` returns the model's schema without querying the
database. Use it to build tables and forms dynamically. It takes no URL
parameters. Private fields are left out.

#### Top-level keys

| Key | Meaning |
|-----|---------|
| `Name`, `Version`, `Description` | Model metadata from the config |
| `Meta` | Free-form `meta:` map from the config (`null` if unset). Use it for frontend hints |
| `Table_name` | Backing PostgreSQL table |
| `Primary_key` | Field name of the primary key |
| `Unique_keys` | List of field-name groups, each a unique constraint |
| `Foreign_keys` | List of foreign keys (below). Empty list if none |
| `Track_history`, `Track_history_field` | Whether `/history/{key}` exists, and which field `{key}` refers to (empty means the primary key) |
| `Fields` | Map of **field name** → field schema (below) |

#### Field schema

| Key | Meaning |
|-----|---------|
| `Type` | `string`, `int`, `float`, `bool`, `time`, `uuid`, `json`, `byte` or `[]string` (section 3.2) |
| `JSON` | Key to use in request bodies. Empty means clients can't set it |
| `Required` | Must be supplied on create (`required-on-insert`) |
| `Skip_insert` | Never written by the API (e.g. serial ids, `deleted_flag`). Don't show it on create forms |
| `Nullable` | May be `null` |
| `Default` | Database default expression, as text (e.g. `now()`, `gen_random_uuid()`) |
| `DB_type` | PostgreSQL column type, e.g. `character varying(15)`. The number is the max length |
| `Description` | Field description from the config |
| `Rules` | `null`, or validation rules: `Min`, `Max`, `Max_length`, `Pattern`, `Enum` (each `null` when unset) |
| `Meta` | Free-form `meta:` map for the field |
| `Select_override`, `Select_expression` | Set only for `json-select` fields: GET returns `Select_expression` (e.g. a JSON array's length) typed as `Select_override.TreatAs` instead of the raw `jsonb`. Writes still take the raw value |

Rules are enforced on writes. A row that breaks one is returned in `invalid`
(section 8):

| Rule | Applies to |
|------|-----------|
| `Min`, `Max` | `int`/`float`: the value. `byte`: the length in bytes |
| `Max_length` | `string`: the length in bytes. `[]string`: the length of each item, in bytes |
| `Pattern` | `string`: a regular expression the whole value must match. `[]string`: every item must match |
| `Enum` | `string`/`int`/`float`: the allowed values, as strings (render as a select). `byte`: allowed lengths |

#### Foreign keys

Each entry in `Foreign_keys` has `Fields` (local fields), `Target_table`,
`Target_fields`, `ON_UPDATE` / `ON_DELETE` (`null` when unset) and
`Target_field_description`: a field on the target table holding a
human-readable label for the referenced record (`null` when unset). It is for
display only. To fill a dropdown, query the target model and show
`Target_field_description` next to the key.

#### Creating and updating with the schema

- **Create**: send every `Required` field, skip `Skip_insert` fields.
- **Update / delete**: identify the row by the primary key, or by every field
  of one unique key (section 8). Primary keys cannot be changed through the API.
  To change a unique key's value, identify the row by its primary key. Serial
  primary keys plus unique keys for natural identifiers work best.

#### Example

```text
GET /asset/building/fn/schema
```

```json
{
  "Name": "Building",
  "Version": "1.3.6",
  "Track_history": false,
  "Track_history_field": "",
  "Description": "",
  "Meta": null,
  "Table_name": "building",
  "Primary_key": "building_id",
  "Foreign_keys": [
    {
      "Fields": ["db_domain"],
      "Target_table": "domain",
      "Target_fields": ["db_domain"],
      "Target_field_description": "domain_description",
      "ON_UPDATE": "CASCADE",
      "ON_DELETE": null
    }
  ],
  "Unique_keys": [["building"], ["building", "db_domain"]],
  "Fields": {
    "building": {
      "Type": "string",
      "JSON": "building",
      "Required": true,
      "Skip_insert": false,
      "Description": "",
      "DB_type": "character varying(15)",
      "Nullable": false,
      "Default": "",
      "Rules": null,
      "Meta": null,
      "Select_override": null,
      "Select_expression": ""
    }
  }
}
```

---

## 7. History endpoint

For models that opt in via `track-history: true`, every record exposes an
audit log at `GET /{endpoint}/history/{key}`, where `{key}` is the
`track-history-field` value (commonly the human-readable identifier — for
`asset/data` it's `asset_no`).

### URL parameters

| Parameter    | Form                | Notes                                              |
|--------------|---------------------|----------------------------------------------------|
| `page`       | int                 | Standard pagination.                               |
| `page_size`  | int                 |                                                    |
| `fields`     | csv                 | Subset of: `change_id`, `record`, `changed_by`, `changed_at`, `old_values`, `new_values`. |
| `sort_by`    | csv                 | Default `changed_at~desc`.                         |
| `changed_by` | username            | Equality filter.                                   |
| `from`       | date                | `changed_at >= from`. Accepts the same date formats as the standard GET endpoint. |
| `to`         | date                | `changed_at <= to`.                                |

### Response shape

```json
{
  "task_type": "Get Resource History",
  "page": 1,
  "page_size": 25,
  "total_count": 42,
  "data": [
    {
      "change_id": 102,
      "record": "CL001",
      "changed_by": "tim",
      "changed_at": "2026-04-30T14:21:09Z",
      "old_values": { "description": "Pump A" },
      "new_values": { "description": "Pump A — replaced 2026-04-30" }
    }
  ]
}
```

`old_values` and `new_values` only contain fields that actually changed. The
reference field (`asset_no` for asset_data) is omitted unless the change
itself updated it.

---

## 8. Writing data

Writes are **asynchronous**. The request is validated, queued as a task, and
answered immediately with a `task_id`. The result arrives as events
(`events.md`), or can be seen by re-reading the data.

### 8.1 Bodies

| Request | Body |
|---------|------|
| `POST /{endpoint}` | One object: the new record |
| `POST /{endpoint}/group` | Array of objects |
| `PUT /{endpoint}` | One object: a key plus the fields to change |
| `PUT /{endpoint}/group` | Array of such objects |
| `DELETE /{endpoint}` | One object: a key |
| `DELETE /{endpoint}/group` | Array of keys |

Keys in the body are `json` names or aliases (section 3.1). Unknown keys are
ignored.

- **Create** rows must include every `required-on-insert` field.
- **Update and delete** rows must include a **key**: the primary key, or every
  field of one unique key. If the primary key is present it is used, otherwise
  a complete unique key. Every other field in an update row is a value
  to set. Delete ignores non-key fields.

```http
PUT /asset/data
Content-Type: application/json
X-User-Note: Corrected description

{"asset_no": "CL001", "description": "Pump A (replaced)"}
```

### 8.2 Response

```json
{
  "task_type": "Update Resource",
  "task_id": "k2Xc9...32 chars",
  "successful_submission": true,
  "rows_received": 1,
  "rows_valid": 1,
  "rows_invalid": 0,
  "invalid": []
}
```

`successful_submission: true` means the task was **queued**, not that it
succeeded: individual rows can still fail when the task runs (for example on a
unique constraint) and are reported in the task's `warn`/`failed` event.

`POST /{endpoint}` (single create) returns only `task_type`, `task_id` and
`message` on success.

`invalid` lists rows rejected before queuing:

```json
"invalid": [
  { "data": { "description": "no key" },
    "reasons": ["Missing at least one valid unique identifying construct: [asset_no, building+room]"] }
]
```

`data` is the row as sent. `reasons` is a list of strings for missing
required/key fields, or a single string for type and rule failures.

| Outcome | Status |
|---------|--------|
| At least one valid row (group requests) | `200`, invalid rows listed and skipped |
| No valid rows | `400` with the same JSON shape, `successful_submission: false` |
| `PUT /{endpoint}` (single) with an invalid row | `400` |
| Body is not valid JSON | `400` or `500`, plain text |

### 8.3 Updating string lists

A `[]string` field can be updated two ways.

**Replace the list** by sending the full list, as for any other field:

```json
{"asset_no": "CL001", "tags": ["pump", "urgent"]}
```

**Add or remove values** by sending an object with `add`, `remove`, or both:

```json
{"asset_no": "CL001", "tags": {"add": ["urgent"], "remove": ["draft"]}}
```

- Added values already in the list are skipped. New values go on the end and
  the existing order is kept.
- Removing a value that isn't in the list does nothing.
- An empty (`NULL`) list is treated as `[]`.
- Each of `add` and `remove` accepts the same formats as a full list (section 3.2).

Prefer add/remove when more than one client may edit the same record. The
change is applied by the database against the current list, so two clients
adding different values at the same time both keep their value. Replacing the
list overwrites whatever another client saved in between.

The row is rejected (returned in `invalid`) when:

- the object has a key other than `add` or `remove`
- `add` and `remove` are both missing or empty
- the same value appears in both `add` and `remove`
- an added value breaks the field's `max-length` or `pattern` rule. Removed
  values are not checked

When creating a record, `add` is used as the starting list and `remove` is
ignored.

The update's success item and history entry record the field's resulting
list, not the `add`/`remove` object that was sent.

---

## 9. Diff workflow

Models with `allow-diff: true` can compare a full dataset supplied by the
client (for example a spreadsheet export) against what is stored, then return
the changes needed to bring either side in sync. Rows are matched on the
model's `diff-comparator` field, and only fields with `include-in-diff: true`
are compared.

1. **Create**: `POST /{endpoint}/diff` with an array of rows (keys as in
   section 8). The response has a `task_id`, `rows_received`, `rows_valid`,
   `rows_invalid` and `invalid_resources`. The diff is computed in the
   background and stored.
2. **List**: `GET /{endpoint}/diff` returns stored diffs for this model, with
   the normal paging and filters. Each has a `checksum`.
3. **Action**: `PUT /{endpoint}/diff?checksum=<checksum>` (no body) returns:

```json
{
  "batch_code": "1042",
  "missing_from_supplied": [ { … } ],
  "missing_from_stored":   [ { … } ],
  "sync_stored":   [ { … } ],
  "sync_supplied": [ { … } ]
}
```

| Key | Meaning |
|-----|---------|
| `missing_from_supplied` | Rows stored but not in the supplied data |
| `missing_from_stored` | Rows supplied but not stored |
| `sync_stored` | Supplied versions of changed rows: apply these to update the store |
| `sync_supplied` | Stored versions of changed rows: apply these to update the source |
| `batch_code` | A batch number assigned the first time the diff is actioned. Repeating the call returns the same code |

Actioning a diff does not write to the model: the client applies the changes
with normal writes.

---

## 10. Admin: schema changes

When a model config changes in a way that would destroy data, the change is
held for approval instead of applied. Users with a role in the model's
`admin-roles` (or the admin role) can review and approve it:

- `GET /{endpoint}/admin/pending` returns `table`, `has_changes_pending`,
  `all_pending_tables`, and, when pending, `pending: {table, changes, approved}`.
- `POST /{endpoint}/admin/approve` with `{"approve": true}` applies the change
  (`applied: true`, or `202` with `applied: false` if it couldn't be applied
  yet). `{"approve": false}` reverts the config to the last good version.
  Returns `404` if nothing is pending.

---

## 11. Authoring a declarative function

Drop a `.yaml` file into `config/functions/`. The file is read at startup
and re-read when modified — you don't need to restart the server.

### Minimum example

```yaml
---
name: assets-by-building
version: 1.0.0
bound-to: asset/data
description: Count assets per building, optionally filtered by category.

parameters:
  - name: category
    field: asset_category

fields: [building]
group-by: [building]
aggregate: [count]
sort-by: [count~desc]
```

That's all that's required. The function becomes available at:

```text
GET /asset/data/fn/assets-by-building
GET /asset/data/fn/assets-by-building?category=PLANT
GET /asset/data/fn/assets-by-building?page=2&page_size=10&sort_by=building
```

### Full schema reference

```yaml
name: <string>          # Required. URL slug — final segment after /fn/.
                        # Cannot be: aggregate, schema, diff, history, group.

version: <semver>       # Required. Hot-reloads only swap the handler when
                        # the new version is strictly greater.

bound-to: <end-point>   # Required. Must match an existing model's `end-point:`
                        # value exactly (e.g. asset/data, not Asset Data).

description: <string>   # Optional but recommended. Free text.

where:                  # Optional. Always-applied equality filters. Field
                        # keys are model field names; values are literals.
  is_active: true
  asset_status: ACTIVE

parameters:             # Optional. URL params accepted by the function.
  - name: <url-key>     # Required. The query-string key (?<name>=...).
    field: <field-name> # Required. The model field the value filters on.
    op: <operator>      # Optional. One of =, >, >=, <, <=, ~* (forces operator;
                        # otherwise the model field's type-driven inference
                        # applies, exactly like the standard GET endpoint).
    required: <bool>    # Optional. When true, missing param → 400.

fields:                 # Optional. Plain SELECT columns.
  - asset_no            # Bare strings reference model fields directly.
  - building
  # Future: { name: full_label, expression: <sql expr> } for calculated
  # fields. v1 rejects entries with `expression` at load time.

group-by:               # Optional. Each token must resolve to a model field
  - building            # (and not be `private`). Adds GROUP BY.

aggregate:              # Optional. Aggregate expressions added to SELECT.
  - count               # See "Aggregate function syntax" above.
  - sum:value
  - avg:condition_rating

sort-by:                # Optional. Default ordering. User ?sort_by= appends.
  - count~desc
  - building~asc

roles-allowed: []       # Optional. List of role names allowed to call this
                        # function. When empty/omitted, inherits the bound
                        # model's end-points-allowed.GET list.

web-hooks:              # Optional. Webhooks for this function's "data altered"
  on-update:            # events, same structure as a model's web-hooks
    success: [https://hooks.example.com/assets]   # (config-instructions.md).
```

Every function also gets a websocket topic, `func:<name>` (`websockets.md`).

The response has the same shape as `GET /{endpoint}`, with `task_type:
"Get Function"` and a `function` key holding the function name.

### Validation rules (enforced at load time)

A function is rejected (and logged) if any of the following fail:

- `name`, `version`, or `bound-to` are missing.
- `name` is one of the reserved words above.
- `bound-to` doesn't match any loaded model's `end-point`.
- A duplicate `(bound-to, name)` already exists from another file.
- Any field reference (`where` keys, `parameters[].field`, `fields`,
  `group-by`, `aggregate` operands, `sort-by` tokens) names a field that
  isn't on the bound model **or** is marked `private`.
- An `aggregate` token doesn't parse (e.g. `total:value`, `sum:`,
  `count:foo`).
- A `sort-by` token doesn't resolve to an aggregate, a group-by column, or
  a declared `fields` entry.
- A `fields` entry uses the calculated form (`expression:`) — reserved for
  a future release.

### Closed-shape contract

A function defines its response shape. Two consequences:

- The standard `?fields=` URL param is **ignored** — callers can't widen
  or narrow the column list.
- The standard field-driven WHERE inference is also off. Only your
  declared `parameters` produce WHERE clauses. If you want a filter to be
  callable from the URL, add it under `parameters:`.

### Pagination

Declarative functions are **unbounded by default** — with no `page`/`page_size`
in the URL the function returns every matching row. This differs from the
standard GET endpoint (which defaults to 25) because a silent cap would quietly
truncate aggregated results and the caller has no signal that rows were dropped.

An explicit `?page=` / `?page_size=` (or `?page=all`) is still honoured, and
`total_count` is group-aware for aggregating functions, exactly as on the
built-in `aggregate`.

### Hot reload

Saving a function YAML triggers a reload:

1. The file is re-parsed and re-validated.
2. If valid and `version:` is greater than the currently registered one,
   the route is swapped atomically — in-flight requests finish on the old
   handler, new requests use the new spec.
3. Validation failures or unchanged versions log and leave the previous
   handler in place.

Deletion of a function file is **not** picked up automatically (a known
limitation shared with model files). Restart the server to fully drop a
function.

---

## 12. Worked example — non-aggregating function

Functions don't have to aggregate. A "saved filter" is just `where` plus
`parameters` plus `fields`:

```yaml
---
name: open-disposals
version: 1.0.0
bound-to: asset/data
description: Assets flagged for disposal but not yet completed.

where:
  asset_status: PENDING_DISPOSAL

parameters:
  - name: department
    field: department

fields: [asset_no, description, department, disposal_date, disposal_cost]
sort-by: [disposal_date~asc]
```

Live URL:

```text
GET /asset/data/fn/open-disposals
GET /asset/data/fn/open-disposals?department=ENG&page_size=50
```

Builds:

```sql
SELECT asset_no, description, department, disposal_date, disposal_cost
FROM asset_data
WHERE asset_status = 'PENDING_DISPOSAL'
  AND department = $1
ORDER BY disposal_date ASC
LIMIT 25 OFFSET 0;
```

---

## Appendix — Date formats accepted

The string→date parser tries each of these in order:

```
2006-01-02
2006-01-02T15:04:05Z07:00
2006-01-02T15:04:05
2006-01-02 15:04:05
01/02/2006
02/01/2006
RFC3339 / RFC3339Nano
```

If none parse, the value is treated as a string and matched with the
fuzzy `~*` operator on time fields.
