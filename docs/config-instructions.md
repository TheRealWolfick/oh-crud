# Config Instructions

Oh CRUD is driven by YAML files. A **model** file describes a database table and
the REST endpoint generated for it, a **function** file describes a saved query
on a model, and the **server** file holds server-wide settings. This document
lists every key, its rules, and how Oh CRUD applies it.

---

## 1. Layout and loading

| Path | Contents | Loaded |
|------|----------|--------|
| `config/default/` | System models (`users`, `events`, `diffs`, `batch_numbers`, `signing_keys`, `services`, `service_keys`) | Startup, watched |
| `config/base-models/` | Your models | Startup, watched |
| `config/special-models/` | Extra models | Watched only (not loaded at startup) |
| `config/functions/` | Declarative functions | Startup, watched |
| `config/server/server.yaml` | Server settings (section 2) | Startup, watched |
| `config/database/` | Generated schema snapshots. Do not edit | Written by Oh CRUD |
| `config/schemas/datamodel.json` | JSON Schema for model files, for editor validation | Not loaded |

Only files ending in `.yaml` are read. On startup each model is validated
(section 5), its table is created or migrated to match, and its routes are
registered.

**Hot reload.** Saving a watched model or function file reloads it **only if
its `version` is higher** than the loaded one. Changes that would destroy data
are held for approval (section 6). Deleting a file is not picked up: restart to
remove a model or function.

### Environment variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `DATABASE_URL` | Yes | `host:port/database` part of the PostgreSQL URL |
| `DATABASE_USER`, `DATABASE_PWD` | Yes | Database credentials |
| `OH_CRUD_SECRET` | Yes | Exactly 32 bytes. Encrypts the token signing keys (`auth.md`) |

The server listens on `:8080`.

---

## 2. Server config (`config/server/server.yaml`)

```yaml
cors:
  allowed-origins:
    - https://app.example.com
  allowed-headers:
    - Authorization
    - X-OhCrud-User
    - Content-Type
    - X-User-Note
  allow-credentials: true

rbac:
  admin-role: administrator
  default-user-role: user
  custom-roles:
    - report

issuer:         "https://api.example.com"
token-url:      "https://api.example.com/oauth/token"
token-lifetime: 10m
```

| Key | Meaning |
|-----|---------|
| `cors.allowed-origins` | Origins allowed to call the API from a browser. Matched exactly, including scheme and port |
| `cors.allowed-headers` | Request headers browsers may send. Include `Authorization`, `X-OhCrud-User` and `Content-Type` for frontends |
| `cors.allow-credentials` | Sets `Access-Control-Allow-Credentials` |
| `rbac.admin-role` | Users with this role pass every permission check |
| `rbac.default-user-role`, `rbac.custom-roles` | Documents the roles in use. Not enforced |
| `issuer` | `iss` of issued access tokens, and base of the discovery URLs. Public HTTPS URL of this server |
| `token-url` | Public URL of `/oauth/token`. Service assertions must use it as `aud` |
| `token-lifetime` | Access token lifetime, as a duration (`10m`, `1h`) |

**Reloading.** The file is watched, but settings take effect at different
times:

| Setting | When a change applies |
|---------|-----------------------|
| `cors`, `token-url`, `token-lifetime` | Immediately |
| `rbac` | When each model's routes are next registered (a model version bump), or on restart |
| `issuer` | Restart. It also invalidates every outstanding access token |

The file is not validated on reload: a typo (for example a zero
`token-lifetime`) is applied as-is.

---

## 3. Models

### 3.1 Example

```yaml
name: Asset Data
version: 1.4.0
description: Physical assets and their condition.
table-name: asset_data
end-point: asset/data
primary-key: asset_id

track-history: true
track-history-field: asset_no
soft-delete: true
allow-diff: true
diff-comparator: asset_no

end-points-allowed:
  GET: [user, report]
  POST: [user]
  POST-GROUP: [user]
  PUT: [user]
  PUT-GROUP: [user]
  DELETE: []          # admin only
  DIFF: [user]
admin-roles: [administrator]

unique-keys:
  uk_asset_no:
    unique-key-fields: [asset_no]

foreign-keys:
  fk_asset_building:
    foreign-key-fields: [building]
    foreign-key-target-table: building
    foreign-key-target-fields: [building]
    foreign-key-target-field-description: building_description
    foreign-key-on-update: CASCADE

fields:
  asset_id:
    type: int
    json: asset_id
    db-type: serial
    skip-insert: true
  asset_no:
    type: string
    json: asset_no
    json-alias: [asset]
    db-type: character varying(15)
    required-on-insert: true
    absolute-match: true
    include-in-diff: true
  condition_rating:
    type: int
    json: condition_rating
    db-type: smallint
    nullable: true
    include-in-diff: true
    rules:
      min: 1
      max: 5
```

### 3.2 Top-level keys

**Metadata**

| Key | Required | Meaning |
|-----|----------|---------|
| `name` | Yes | Unique name, used in logs and generated file names |
| `version` | Yes | `x.y.z`. Must increase for a hot reload to apply |
| `description` | No | Free text. Returned by the `schema` function |
| `meta` | No | Free-form map, returned as-is by the `schema` function. Use it for frontend hints (labels, grouping) |
| `table-name` | Yes | Database table to create/use |
| `end-point` | No | URL path for the API, e.g. `asset/data` → `/asset/data`. Omit or leave empty for a table with no API |

**Flags**

| Key | Meaning |
|-----|---------|
| `track-history` | Record every change in a `{table}_history` table and expose `GET /{end-point}/history/{key}` |
| `track-history-field` | Field that `{key}` refers to in history. Defaults to the primary key |
| `soft-delete` | Deletes set a `deleted_flag` instead of removing the row, and GETs hide deleted rows unless `?deleted=all`. Adds a `deleted_flag` field (`json: deleted`, aliases `is_deleted`, `deleted_flag`) |
| `allow-diff` | Enables the `/diff` routes. Requires `diff-comparator` and at least one field with `include-in-diff: true` |
| `diff-comparator` | Field used to match supplied rows to stored rows in a diff |

**Access**

| Key | Meaning |
|-----|---------|
| `end-points-allowed` | Which operations exist, and which roles may use them (3.3) |
| `admin-roles` | Roles allowed to use `/{end-point}/admin/pending` and `/admin/approve` |
| `access` | Who may call the model's routes: `require-user` (service calls must act for a user) and `allow-direct` (accept a user token with no service). Both default to `false`. See `auth.md` 1.2 |

**Keys and events**

| Key | Meaning |
|-----|---------|
| `primary-key` | Field name of the primary key. Required. Cannot be nullable |
| `unique-keys` | Unique constraints (3.5) |
| `foreign-keys` | Foreign keys (3.6) |
| `indexes` | Non-unique indexes (3.7) |
| `web-hooks` | URLs notified of events (3.4) |
| `fields` | Column definitions (4). At least one required |

### 3.3 `end-points-allowed`

A map of operation → list of roles:

| Operation | Enables |
|-----------|---------|
| `GET` | `GET /{end-point}`, `/fn/aggregate`, `/fn/schema`, `/history/{key}`, the websocket route, and declarative functions without `roles-allowed` |
| `POST` / `POST-GROUP` | Create one / many. `POST` also gates `POST /diff` and opening a websocket |
| `PUT` / `PUT-GROUP` | Update one / many. `PUT` also gates `PUT /diff` |
| `DELETE` / `DELETE-GROUP` | Delete one / many |
| `DIFF` | `GET /diff` |

- **Key absent**: the operation is disabled and returns `405`.
- **Empty list `[]`**: enabled, but only for the admin role.
- **List of roles**: those roles, plus the admin role.

Roles are matched against the comma-separated `roles` of the user (`auth.md`).

### 3.4 `web-hooks`

Each trigger holds a map of status → list of URLs. When an event matches, its
payload (`events.md`) is POSTed as JSON to each URL.

```yaml
web-hooks:
  on-insert:
    success: [https://hooks.example.com/assets/created]
    warn:    [https://hooks.example.com/assets/created]
  on-update:
    error:   [https://hooks.example.com/alerts]
```

| Trigger | Fires on action |
|---------|-----------------|
| `on-insert` | `create` |
| `on-update` | `update` |
| `on-delete` | `delete` |
| `on-get` | `get` (reads publish no events, so never fires) |
| `on-any` | Not currently supported. Do not use |

Status keys: `queued`, `start`, `success`, `warn`, `fail`, `error`, `all`.
Currently only `queued`, `start`, `success`, `warn` and `error` deliver. `fail`
and `all` are accepted but never fire.

### 3.5 `unique-keys`

A map of constraint name → `unique-key-fields` list. Each key can span several
fields. Unique keys can identify rows in update and delete requests.

```yaml
unique-keys:
  uk_building_room:
    unique-key-fields: [building, room]
```

### 3.6 `foreign-keys`

A map of constraint name → definition:

| Key | Required | Meaning |
|-----|----------|---------|
| `foreign-key-fields` | Yes | Fields on this model |
| `foreign-key-target-table` | Yes | Referenced table |
| `foreign-key-target-fields` | Yes | Referenced fields. Same length as `foreign-key-fields` |
| `foreign-key-target-field-description` | No | Field on the target table holding a human-readable label for the referenced record. Returned by the `schema` function for display only |
| `foreign-key-on-update` | No | `CASCADE`, `SET NULL`, `SET DEFAULT`, `RESTRICT`, `NO ACTION` |
| `foreign-key-on-delete` | No | Same values |

### 3.7 `indexes`

A map of index name → definition. Indexes speed up lookups on fields that are
not keys. They don't change the API, and each name must differ from the
`unique-keys` names.

| Key | Required | Meaning |
|-----|----------|---------|
| `index-fields` | Yes | Fields in the index, in order |
| `index-type` | No | `BTREE` (default), `HASH`, `GIN`, `GIST`, `BRIN`, `SPGIST` |
| `index-where` | No | SQL predicate that makes it a partial index |

```yaml
indexes:
  idx_refresh_tokens_user:
    index-fields: [user_id]
    index-where: revoked_at IS NULL
```

Bump `version` to apply an index change (section 6). New indexes are added
automatically. Removing or renaming one drops an index and needs approval.

Write `index-where` the way PostgreSQL stores it (`pg_get_expr(indpred, indrelid)`),
without the outer brackets. A simple predicate like `revoked_at IS NULL` is fine
as written. PostgreSQL brackets each part of a compound predicate, so write
`(a IS NULL) AND (b IS NULL)`, not `a IS NULL AND b IS NULL`. Otherwise every
sync sees the index as changed and silently drops and recreates it. A rebuild
isn't treated as destructive, so it never asks for approval.

---

## 4. Fields

`fields` is a map of **field name** → definition. The field name is the
database column and the key used in API responses.

### 4.1 Required

| Key | Meaning |
|-----|---------|
| `type` | API type: `int`, `float`, `bool`, `string`, `time`, `uuid`, `json`, `byte`, `[]string` |
| `db-type` | PostgreSQL type. Base types accepted: `int`, `integer`, `smallint`, `bigint`, `serial`, `smallserial`, `numeric`, `decimal`, `boolean`, `text`, `varchar`, `character varying`, `char`, `character`, `uuid`, `json`, `jsonb`, `date`, `timestamptz`, `timestamp with time zone`, `timestamp without time zone`, `bytea`. Sizes go in brackets: `character varying(255)`, `numeric(10,2)`. Append `[]` for an array column, e.g. `text[]` |

### 4.2 Naming and defaults

| Key | Meaning |
|-----|---------|
| `json` | Key clients use in request bodies and filters. Empty or omitted means clients cannot set or filter the field |
| `json-alias` | Extra accepted keys for request bodies and filters |
| `default` | Database default, e.g. `now()`, `gen_random_uuid()`, `false` |
| `description` | Free text. Returned by the `schema` function |
| `meta` | Free-form map, returned by the `schema` function |

### 4.3 String lists

A `[]string` field holds a list of strings in a PostgreSQL array column. Use
`db-type: text[]`:

```yaml
  tags:
    type: "[]string"
    db-type: "text[]"
    json: tags
    rules:
      max-length: 30
      pattern: "[a-z0-9-]+"
```

`text[]` is preferred over `varchar(n)[]`. The two store and perform the same,
and the API reads and writes these columns as `text[]`. Limit item length with
the `max-length` rule rather than the column size, so it can change without a
migration. A `varchar[]` column needs PostgreSQL 14 or later for add/remove
updates.

Clients can replace the whole list, or add and remove values without sending
the rest (`api-guide.md`, section 8.3).

### 4.4 Flags

| Key | Default | Meaning |
|-----|---------|---------|
| `nullable` | `false` | Column allows `NULL` |
| `required-on-insert` | `false` | Create requests must supply it |
| `skip-insert` | `false` | Never written by the API (serials, computed columns) |
| `private` | `false` | Never returned, selectable, filterable or sortable, and left out of the schema. Also set `json: ""` so clients cannot write it |
| `absolute-match` | `false` | Filters always use `=` instead of the default case-insensitive regex for strings. `#NULL` / `#NOTNULL` still work |
| `include-in-diff` | `true` | Set `false` to leave the field out of diff comparisons. `allow-diff` needs at least one field set explicitly to `true` |
| `migration` | — | `alter`, `skip` or `recreate`. Validated, but not yet applied: every change goes through section 6 |

`deleted_flag` is reserved and cannot be declared (use `soft-delete`).

### 4.5 `rules`

Validation applied to writes. Failing rows are rejected with the reason.

| Rule | Types | Meaning |
|------|-------|---------|
| `min`, `max` | `int`, `float` | Value bounds |
| | `byte` | Length bounds in bytes |
| `max-length` | `string` | Maximum length in bytes |
| | `[]string` | Maximum length of each item, in bytes |
| `pattern` | `string` | Regular expression the whole value must match |
| | `[]string` | Regular expression every item must match |
| `enum` | `string`, `int`, `float` | Allowed values, written as strings |
| | `byte` | Allowed lengths |

### 4.6 `json-select`

Returns a computed value for a `jsonb` field in GET responses instead of the raw
JSON. Writes still take the raw value.

```yaml
missing_items:
  type: json
  json: missing_items
  db-type: jsonb
  json-select:
    action: count      # only "count" (jsonb_array_length) is supported
    apply-on: get
    treat-as: int      # type of the computed value
```

---

## 5. Validation at load

A model is rejected (logged, not loaded) if:

- `name`, `version` (`x.y.z`), or `table-name` is missing, or there are no
  fields.
- `primary-key` is missing, not a field, or nullable.
- A field has a missing or unknown `type` or `db-type`, an unknown
  `migration`, or is named `deleted_flag`.

`end-point` is optional here, but `config/schemas/datamodel.json` marks it
required, so editors using that schema will flag models without one.
- A foreign key has no target table, empty field lists, lists of different
  lengths, a field that doesn't exist, or an unknown on-update/on-delete action.
- A unique key is empty or names a field that doesn't exist.
- An index is empty, names a field that doesn't exist, has an unknown
  `index-type`, or shares its name with a unique key.
- `allow-diff` is true without a valid `diff-comparator`, or no field has
  `include-in-diff: true`.

---

## 6. Schema changes and approval

When a model's fields change, Oh CRUD migrates its table. Changes that could
lose data (dropping a table, column or index, or changing a column's type,
except widening a `character varying`) are **not** applied automatically: they are held in a pending gate, the old routes keep serving,
and an admin approves or denies them through `GET /{end-point}/admin/pending`
and `POST /{end-point}/admin/approve` (`api-guide.md` section 10). Denying
reverts the config file to its last good version.

---

## 7. Declarative functions

A function is a saved, parameterised query on one model, served at
`GET /{bound-to}/fn/{name}`. Full reference and examples are in `api-guide.md`
sections 11–12.

### 7.1 Top-level keys

| Key | Required | Meaning |
|-----|----------|---------|
| `name` | Yes | URL slug after `/fn/`. Cannot be `aggregate`, `schema`, `diff`, `history`, `group` |
| `version` | Yes | `x.y.z`. Hot reload only applies a higher version |
| `bound-to` | Yes | The model's `end-point` exactly (e.g. `asset/data`, not `Asset Data`) |
| `description` | No | Free text |
| `roles-allowed` | No | Roles that may call it. Empty or omitted inherits the model's `end-points-allowed.GET` |
| `where` | No | Always-applied equality filters: field name → literal |
| `parameters` | No | URL parameters the caller may filter by (7.2) |
| `fields` | No | Fields returned |
| `group-by` | No | Fields to group by. Each must also be in `fields` |
| `aggregate` | No | Aggregate tokens (`count`, `sum:field`, … see `api-guide.md`) |
| `sort-by` | No | Default sort. Caller `sort_by` is appended |
| `web-hooks` | No | Same structure as a model's `web-hooks`, for the function's "data altered" events |

Every function also gets a websocket topic, `func:{name}`.

### 7.2 Parameters

| Key | Required | Meaning |
|-----|----------|---------|
| `name` | Yes | Query-string key (`?<name>=…`) |
| `field` | Yes | Model field it filters |
| `op` | No | Force an operator: `=`, `>`, `>=`, `<`, `<=`, `~*`. Otherwise inferred from the field type, as on the standard GET |
| `required` | No | When `true`, a request without it gets `400` |

### 7.3 Fields

Currently a list of model field names. The calculated form
`{ name: label, expression: <sql> }` is reserved and rejected at load time.
