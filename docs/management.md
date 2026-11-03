# Management platform

The management platform is Oh CRUD's admin dashboard: users, services and keys,
sessions, the audit log and pending schema changes. It runs as its own service
(`cmd/management`), registered with Oh CRUD like any other, and uses the admin API
(`admin-api.md`).

It is a backend-for-frontend. It holds the service's private key, signs people in
through Oh CRUD's password grant (with their one-time code if they use one), keeps
their tokens on the server, and forwards the dashboard's calls to `/admin/v1`. The
browser only holds a session cookie (`HttpOnly`, `SameSite=Strict`).

---

## 1. Setting it up

1. Make its key pair:

   ```bash
   go run ./cmd/management keygen -kid management-1 -out management.pem
   ```

   This writes the private key (keep it secret) and prints how to register the public
   half: through `POST /admin/v1/services`, or, for the very first one, two SQL
   inserts.

2. List it in Oh CRUD's `server.yaml`, so it may call the admin API for people:

   ```yaml
   admin:
     services: [management]
   ```

3. Run it:

   ```bash
   OHC_URL=https://api.example.com \
   OHC_TOKEN_AUDIENCE=https://api.example.com/oauth/token \
   MGMT_KID=management-1 MGMT_KEY_FILE=management.pem \
   go run ./cmd/management
   ```

| Variable | Required | Meaning |
|----------|----------|---------|
| `OHC_URL` | Yes | Oh CRUD's base URL |
| `OHC_TOKEN_AUDIENCE` | No | Oh CRUD's `token-url` from its `server.yaml`. Default `OHC_URL` + `/oauth/token` |
| `MGMT_KID`, `MGMT_KEY_FILE` | Yes | The registered key id, and the private key `keygen` wrote |
| `MGMT_SERVICE_ID` | No | Its service id at Oh CRUD. Default `management` |
| `ADDR` | No | Address to listen on. Default `:8090` |
| `MGMT_INSECURE_COOKIE` | No | `true` drops the cookie's `Secure` flag, for local development over plain http only |

Only people with the platform admin role (or, for their own service's roles and
schema changes, `<service>:admin`) can use it: Oh CRUD checks every call.

## 2. How it keeps people safe

- **Tokens stay on the server.** The browser never sees an access or refresh token.
- **Refreshing is serialised per session.** Refresh tokens are single use, and Oh CRUD
  ends the whole session if one is used twice, so concurrent requests wait for one
  refresh rather than racing.
- **Changes need the page's CSRF token and the same origin.** Another site can't make
  a change for a signed-in person.
- **Only the admin API is reachable**, by a clean path: encoded `..` and slashes are
  refused.
- **A refused token is retried once** with fresh tokens (a removed role, say); if that
  fails too, the person is signed out. A step-up challenge is passed to the page.
- **Passwords.** A user whose password an admin set (on creation, a reset, or the
  first admin) is asked for a new one straight after signing in (`auth.md` 4.1). The
  password and code they signed in with wait on the server for up to 10 minutes,
  behind their own cookie, and are never written into the page; "Start again" or a
  new sign-in drops them. Anyone signed in can change their password from the side
  panel, which needs their current password (and code). The session moves to the new
  login's tokens and the old Oh CRUD session is ended at once. Admins set a temporary
  password from a user's details (`POST /admin/v1/users/{id}/resetpw`).
- **Strict headers**: a content security policy allowing only its own scripts and
  styles (and the design's web fonts), no framing, no sniffing.

## 3. Limits

- **Sessions live in memory.** Restarting the platform signs everyone out, and two
  instances behind a load balancer wouldn't share sessions, or sign-ins waiting for a
  new password.
- **Sign-in is the password grant.** Oh CRUD's own login page (with redirects and
  single sign-on) replaces it when it exists.
