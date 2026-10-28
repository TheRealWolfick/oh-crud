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
| `GET /{endpoint}`, `/fn/aggregate`, `/fn/schema`, `/history/{key}` | `end-points-allowed.GET` |
| Declarative function `/fn/{name}` | The function's `roles-allowed`, or the model's `GET` list if that is empty |
| `POST /{endpoint}` / `POST /{endpoint}/group` | `POST` / `POST-GROUP` |
| `PUT /{endpoint}` / `PUT /{endpoint}/group` | `PUT` / `PUT-GROUP` |
| `DELETE /{endpoint}` / `DELETE /{endpoint}/group` | `DELETE` / `DELETE-GROUP` |
| `GET /{endpoint}/diff` | `DIFF` |
| `POST /{endpoint}/diff` | `POST` |
| `PUT /{endpoint}/diff` | `PUT` |
| `GET /{endpoint}/admin/pending`, `POST /{endpoint}/admin/approve` | The model's `admin-roles` |
| `GET /ws/{endpoint}` (websocket) | `POST` (see `websockets.md`) |

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

The discovery document also advertises `/authorize`, `/logout`, and the
`authorization_code` and token-exchange grants. Those are **planned, not
implemented**.

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
| `401` | `unauthorized` | Wrong username or password, disabled user, or a user with no password (such as one who signs in through Entra) |
| `401` | `{"error": "invalid_grant"}` | TOTP code missing or wrong |
| `429` | `{"error": "slow_down"}` | More than 10 attempts for this service and username in 15 minutes |

An unknown username and a wrong password give identical responses.

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
