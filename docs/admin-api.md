# Admin API

`/admin/v1` is what the management platform uses to show and manage Oh CRUD's users,
services, sessions, audit log, logs and pending schema changes.

---

## 1. Access

Every route needs a **user** holding the platform admin role (`rbac.admin-role` in
`server.yaml`), sent one of two ways:

- Through a service listed in `admin.services` (the management platform): its service
  token as `Authorization: Bearer`, and the user's token in `X-OhCrud-User`.
- Directly: the user's own access token as the bearer.

A service alone (no user), a service not in `admin.services`, or a user without the
role is refused with `403`. The per-service schema route also lets in that service's
admin role, `<service>:admin`.

Every answer carries `X-Request-Id`. Errors are RFC 9457 problem details:

```json
{"type": "about:blank", "title": "Not an administrator", "status": 403,
 "instance": "/admin/v1/users", "request_id": "evyAeRj3mQUkZw5CZgzUjFkr"}
```

A `500` says only "Something went wrong"; the error is in the logs under the same
`request_id` (`GET /admin/v1/logs?request_id=...`).

### 1.1 The first admin

A new instance has nobody to sign in as. So when an instance starts and there are no
users yet, Oh CRUD creates one platform admin:

| | |
|--|--|
| Username | `admin` (display name `admin`) |
| Role | `rbac.admin-role` from `server.yaml` |
| Password | `OH_CRUD_ADMIN_PASSWORD` when set (at least 12 characters). Otherwise 24 random characters, printed once to the console (stderr) and nowhere else |

The password is temporary: the first login must choose a new one (`auth.md` 4.1). Do
that straight away: until then, whoever has the temporary password can choose it
instead.

- **Only once.** Any user at all, including a disabled one or one who signs in through
  Entra, means it is skipped. Service rows (`svc:<id>`) aren't users and don't count,
  so an instance only services have used still gets one. Several instances starting
  together create one admin between them.
- **Recorded** in the audit log as `user.create` with no actor and `after.reason:
  bootstrap`. The password never is, and is never written to the logs.
- **Refusals stop the start.** With no `rbac.admin-role`, or an `OH_CRUD_ADMIN_PASSWORD`
  under 12 characters, the instance logs why and exits rather than start with nobody
  able to administer it. Neither is checked once there are users.
- **Afterwards**, remove `OH_CRUD_ADMIN_PASSWORD` from the environment. It is ignored
  from then on, but it's still a password left lying around.
- **Lost the printed password?** If nobody has signed in yet, delete the row
  (`DELETE FROM ohc_users WHERE username = 'admin'`) and start an instance again to
  get a new one.

## 2. Lists

Lists answer `{"items": [...], "next_cursor": "..."}`. Pass `cursor=<next_cursor>` to
get the next page; it is empty on the last one. `limit` is 50 by default, at most 200.

Times are RFC 3339. Filters taking a time also take a date (`2026-10-28`); as an upper
bound a date includes the whole day.

## 3. Routes

| Route | Returns |
|-------|---------|
| `GET /admin/v1/overview` | The dashboard's first screen (3.1) |
| `GET /admin/v1/services?q=` | Services by id, with status and key counts. `q` searches the id and role |
| `GET /admin/v1/services/{id}` | One service, its keys, and what its role can reach |
| `GET /admin/v1/users?status=&q=` | Users by username, with each tab's count (3.2) |
| `GET /admin/v1/users/{id}` | One user, their sessions, and their last 20 audit entries |
| `GET /admin/v1/audit?...` | The audit log, newest first (3.3) |
| `GET /admin/v1/roles/{role}/access` | What a role can reach (3.4) |
| `GET /admin/v1/schema/pending` | Destructive schema changes waiting for approval |
| `GET /admin/v1/services/{svc}/schema/pending` | The same, for one service's tables. Open to `<svc>:admin` too |
| `GET /admin/v1/logs?...` | Application logs, newest first (3.5) |

### 3.1 Overview

"Today" is the UTC day.

| Field | Meaning |
|-------|---------|
| `tiles.services` | `total`, `idle` (no key has signed for `admin.idle-days`, default 30, or ever), `disabled` |
| `tiles.people` | `total` users (not service rows), and those who `signed_in_today` |
| `tiles.sessions_ended_today` | Sessions ended by the system today, and how many of those were `token_reuse` |
| `tiles.admin_changes_today` | Audit entries made by a user that aren't logins or logouts, and how many of those were made `with_otp` |
| `recent_changes` | The last 5 audit entries |
| `needs_you` | `{kind, id, title}` to open: idle services, admins with no second factor, users whose sessions were ended by token reuse in the last day, pending schema changes |
| `key_health` | Each service's `last_signed_at` and status |

### 3.2 Users

| `status` | Lists |
|----------|-------|
| `all` (default) | Everyone |
| `active` | Not disabled, not a service row |
| `sessions_ended` | Active users who had a session ended by token reuse or a revocation in the last 7 days |
| `disabled` | Disabled users |
| `service` | The rows a service's own writes are recorded against (`svc:<id>`). A service gets one when it first takes a token |

`q` searches the username, id, display name and roles. Each user has `has_password`,
`has_otp`, `needs_new_password` (they must choose a new password at their next login),
`last_login_at` (from the audit log), `live_sessions` and `status`.

A user's sessions are their refresh token families: `session` (the `sid`), the
`service` it was started through, `amr`, `started_at`, `expires_at` and `state`
(`live`, `revoked` or `expired`).

### 3.3 Audit

| Parameter | Matches |
|-----------|---------|
| `actor` | `actor_user`, a user id |
| `action` | Exactly, or by prefix with a trailing `*`: `service_key.*`, `user.login*` |
| `target` | Exactly: `usr_…`, `svc:…`, `username:…` |
| `service` | The service that owns the target |
| `request_id` | One request |
| `from`, `to` | Time range |

`format=csv` downloads up to 10,000 matching entries as `audit.csv` instead.

### 3.4 Role access

Read from every loaded model's `end-points-allowed` and `admin-roles`, and every
function's `roles-allowed` (a function with none inherits its model's `GET`):

```json
{"role": "editor", "platform_admin": false,
 "models": [{"service": "inventory", "path": "/inventory/building", "model": "Building",
             "methods": ["GET", "POST"], "admin": false}],
 "functions": []}
```

`platform_admin` is true for `rbac.admin-role`, which passes every check whatever the
lists say.

### 3.5 Logs

| Parameter | Matches |
|-----------|---------|
| `level` | This level and above: `debug`, `info`, `warn`, `error` |
| `service` | Who logged it (`ohc` for Oh CRUD) |
| `request_id` | One request |
| `q` | Text in the message |
| `since`, `until` | Time range |

Logs have no id, so the cursor is the time of the last row shown; rows logged in the
same microsecond as a page boundary can be skipped.

---

## 4. Changes

Each change is written in one transaction with its audit entry (`auth.md` 5), so it
never happens unrecorded. Anything that follows it, such as ending sessions or refusing
tokens, runs once it has committed. Bodies are JSON; unknown fields are refused.

| Route | Who | Does |
|-------|-----|------|
| `POST /admin/v1/services` | platform | Registers a service: `{"id", "role", "entra_client_id"?, "key"?: {"kid", "public_key"}}`. `201` |
| `PATCH /admin/v1/services/{id}` | platform | `{"role"}`. The service's current tokens are refused, so the new role applies at once |
| `POST /admin/v1/services/{id}/keys` | platform | Adds a key: `{"kid", "public_key"}`. `201` |
| `POST /admin/v1/services/{id}/keys/{kid}/revoke` | platform | `{"reason"?}`. The key can't sign again, and every token the service holds now is refused (a leaked key may have got them); it asserts again with another key |
| `POST /admin/v1/services/{id}/disable`, `/enable` | platform | Disabling refuses the service's tokens and stops it getting new ones |
| `POST /admin/v1/users` | platform | `{"username", "display_name"?, "password"?, "roles"?}`. Without a password the user can't sign in with one. With one, they must replace it at their first login. `201` |
| `POST /admin/v1/users/{id}/resetpw` | platform | `{"password"}`: a temporary password the user must replace at their next login. Ends every session the user has. A user without a password (Entra, say) is a `409` |
| `PUT /admin/v1/users/{id}/roles` | platform, or a service admin (below) | `{"roles": [...]}`, the whole list |
| `POST /admin/v1/users/{id}/disable`, `/enable` | platform | Disabling ends every session and stops sign-in |
| `POST /admin/v1/users/{id}/sessions/revoke` | platform | Ends every session the user has |
| `POST /admin/v1/users/{id}/sessions/{session}/revoke` | platform | Ends one session (`session` from `GET /users/{id}`) |

Each answers with the service or user as its `GET` route shows it.

- **Service ids** follow the model rule: 2 to 32 lower case letters or digits, not
  `ohc` or a reserved name. A `kid` is 1 to 32 of `A-Z a-z 0-9 . _ -`. A public key is
  the 32 byte Ed25519 key in base64 (standard or URL, padded or not).
- **Passwords** set here are at least 12 characters, and are temporary: the user
  chooses their own at their next login (`auth.md` 4.1). Tell them the temporary
  password some other way; it is never shown again or recorded.
- **Roles.** A platform admin can give or take any role. The admin of a service
  (`<service>:admin`) can give or take only that service's roles (`<service>:...`),
  including appointing other admins of it. Removing a role refuses the user's current
  access tokens, so it applies at once; their next refresh carries the new list
  without signing in again. A role change touching one service's roles is filed in the
  audit log under that service, so its admins see it.
- **Lock-out.** Nobody can disable themselves. The last active holder of an admin role
  (the platform role or a `<service>:admin`) can't lose it or be disabled: `409`.
- **Conflicts.** A service id, key id or username that already exists is a `409`.

### 4.1 Step-up

With `admin.step-up-minutes` above 0 (off by default), every change needs the user to
have signed in with a one-time code within that many minutes. Otherwise it answers
`401` with `WWW-Authenticate: Bearer error="insufficient_user_authentication",
max_age=<seconds>` (RFC 9470): sign the user in again with their code, then retry.

### 4.2 Schema changes

A destructive schema change (dropping a column, say) waits for a decision. List them
with `GET /admin/v1/schema/pending`.

| Route | Who | Does |
|-------|-----|------|
| `POST /admin/v1/schema/{table}/approve` | platform, the table's `<service>:admin`, or a role in the model's `admin-roles` | Approves and applies it |
| `POST /admin/v1/schema/{table}/deny` | the same | `{"reason"?}`. Drops it and puts the model's file back to its last-good version |
| `POST /admin/v1/services/{svc}/schema/approve` | platform or `<svc>:admin` | Approves everything pending for the service, applied together |

Approving applies every approved change in one go, so changes that depend on each
other go in together. If that fails (an approved change needs one still pending), the
changes stay approved and queued, and the answer is `202` with `applied: false` and
the reason. `table` is the database name, `<service>_<table-name>`.

Each decision is recorded as `schema.approve` or `schema.deny`, filed under the
table's service. Atlas applies the change in its own transaction, so the entry is
written straight after it, saying whether it applied.

With several instances, each holds its own pending changes and model files. The one
that decides tells the others on the cluster bus (`auth.md` 4.4): on an approval they
clear the change and load the new model; on a denial they revert their own copy of
the file too.
