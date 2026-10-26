# TODO

Code issues found while updating the docs (2026-10-27). Each one is currently
documented as existing behaviour, so update the listed docs when it is fixed.

## Priority

- [ ] **Websocket subscriptions bypass RBAC.** A client connected to one
  endpoint's websocket can `sub` to any topic (e.g. `table:other/table`) and
  receive its events. Roles are only checked on the initial upgrade.
  `events/eventsManager.go` `registerUnsafe` only checks the topic exists.
  Fix: check the user's roles for the target topic's model on every `sub`.
  Docs: `websockets.md` 2.1.

- [ ] **CORS missing on several routes.** `/fn/{name}` (declarative functions,
  `handlers/functionHandler.go`) and `/diff` routes are not wrapped in
  `middleware.Cors`. `OPTIONS` preflight is only registered for
  `/{endpoint}`, not `/group`, `/fn/…`, `/history/…`, `/diff`. Browser calls
  to those from another origin will fail. Also, `Access-Control-Allow-Methods`
  lists `POST-GROUP`, `PUT-GROUP`, `DELETE-GROUP`, which are not HTTP methods.
  Docs: `api-guide.md`, `config-instructions.md` 2.

## Bugs

- [ ] **Websocket upgrade checks the `POST` role** instead of `GET`
  (`handlers/dataHandler.go` `websocketRegister`). The route itself requires
  `GET` to be enabled. Docs: `websockets.md` 1.1, `auth.md` 1.3,
  `config-instructions.md` 3.3.

- [ ] **`on-any` webhooks panic at startup.** `EnableTopic` writes to
  `i.webhook_url["any"]`, which is a nil map (`"any"` is not in
  `ValidActions`). Docs: `config-instructions.md` 3.4.

- [ ] **Webhook statuses `fail` and `all` never fire.** The YAML key `fail`
  maps to `"fail"` but events use `"failed"`, and `Callback` never looks up
  `"all"`. Docs: `config-instructions.md` 3.4.

- [ ] **Diff tasks always report `failed`.** `store.createDiff` returns
  `success_count` as `int64` (`cmdtag.RowsAffected()`), so the `.(int)` check
  in `queue.work` fails. Also `create_diff` is not in `events.ValidActions`, so
  diff events can never reach websocket subscribers (only the `events` table).

- [ ] **`rbac` changes in `server.yaml` don't apply on reload.** Handlers are
  built with `svr_cfg.Get()` at registration, so they hold a snapshot. Pass the
  `SwappableServerConfig` through and call `Get()` per request.
  Docs: `config-instructions.md` 2.

## Smaller

- [ ] `invalid[].reasons` is a `[]string` for missing-field errors but a
  `string` for coercion/rule errors (`datamodel/maps.go`). Pick one.
  Docs: `api-guide.md` 8.2.

- [ ] `models.DeleteSuccessItem` has no JSON tags, so delete events use
  `WhereFields` while update events use `where_fields`.
  Docs: `events.md` 3.

- [ ] Field `migration` (`alter`/`skip`/`recreate`) is validated but never
  used by `schematools`. Docs: `config-instructions.md` 4.3.

- [ ] `config/schemas/datamodel.json` marks `end-point` as required, but the
  loader allows it to be empty (system tables rely on that).
  Docs: `config-instructions.md` 5.

## Related, already known

- Data endpoints only accept `X-API-Key`; OAuth access tokens from
  `/oauth/token` are not accepted yet (`middleware/auth.go`). Docs: `auth.md`.
- Browsers can't send `X-API-Key` on a websocket upgrade; needs token or
  ticket based websocket auth. Docs: `websockets.md` 1.1.
- Server config is not validated before being swapped in on reload.
