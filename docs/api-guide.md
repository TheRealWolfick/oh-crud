# API Guide

A user-facing reference for the dynamic REST API exposed by `asset-data-api`.
Covers the URL conventions you'll use against any model endpoint, the built-in
`aggregate` function, the history endpoint, and how to author your own
declarative functions in YAML.

---

## 1. URL conventions

Every model declared in `config/base-models/` (or `special-models/`, `default/`)
exposes the same shape of endpoints:

| Method | Path                                  | Purpose                                    |
|--------|---------------------------------------|--------------------------------------------|
| GET    | `/{endpoint}`                         | List / filter records                      |
| GET    | `/{endpoint}/fn/{function}`           | Built-in or declarative function           |
| GET    | `/{endpoint}/{key}/history`           | Per-record audit log (if `track-history`)  |
| POST   | `/{endpoint}` and `/{endpoint}/group` | Create one / many                          |
| PUT    | `/{endpoint}` and `/{endpoint}/group` | Update one / many                          |
| DELETE | `/{endpoint}` and `/{endpoint}/group` | Delete one / many                          |
| GET    | `/{endpoint}/diff` (POST/PUT)         | Diff workflow (if `allow-diff`)            |

`{endpoint}` is the model's `end-point:` value — for example, `asset/data` for
the `Asset Data` model, so the live URL is `/asset/data`.

---

## 2. Standard URL parameters (GET)

These parameters are honoured by **every** GET endpoint (including
`/{endpoint}` and `/{endpoint}/fn/{function}`), unless a function YAML opts
out of a particular one.

### Pagination

| Parameter   | Default | Notes                                                          |
|-------------|---------|----------------------------------------------------------------|
| `page`      | `1`     | 1-indexed. `page=all` disables pagination entirely.            |
| `page_size` | `25`    | Negative or non-integer values fall back to default.           |

The response always includes `page`, `page_size`, and `total_count` keys so
the caller can compute "has more pages?" without re-issuing the request.

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

## 3. Filter parameters

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
| (none) | equality / fuzzy     | depends on field — see below     |

### Default behaviour by type

| Field type | `?field=value` becomes              |
|------------|-------------------------------------|
| `int`      | `field = value` (parsed as integer) |
| `float`    | `field = value` (parsed as float)   |
| `bool`     | `field = value` (parsed as bool)    |
| `time`     | `field = parsedDate`; if not parseable, falls back to `~*` (case-insensitive regex) |
| `string`   | `field ~* value` (case-insensitive regex match)                     |

If the field has `absolute-match: true` set in its model YAML, the operator
is forced to `=` regardless.

### Examples

```text
GET /asset/data?asset_no=CL001
GET /asset/data?condition_rating=>=3
GET /asset/data?install_date=>=2024-01-01&install_date=<=2024-12-31
GET /asset/data?description=pump
```

---

## 4. The `/fn/{function}` namespace

Per-endpoint functions live under `/{endpoint}/fn/{function}`. Two kinds of
functions are dispatched by this route:

- **Built-in** — implemented in code (`aggregate`).
- **Declarative** — defined in YAML files under `config/functions/` and
  reloaded automatically when the file changes.

When the path component doesn't match a built-in or a registered declarative
function, the response is a 404.

---

## 5. Built-in: `aggregate`

`GET /{endpoint}/fn/aggregate` runs an ad-hoc grouped query. It's most
useful when you need a one-off summary that doesn't justify creating a
declarative function.

### URL parameters

| Parameter  | Form                              | Notes                                                |
|------------|-----------------------------------|------------------------------------------------------|
| `group_by` | `col1,col2`                       | Required when `aggregate` is empty, else optional.   |
| `aggregate`| `count,sum:value,avg:rating,...`  | At least one of `group_by`/`aggregate`/`sort_by` must be supplied. |
| `sort_by`  | `count~desc,col~asc`              | Sortable column must already appear in SELECT.       |

### Aggregate function syntax

| Token        | Renders to                |
|--------------|---------------------------|
| `count`      | `COUNT(*)`                |
| `sum:field`  | `SUM(field)`              |
| `avg:field`  | `AVG(field)`              |
| `min:field`  | `MIN(field)`              |
| `max:field`  | `MAX(field)`              |

`field` must resolve via the same rules as `?fields=` (model field name or
JSON alias, not `private`).

### Example

```text
GET /asset/data/fn/aggregate?group_by=building&aggregate=count,avg:condition_rating&sort_by=count~desc
```

Builds:

```sql
SELECT building, count(*), avg(condition_rating)
FROM asset_data
GROUP BY building
ORDER BY count DESC;
```

Standard pagination params (`page`, `page_size`) apply on top.

---

## 6. History endpoint

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
  "page_size": 0,
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

## 7. Authoring a declarative function

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
                        # Cannot be: aggregate, diff, history, group.

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
```

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

## 8. Worked example — non-aggregating function

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
