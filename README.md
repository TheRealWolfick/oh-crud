# Oh CRUD
A general purpose backend designed to abstract away database management and provide
a common interface for any frontend to be able to interface with it.

You describe your data in YAML. The server reads those files at startup, syncs the
PostgreSQL schema to match, generates a full REST endpoint for each model, and
watches the files so changes take effect without a restart. No Go code is written
per model, and no migrations are hand-authored.

A current restriction is users interface can be used to create a new user or manage
an existing one, it is required to go into the database to get the api key. You could
make this field not private in the config and assign a json key, but this is not recommended.

## How it works

1. **Load** — YAML model configs are read from `config/default` and `config/base-models`,
   validated against `config/schemas/datamodel.json`, and registered in memory.
2. **Sync** — Each model is converted to HCL and diffed against the live database with
   [Atlas](https://atlasgo.io). Non-destructive changes apply automatically. Destructive
   ones (drops, type narrowing) are held in an approval gate until someone approves them
   through the admin endpoints.
3. **Serve** — Routes are registered per model, wrapped in CORS and API key auth.
4. **Queue** — Every create/update/delete is enqueued and returns a `task_id` immediately.
   Workers run the generated SQL and emit events as the job progresses.
5. **Watch** — File monitors reload models, declarative functions, and the server config
   on write, hot-swapping the affected handlers in place.

## Quick start

```bash
# Required environment
export DATABASE_USER=...      # PostgreSQL user
export DATABASE_PWD=...       # PostgreSQL password
export DATABASE_URL=...       # host:port/dbname
export LOG_TYPE=dev           # "production" for JSON logs
export LOG_LEVEL=info         # debug | info | warn | error

go run main.go                # listens on :8080
```

```bash
go build -o oh-crud main.go   # build
go test ./...                        # test
```

A running server always exposes:

| Path             | Purpose                                  |
|------------------|------------------------------------------|
| `GET /health`    | Liveness check                           |
| `GET /openapi.json` | Generated OpenAPI spec for all models |

Note that `config/base-models/` is gitignored — a fresh clone starts with no models.
Drop your YAML files in there and restart.

A config file for these datamodels exists: config/schemas/datamodel.json

You can this file with your LSP for building these files.

## Defining a model

```yaml
name: Building
version: 1.0.0
table-name: buildings
end-point: building
allow-diff: false
soft-delete: true
track-history: true
track-history-field: building

primary-key: building_id

unique-keys:
  uk_buildings_building:
    unique-key-fields:
      - building

end-points-allowed:
  GET: []
  PUT: []
  POST: []
  DELETE: []
  PUT-GROUP: []
  POST-GROUP: []
  DELETE-GROUP: []

fields:
  building_id:
    type: int
    json: building_id
    db-type: smallserial
    skip-insert: true
  building:
    type: string
    json: building
    db-type: character varying(15)
    required-on-insert: true
  building_description:
    type: string
    json: building_description
    db-type: character varying(200)
    nullable: true
```

That file alone creates the `buildings` and `buildings_history` tables and the whole `/building` 
endpoint tree. See `docs/config_instructions.md` for every available field.

## Endpoints

Each model with an `end-point` gets the following, where `{endpoint}` is the model's
`end-point:` value:

| Method | Path                             | Purpose                                     |
|--------|----------------------------------|---------------------------------------------|
| GET    | `/{endpoint}`                    | List and filter records                     |
| POST   | `/{endpoint}` · `/{endpoint}/group` | Create one · create many                 |
| PUT    | `/{endpoint}` · `/{endpoint}/group` | Update one · update many                 |
| DELETE | `/{endpoint}` · `/{endpoint}/group` | Delete one · delete many                 |
| GET    | `/{endpoint}/fn/{function}`      | Built-in or declarative function            |
| GET    | `/{endpoint}/history/{key}`      | Per-record audit log (if `track-history`)   |
| GET/POST/PUT | `/{endpoint}/diff`         | Diff workflow (if `allow-diff`)             |
| GET    | `/{endpoint}/admin/pending`      | Pending destructive schema changes          |
| POST   | `/{endpoint}/admin/approve`      | Approve a pending change                    |
| WS     | `/ws/{endpoint}`                 | Event stream for the model                  |

GET requests support pagination (`page`, `page_size`), field selection (`fields=`),
sorting (`sort_by=field~desc`), and per-field filtering with comparison prefixes
(`?condition_rating=>=3`). Full details in `docs/api-guide.md`.

### Authentication

Requests carry an API key in the `X-API-Key` header, which resolves to a user record
holding that user's roles. Access is checked per table, and the admin role — set by
`rbac.admin-role` in `config/server/server.yaml` — gates the schema approval endpoints.

### Async writes

Writes do not block on the database. A successful `POST` returns a `task_id`, and the
job's progress is published as events. Subscribe over a websocket to follow it:

```json
{"instruction": "sub", "topic": "table:building", "action": "insert", "status": "any"}
```

## Docs
Docs can be found inside the /docs folder. These include:
- api-guide: A guide on how to interact with the API and write configuration files for it
- config_instructions: More detailed instructions on the structure of a config file
- events: A list of all the event notifications structure.
- websockets: An instruction on how to connect and utilize websockets

## Project layout

| Package        | Responsibility                                                        |
|----------------|-----------------------------------------------------------------------|
| `handlers/`    | Dynamic CRUD, declarative functions, admin approvals, OpenAPI spec     |
| `models/`      | `DataModel`, type coercion (JSON↔Go↔PostgreSQL), diffs, hot-swap       |
| `tools/`       | Queue manager, config loading, query builder, events, registries       |
| `schematools/` | Atlas HCL generation, schema diffing, destructive-change approval gate |
| `middleware/`  | API key auth, user context, CORS                                       |
| `monitors/`    | fsnotify watchers for models, functions, and server config             |
| `config/`      | YAML model definitions, declarative functions, server config, schemas  |

## Features
- Full database management with approval pathways for destructive changes
- Queue management for all create / update / delete async workflows
- Diffs between supplied data and what is currently in the database
- Full history / audit log
- Webhooks
- Hot reload of models, functions, and server config with no restart
- Declarative YAML functions for custom read endpoints
- Websocket event streams per table and function
- Role-based access control, per table
- Generated OpenAPI spec

## Future Improvements / Planned changes
- Service specification in tables, specifying what service they are for (manual migration 
of existing services required due to change of table names)
- Automated alerts
- Calculated fields
- Cross table end points
- Automated database creation in docker if an end point is not specified
- Database snapshot backups and restoration management.
- Remove default from users config and add it from the server config. Currently, default user
role holds no effect.

## License
LotusForge License, Version 1.0 — see [LICENSE](LICENSE). Third party dependency
licences are collected in [THIRD-PARTY-LICENSES](THIRD-PARTY-LICENSES).
