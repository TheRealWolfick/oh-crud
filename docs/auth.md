# Authentication

How clients prove who they are to Oh CRUD, and how Oh CRUD decides what they
can do.

Every client that talks to Oh CRUD is a registered **service** with its own
Ed25519 key pair, and proves who it is with a signed client assertion
(section 2). A service calls the data endpoints either as itself, or on behalf
of a user who has logged in through it (section 4).

| Call | Headers | Writes recorded as |
|------|---------|--------------------|
| Service, as itself (background task) | `Authorization: Bearer <service token>` | `svc:<service id>` |
| Service, for a user | `Authorization: Bearer <service token>` and `X-OhCrud-User: <user token>` | The user's username |
| User directly, no service (only where a model sets `access.allow-direct`) | `Authorization: Bearer <user token>` | The user's username |

API keys (`X-API-Key`) are no longer accepted.

---

## 1. Calling the data endpoints

### 1.1 Sending tokens

```http
GET /asset/data?page_size=50 HTTP/1.1
Host: api.example.com
Authorization: Bearer eyJhbGciOiJFZERTQSIsImtpZCI6...   (service token)
X-OhCrud-User: eyJhbGciOiJFZERTQSIsImtpZCI6...          (user token, optional)
```

- The `Authorization` token is the service's own access token from the
  `client_credentials` grant (2.4).
- `X-OhCrud-User` is the user's access token from the `password` or
  `refresh_token` grant (section 4). It is only accepted from the **same
  service the user logged in through**: a user token carries the `client_id`
  it was issued to, so one service cannot use a user token obtained by
  another.
- Refresh tokens are never accepted here.

| Failure | Status | Body (plain text) |
|---------|--------|-------------------|
| No bearer token, or it is invalid, expired, or not an access token | `401` | `unauthorized` |
| `X-OhCrud-User` is invalid, not a user token, or issued to another service | `401` | `unauthorized` |
| `X-OhCrud-User` sent alongside a user token | `401` | `unauthorized` |
| A user token with no service, on a model without `access.allow-direct` | `403` | `forbidden` |
| No user token, on a model with `access.require-user` | `403` | `forbidden` |

### 1.2 Model access rules

Each model can set who may call its routes (`config-instructions.md` 3.2):

```yaml
access:
  require-user: true   # service calls must act for a user (default false)
  allow-direct: false  # accept a user token with no service (default false)
```

With neither set, a service may call as itself or for a user, and users cannot
call directly. Declarative functions follow the model they are bound to.

### 1.3 Role checks (RBAC)

After authentication, each operation checks roles against the model's
`end-points-allowed` (see `config-instructions.md`). **Every party to the
request must pass**: when a service acts for a user, both the service's role
and one of the user's roles must be allowed. A service can never reach further
than its own role allows by acting for a more privileged user, and a user
cannot reach further through a more privileged service.

A party passes when it holds the **admin role** (`rbac.admin-role` in
`config/server/server.yaml`, e.g. `administrator`) or a role listed for the
operation:

| Operation | Roles taken from |
|-----------|------------------|
| `GET /{service}/{endpoint}`, `/fn/aggregate`, `/fn/schema`, `/history/{key}` | `end-points-allowed.GET` |
| Declarative function `/fn/{name}` | The function's `roles-allowed`, or the model's `GET` list if that is empty |
| `POST /{service}/{endpoint}` / `POST /{service}/{endpoint}/group` | `POST` / `POST-GROUP` |
| `PUT /{service}/{endpoint}` / `PUT /{service}/{endpoint}/group` | `PUT` / `PUT-GROUP` |
| `DELETE /{service}/{endpoint}` / `DELETE /{service}/{endpoint}/group` | `DELETE` / `DELETE-GROUP` |
| `GET /{service}/{endpoint}/diff` | `DIFF` |
| `POST /{service}/{endpoint}/diff` | `POST` |
| `PUT /{service}/{endpoint}/diff` | `PUT` |
| `GET /{service}/{endpoint}/ws` (websocket) | `POST` (see `websockets.md`) |

| Failure | Status | Body (plain text) |
|---------|--------|-------------------|
| Operation not enabled on the model (no list in `end-points-allowed`) | `405` | `Not allowed` (the `Allow` header lists what is enabled) |
| Operation enabled but a party holds none of the listed roles | `401` | `User role does not have access to this end point` |

Roles answer "may editors update assets?", not "may this editor update this
asset?". Row-level checks belong in the handler, using the user's `sub`.

### 1.4 Who writes are recorded against

History, diffs and events record a username, and reference `users.username`.

- With a user token, writes are recorded against the user's username (the
  token's `preferred_username` claim). Every user who writes needs a username.
- Without one, writes are recorded against the service's own user row,
  `svc:<service id>`. Oh CRUD creates that row the first time the service gets
  a token. It has no password and no roles, so it can never log in or grant
  access. Because `users.username` is at most 20 characters, service ids must
  be at most 16.

### 1.5 Frontend guidance

- **A browser frontend is a service.** Its private key must stay on a server
  (a backend-for-frontend), never in a public bundle. The backend holds the
  service token and the user's tokens, and attaches both to each call.
- **CORS.** For browser calls from another origin, `Authorization` and
  `X-OhCrud-User` must be in `cors.allowed-headers`, and the frontend's origin
  in `cors.allowed-origins` (`config/server/server.yaml`). Add `X-User-Note`
  too if the frontend sends notes with writes.
- **Websockets.** The browser `WebSocket` API cannot set request headers, so a
  browser cannot send these tokens on the upgrade request. See
  `websockets.md`.

---

## 2. Service authentication (OAuth 2.0)

Backend services authenticate with the OAuth 2.0 **client credentials** grant,
proving their identity with a signed JWT (**`private_key_jwt`**,
RFC 7523) instead of a shared secret. The service holds an Ed25519 private key.
Oh CRUD only ever stores the public half.

### 2.1 Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/oauth/token` | Exchange a client assertion for an access token |
| `GET` | `/.well-known/jwks.json` | Public keys that verify Oh CRUD's access tokens |
| `GET` | `/.well-known/openid-configuration` | Discovery document |

These need no access token. Every grant authenticates the service with a
client assertion (2.3).

| `grant_type` | Purpose |
|--------------|---------|
| `client_credentials` | A token for the service itself (2.4) |
| `password` | Log a user in through the service (4.1) |
| `refresh_token` | Rotate a user's refresh token (4.2) |

The discovery document advertises only what exists: these three grants, the token
endpoint and the JWKS. `POST /oauth/logout` (4.3) is not listed, because the
`end_session_endpoint` field is for browser logout, which arrives with the login page.

### 2.2 Registering a service

The service generates its own Ed25519 key pair and keeps the private key. An
operator registers the public key:

```sql
INSERT INTO services (id, role) VALUES ('svc-reports', 'report');
INSERT INTO service_keys (kid, service_id, public_key)
VALUES ('svc-reports-2026-10', 'svc-reports', decode('<base64 public key>', 'base64'));
```

| Table | Field | Notes |
|-------|-------|-------|
| `services` | `id` | The service id. Used as the assertion's `iss` and `sub`. At most 16 characters (1.4). |
| | `role` | Placed in the access token's `roles` claim. |
| | `disabled_at` | Set it to disable the service; every key stops working. |
| `service_keys` | `kid` | Key id, max 32 chars. Sent in the assertion header. |
| | `public_key` | Raw 32-byte Ed25519 public key. |
| | `revoked_at`, `revoked_reason` | Set `revoked_at` to revoke one key. |
| | `last_used_at` | Updated each time the key authenticates. |

**Rotating a key:** insert a second `service_keys` row for the same service,
switch the service to the new `kid`, then set `revoked_at` on the old row. Both
keys work in the meantime.

The `oh-crud-jwks-test` project contains a small tool that generates a key pair,
prints these inserts, and runs the full flow against a server.

### 2.3 Building the client assertion

The service signs a short-lived JWT with its private key.

**Header**

| Field | Value |
|-------|-------|
| `alg` | `EdDSA` (the only accepted algorithm) |
| `kid` | The registered `service_keys.kid` |

**Claims**

| Claim | Value |
|-------|-------|
| `iss` | The service id |
| `sub` | The service id (must equal `iss`) |
| `aud` | Oh CRUD's token URL, exactly as configured in `token-url` (e.g. `https://api.example.com/oauth/token`) |
| `iat` | Issue time. Required |
| `exp` | Expiry. Required. No more than **5 minutes** after `iat` |
| `jti` | A unique id, e.g. a UUID. Required. Each `jti` can be used **once** |

Oh CRUD allows **30 seconds** of clock skew on `exp` and `iat`. Sign a new
assertion for every token request: reusing one is rejected as a replay.

### 2.4 Requesting a token

```http
POST /oauth/token HTTP/1.1
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
&client_assertion_type=urn%3Aietf%3Aparams%3Aoauth%3Aclient-assertion-type%3Ajwt-bearer
&client_assertion=eyJhbGciOiJFZERTQSIsImtpZCI6...
```

The body must be form-encoded, not JSON.

**Success** (`200`, `Cache-Control: no-store`):

```json
{
  "access_token": "eyJhbGciOiJFZERTQSIsImtpZCI6Im9oYy0yMDI2MTAyNi0...",
  "token_type": "Bearer",
  "expires_in": 600
}
```

`expires_in` is in seconds and comes from `token-lifetime` in the server
config. There is no refresh token: request a new access token, with a new
assertion, shortly before the current one expires.

**Errors** (JSON body `{"error": "<code>"}`):

| Status | `error` | Cause |
|--------|---------|-------|
| `400` | `unsupported_grant_type` | `grant_type` is missing or not one listed in 2.1 |
| `401` | `invalid_client` | Any assertion problem: wrong `client_assertion_type`, unknown/revoked key, disabled service, bad signature, wrong `aud`, expired, `iss` ≠ `sub`, missing `iat`/`jti`, lifetime over 5 minutes, or a reused `jti` |
| `500` | `server_error` | The assertion was valid but creating the service's user row or signing the token failed |

`invalid_client` deliberately does not say which check failed. The reason is
in the server log.

### 2.5 The access token

Access tokens are JWTs signed by Oh CRUD with EdDSA (Ed25519).

**Header:** `alg: EdDSA`, `kid: <Oh CRUD signing key id>`, `typ: at+jwt`.

| Claim | Value |
|-------|-------|
| `iss` | `issuer` from the server config |
| `aud` | `["oh-crud"]` |
| `sub` | `svc:<service id>`, or the user's id |
| `token_use` | `service` or `user` |
| `roles` | `[<service role>]`, or the user's roles |
| `iat`, `nbf`, `exp` | Issue time, not-before, expiry |
| `jti` | Unique token id |

User tokens also carry:

| Claim | Value |
|-------|-------|
| `preferred_username` | The user's username, used to record writes |
| `client_id` | The service the user logged in through |
| `sid` | The login session. Every token from one login shares it |
| `amr` | How the user logged in: `pwd`, plus `otp` with TOTP, or `fed` through an external IdP |
| `auth_time` | When the user last authenticated interactively (Unix seconds). Set at login and kept unchanged through refreshes, so it shows how long ago they proved who they are. Missing on sessions started before it was recorded; treat those as needing a fresh login |

### 2.6 Verifying an access token

Any resource server can verify a token without contacting Oh CRUD again:

1. Fetch `GET /.well-known/jwks.json` (cacheable for 5 minutes). The response is
   `{"keys": [...]}`, each key with `kty: OKP`, `crv: Ed25519`, `x`
   (base64url public key), `kid`, `alg: EdDSA`, `use: sig`.
2. Select the key whose `kid` matches the token header.
3. Check: `alg` is `EdDSA`, `typ` is `at+jwt`, the signature, `iss`, `aud`
   contains `oh-crud`, `exp` (and `iat`/`nbf`).
4. Authorize using `token_use`, `sub` and `roles`.

If a token's `kid` is not in the cached JWKS, refetch it once: Oh CRUD may have
rotated its signing key. Keys being rotated out stay in the JWKS, so tokens
they already signed keep verifying.

---

## 3. Configuration summary

From `config/server/server.yaml` (see `config-instructions.md`):

```yaml
issuer: "https://api.example.com"
token-url: "https://api.example.com/oauth/token"
token-lifetime: 10m

rbac:
  admin-role: administrator
  default-user-role: user
  custom-roles: [report]

cors:
  allowed-origins: [https://app.example.com]
  allowed-headers: [Authorization, X-OhCrud-User, Content-Type, X-User-Note]
```

- `issuer` and `token-url` must be the public URLs clients use, matched
  exactly. `token-url` is the `aud` every service assertion must carry.
- Changing `issuer` invalidates every outstanding access token.

Environment variables used by auth:

| Variable | Purpose |
|----------|---------|
| `OH_CRUD_SECRET` | Exactly 32 bytes. Encrypts Oh CRUD's private signing keys at rest. The server will not start without it, and changing it makes existing signing keys unreadable. |

---

## 4. Users

A user logs in through a service. The service sends the user's credentials
with its own client assertion, so every user session is bound to the service
it was started through.

### 4.1 Logging in (`password` grant)

```http
POST /oauth/token HTTP/1.1
Content-Type: application/x-www-form-urlencoded

grant_type=password&username=alice&password=...&otp=123456
&client_assertion_type=urn%3Aietf%3Aparams%3Aoauth%3Aclient-assertion-type%3Ajwt-bearer
&client_assertion=eyJhbGciOiJFZERTQSIsImtpZCI6...
```

The password is sent as typed, over TLS. Oh CRUD hashes it. `otp` is required
when the user has TOTP set up.

**Success** (`200`, `Cache-Control: no-store`):

```json
{
  "access_token": "eyJhbGciOiJFZERTQSIsImtpZCI6...",
  "token_type": "Bearer",
  "expires_in": 900,
  "refresh_token": "ohc_rt_..."
}
```

| Status | Body | Cause |
|--------|------|-------|
| `401` | `{"error": "invalid_client"}` | The client assertion failed (2.4) |
| `400` | `{"error": "invalid_grant"}` | Wrong username or password, disabled user, a user with no password (such as one who signs in through Entra), or a TOTP code that is missing, wrong or already used |
| `429` | `{"error": "slow_down"}` | More than 10 attempts for this service and username in 15 minutes |

Every login failure gives the identical response, so a caller can't tell which check failed. In particular a wrong TOTP code never confirms that the password was right. Each TOTP code can be used once; a second login with the same code fails.

### 4.2 Refreshing (`refresh_token` grant)

```http
POST /oauth/token HTTP/1.1
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&refresh_token=ohc_rt_...
&client_assertion_type=...&client_assertion=...
```

The response has the same shape as 4.1, with a **new** refresh token.

- Each refresh token works **once**. Store the new one and discard the old.
- Only the service the user logged in through can redeem it.
- Presenting a used refresh token again ends the whole login session: every
  refresh token from that login is revoked and the user must log in again.
  Do not retry a refresh with the same token after a network error. Log the
  user in again instead.
- Refreshing reloads the user, so a disabled account or a change of roles
  takes effect at the next refresh.

| Login | Refresh token lifetime |
|-------|------------------------|
| Password | 14 days, renewed on each refresh |
| External IdP (`amr` contains `fed`) | 1 day from login, not renewed. The user signs in through the IdP again daily |

| Status | `error` | Cause |
|--------|---------|-------|
| `401` | `invalid_client` | The client assertion failed |
| `400` | `invalid_grant` | Unknown, expired, revoked or reused token, the wrong service, or the user was disabled or removed |
| `500` | `server_error` | A database error. Safe to retry with the same token |

### 4.3 Logging out

```http
POST /oauth/logout HTTP/1.1
Authorization: Bearer <service token>
X-OhCrud-User: <the user's access token>
```

Ends the session the user's access token belongs to (its `sid`): its refresh tokens
and access tokens stop working at once, on every instance (4.4). The user's other
sessions carry on. `POST /oauth/logout?all=true` ends every session the user has.

A user can also log themselves out directly, with their own access token as the
bearer and no `X-OhCrud-User`.

| Status | Body | Cause |
|--------|------|-------|
| `204` | | Logged out |
| `401` | `unauthorized` | No valid token |
| `400` | `{"error": "invalid_request"}` | The request carries no user, or the user token has no session |

### 4.4 Revocation

Access tokens can't be recalled once issued, so each instance keeps the recent
revocations in memory and checks every token against them. A token is refused when its user (`user:<id>`), its session (`sid:<sid>`) or, for a
service token, its service (`svc:<id>`) was revoked at or after the moment the token
was issued. A login after a revocation works straight away. (JWT times are whole
seconds, so a token issued in the same second as a revocation is refused too; the
next refresh replaces it.)

| Event | What is revoked |
|-------|-----------------|
| Logout | The session |
| Logout with `all=true` | The user |
| A used refresh token is presented again | The session (`refresh_token.reuse` in the audit log) |
| A refresh finds the user disabled or removed | The user: every session, not only the one refreshing |

**Instances.** Oh CRUD can run as several server processes (instances) behind a load
balancer, sharing one database. Each holds its own copy of the revocations, and they
are kept in step by telling each other, never by polling:

1. The instance that revokes writes a row to `ohc_revocations`, applies it in
   memory, and posts it to every peer in `cluster.peers` (`config-instructions.md` 2).
2. Each peer applies it on receipt. Measured between two instances on one machine,
   a logout on one was refused by the other within 10 ms.
3. An instance reads the table once, when it starts, for the revocations made before
   it was running. Rows only matter while a token issued before them could still be
   live: the longest access token lifetime plus 5 minutes (20 minutes by default).

Peers authenticate each other with a key derived from `OH_CRUD_SECRET`, which they
already share: messages go to `POST /mesh/v1/peer/events`, signed over the whole body
(`X-OhCrud-Peer-Signature`), and are refused if unsigned, altered, or more than a
minute old. Delivery is retried twice (after 1 and 3 seconds); a peer still
unreachable misses the message and logs nothing, while the sender logs
`cluster message not delivered`. A peer that was down learns the revocation from the
table when it starts again; one that stayed up but unreachable for all three attempts
does not, until it restarts. With a single instance there are no peers and nothing is
sent.

Open websockets are closed by the revocation itself: each socket remembers the tokens
it was opened with, and when an instance applies a revocation, its own or a peer's,
it closes every socket whose token the revocation covers, with close code `4401`
(see `websockets.md` 1.3).

---

## 5. Audit log

Security and admin actions are recorded in `ohc_audit_events`, written by Oh CRUD
itself (package `audit`). It has no data endpoint; the admin API will read it.

| Column | Meaning |
|--------|---------|
| `at` | When it happened |
| `action` | What happened, as `<object>.<verb>` (below) |
| `target` | What it happened to: a user id, `svc:<id>`, `svc:<id>/<kid>`, or `username:<name>` for a login naming no known user |
| `service` | The service that owns the target (`ohc` for users, services and keys), so each service's admins can read their own entries |
| `actor_user` | The user who did it. Empty for the system, and for a failed login |
| `via_service` | The service the request came through |
| `amr` | How the actor had signed in |
| `org_id` | Set when the action is inside an organisation |
| `before`, `after` | JSON values before and after the change |
| `ip` | The caller's address, without a port: the first address in `X-Forwarded-For`, else the connection's |
| `request_id` | The caller's `X-Request-Id` when it is 8 to 64 of `A-Z a-z 0-9 . _ -`, else a new random id |

### 5.1 Actions

| Action | When | Notes |
|--------|------|-------|
| `user.login` | A password login succeeds | `after.session` is the session (`sid`) |
| `user.login_failed` | A password login fails | `after.reason`: `unknown_user`, `no_password`, `bad_password`, `disabled`, `otp_missing`, `bad_otp`, `otp_reused`, `otp_error` or `rate_limited`. The caller still gets the one `invalid_grant` response (4.1) |
| `refresh_token.reuse` | A used refresh token is presented again | The session is revoked; no actor |
| `refresh_token.wrong_client` | A refresh token is presented by a service it wasn't issued to | `after.issued_to` names the right service. The token is left alone |
| `session.revoke` | A refresh finds the user disabled or removed | `after.reason`: `user_disabled` or `user_missing`. Every session the user has is ended |
| `user.logout` | The user logs out of one session | `before.session` is the session ended |
| `user.logout_all` | The user logs out of every session | |

A request whose client assertion fails is not recorded: it never reached a user.
Passwords, OTP codes and tokens are never recorded.

### 5.2 Append-only

A trigger (`config/server/audit-append-only.sql`, run at every start) refuses
`UPDATE`, `DELETE` and `TRUNCATE` on the table for every database role, including
the one Oh CRUD connects as, which owns it. Only a role able to drop the trigger can
change past entries.

An action is recorded after it happens. If the write fails it is logged as an
error, and the login or refresh it describes still succeeds.
