# Testing Guide

A practical, codebase-specific guide to writing tests for this project. It assumes
you know Go but are new to *testing* Go. It progresses from the easy wins you already
have to the harder cases (the queue, webhooks, websockets).

The goal is not 100% coverage. The goal is: **when you change something, a test tells
you whether you broke it.** Everything below serves that.

---

## 1. The mental model: tests by shape, not by feature

Don't think "how do I test webhooks." Think "what *shape* is this code?" Each shape
has a standard recipe. Almost everything in this repo is one of four shapes:

| Shape | Example here | Recipe | Difficulty |
|---|---|---|---|
| **Pure function** — input → output, no I/O | `QueryBuilder`, `CoerceType`, `ValidateDataModel`, `DiffStruct` | Call it, assert the return value | Easy ✅ |
| **HTTP handler** — request → response | `addNewResource`, `Cors` | `httptest` request + recorder | Easy ✅ |
| **Code that talks to a dependency** (DB, HTTP, clock) | `SingleInsert`, queue workers | Replace the dependency with a fake (a "seam") | Medium ⚠️ |
| **Concurrent / network code** | `QueueManager` workers, `EventManager` websockets | Synchronize on channels, use real loopback servers | Hard 🔶 |

The single most important skill is recognizing the shape and not over-engineering.
A pure function needs no mocks, no setup, no framework. Don't reach for the hard
tools until the shape demands it.

---

## 2. Go testing fundamentals (the parts that matter here)

You already use these in `tools/model_funcs_test.go` and `middleware/cors_test.go`.
Here's the vocabulary so the rest of the guide makes sense.

- **File naming**: `foo.go` is tested by `foo_test.go` in the *same package*. Same
  package = you can test unexported functions. (Use `package tools_test` only when you
  deliberately want to test the public API as an outsider — rare here.)
- **Test function**: `func TestXxx(t *testing.T)`. Must start with `Test`, take `*testing.T`.
- **Failing**: `t.Errorf(...)` records a failure but keeps going; `t.Fatalf(...)` stops
  the test immediately (use it when continuing makes no sense, e.g. a setup step failed).
- **Subtests**: `t.Run("name", func(t *testing.T){ ... })` — groups related cases, gives
  each its own pass/fail line. You use this already.
- **Helpers**: a function that does setup and calls `t.Helper()` so failures point at the
  *caller's* line, not inside the helper. Your `basicModel()` / `makeServerConf()` are
  helpers (add `t.Helper()` if they take `t`).

### The table-driven pattern (your default)

This is the idiomatic Go pattern and you already use it well. Each row is a case:

```go
func TestCoerceType_Int(t *testing.T) {
    cases := []struct {
        name    string
        in      any
        want    int64
        wantErr bool
    }{
        {"from int", 5, 5, false},
        {"from string", "5", 5, false},
        {"from float", 5.0, 5, false},
        {"garbage", "abc", 0, true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := CoerceType(tc.in, "int")
            if tc.wantErr {
                if err == nil {
                    t.Fatalf("expected error, got nil")
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if got != tc.want {
                t.Errorf("got %v, want %v", got, tc.want)
            }
        })
    }
}
```

Adding a new case is one line. That low cost is the whole point — it's why you end up
with thorough coverage of edge cases instead of just the happy path.

### Running tests

```bash
go test ./...                      # everything
go test ./tools -v                 # one package, verbose (see each subtest)
go test -run TestCoerceType ./tools   # one test (regex match on name)
go test -run TestCoerceType/from_string ./tools   # one subtest
go test -race ./...                # detect data races (CRITICAL for the queue/eventmanager)
go test -cover ./...               # coverage % per package
go test -coverprofile=cov.out ./tools && go tool cover -html=cov.out  # visual coverage
```

`-race` is not optional for this codebase. The queue manager and event manager spawn
goroutines; the race detector catches concurrent map access and unsynchronized shared
state that otherwise show up as random production crashes. **Run `go test -race ./...`
before every commit.**

---

## 3. The key idea: seams (how to test code with dependencies)

A "seam" is a place where you can swap a real dependency for a fake one *without
changing the code under test*. This is the concept that unlocks everything past pure
functions. The good news: **your codebase already has seams**, you just haven't
leaned on them yet.

You already did this in `cors_test.go`:

```go
alwaysRejectAuth := func(_ http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
    })
}
```

That `alwaysRejectAuth` is a **test double** — a fake auth middleware that always
rejects, so you can test the broken-preflight scenario deterministically. That is
exactly the technique used everywhere below; you already understand it.

### The two seams you have for the database

In `models/database.go` you defined interfaces:

```go
type DBExecutor interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
type DBExecQuery interface { DBExecutor; DBQueryer; Query(...) }
```

Functions like `SingleInsert(db models.DBExecutor, ...)` take the *interface*, not the
concrete `*pgxpool.Pool`. **This is the seam.** In production you pass the real pool;
in a test you pass a fake that implements `Exec`:

```go
// fakeExecutor records what SingleInsert tried to run, without a real DB.
type fakeExecutor struct {
    gotSQL  string
    gotArgs []any
    err     error
}

func (f *fakeExecutor) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
    f.gotSQL = sql
    f.gotArgs = args
    return pgconn.CommandTag{}, f.err
}

func TestSingleInsert_BuildsCorrectSQL(t *testing.T) {
    db := &fakeExecutor{}
    err := SingleInsert(context.Background(), db, /* model, data ... */)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !strings.Contains(db.gotSQL, "INSERT INTO") {
        t.Errorf("expected INSERT, got: %s", db.gotSQL)
    }
    // assert the args were coerced/ordered correctly, etc.
}
```

No Postgres needed. You're testing *your* logic (does it build the right SQL with the
right args), not Postgres's logic. This is the right boundary for a unit test.

### The refactor that unlocks the queue manager

`QueueManager.Db` is currently a concrete `*pgxpool.Pool`, so workers call
`qm.Db.Exec(...)` directly — no seam, can't fake it. The fix is a one-line type change:

```go
// before
type QueueManager struct { Db *pgxpool.Pool; ... }
// after
type QueueManager struct { Db models.DBExecQuery; ... }
```

`*pgxpool.Pool` already satisfies `DBExecQuery`, so production code is unaffected, but
now tests can inject a fake. **This kind of "widen the field to an interface" refactor
is the most valuable testing investment you can make in this repo** — do it for the DB
on `QueueManager` and `EventManager`. Whenever you find code you "can't test," look for
the concrete dependency and ask whether it can become an interface parameter.

---

## 4. Recipes by layer

### 4.1 Pure functions — start here (you're mostly done)

`queryBuilder`, `CoerceType`, `ValidateDataModel`, `DiffStruct`, the version-compare
logic. These are already your best-tested code. The work remaining is **edge cases**,
not new machinery. For each pure function, deliberately add rows for:

- empty / nil / zero inputs
- the boundary (max int, empty string, single-element slice)
- the malformed input that *should* error (assert it errors — error paths are where
  real bugs hide)

Practice target: open `tools/queryManager.go`, pick `BuildUpdate`, and write a
table-driven test covering: one field, many fields, with WHERE, without WHERE, with
limit/offset. You'll likely find an edge case the current code mishandles. That
discovery *is* the value of the test.

### 4.2 HTTP handlers — `httptest`

The pattern (you have it in `cors_test.go`): build a request, build a recorder, call
the handler, assert on the recorder.

```go
func TestAddNewResource_RejectsInvalidJSON(t *testing.T) {
    qm := newTestQueueManager(t)      // helper: queue with a fake DB
    cfg := basicModel()               // reuse your existing helper
    svr := makeServerConf(nil, nil)

    handler := addNewResource(qm, &cfg, svr)

    body := strings.NewReader(`{ this is not valid json `)
    req := httptest.NewRequest(http.MethodPost, "/test", body)
    // inject the user the handler expects from middleware:
    req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, &models.User{Role: "admin"}))
    rr := httptest.NewRecorder()

    handler.ServeHTTP(rr, req)

    if rr.Code != http.StatusBadRequest {
        t.Errorf("want 400 for invalid JSON, got %d (body: %s)", rr.Code, rr.Body.String())
    }
}
```

Things to test per handler — each is a subtest or a table row:
- **Happy path**: valid request → 200/202 and a `task_id` in the body.
- **Bad input**: malformed JSON, missing required field, wrong type → 400.
- **Authz**: a user whose role isn't allowed → 403.
- **Method/endpoint disabled** in `end-points-allowed` → 405/404.

Note the user-context injection. Handlers read the user via `middleware.GetUser(r)`,
which middleware sets earlier. In a unit test there's no middleware, so you set the
context value yourself. Find the exact context key in `middleware/` and wrap a small
helper `withUser(req, role)` so every handler test stays one line.

The key boundary decision: in handler tests, **the queue is faked** (you assert "a job
was enqueued with these args"), you do *not* drive a real DB. You're testing the
HTTP layer — parse, validate, authorize, enqueue, respond — in isolation.

### 4.3 Database code — unit fake vs. real Postgres

Two complementary levels, both worth having:

**Unit level (fast, no DB):** use the `DBExecutor`/`DBExecQuery` fake from §3. Tests
that the right SQL and args are produced. Run on every save. This covers the *logic*.

**Integration level (slower, real DB):** spins up a real Postgres so you test that the
SQL *actually runs* and the schema matches. The fake can't catch a typo'd column name;
a real DB can. Recommended tool: **testcontainers-go** (programmatically starts a
throwaway Postgres in Docker):

```go
//go:build integration

func TestSingleInsert_Integration(t *testing.T) {
    ctx := context.Background()
    pgC, _ := postgres.Run(ctx, "postgres:16-alpine", /* opts */)
    defer pgC.Terminate(ctx)
    pool, _ := pgxpool.New(ctx, pgC.MustConnectionString(ctx))
    // run your real migrations/Atlas sync here, then:
    err := SingleInsert(ctx, pool, /* ... */)
    // query the row back, assert it landed correctly
}
```

Gate these behind a build tag (`//go:build integration`) so `go test ./...` stays fast
and CI runs them separately: `go test -tags=integration ./...`. The lighter-weight
alternative if Docker is a hassle: a `DATABASE_URL` pointing at a local test database,
skipped via `t.Skip()` when the env var is unset.

> Don't try to mock the entire pgx interface by hand — it's huge. Either fake the small
> `DBExecutor` interface *you* defined (unit), or use a real Postgres (integration).
> The middle ground (hand-mocking pgx) is all pain, no value.

### 4.4 The queue manager — concurrency

`QueueManager` spawns worker goroutines. Two rules make this testable:

1. **Never assert with `time.Sleep`.** Sleeps are flaky (too short → fails under load;
   too long → slow suite). Synchronize on a signal instead. Easiest: have your fake DB
   close a channel when `Exec` is called, and the test waits on that channel:

```go
func TestQueue_RunsEnqueuedJob(t *testing.T) {
    done := make(chan struct{})
    db := &fakeExecutor{onExec: func() { close(done) }}
    qm := tools.NewQueue(db, 1 /*workers*/, testLogger(), fakeEventManager())

    qm.QueueExec(context.Background(), "UPDATE x SET y=1", "note", nil)

    select {
    case <-done: // worker picked up and ran the job
    case <-time.After(2 * time.Second):
        t.Fatal("job was never executed")
    }
}
```

   The `time.After` is a *timeout safety net* (fail if it never happens), not a guess at
   timing — that's the correct use of time in concurrent tests.

2. **Always run these with `-race`.** First do the §3 refactor so `Db` is an interface.

Separately, extract the pure decision logic so you can test it without goroutines at
all. `reportWork(w, status, res, fail)` decides success/warn/fail counts — that's pure
logic. Test it directly with no worker, no goroutine, no DB. Whenever a concurrent
component has a pure "decide what to do" core, pull it out and unit-test the core; only
use the goroutine-level test for the "does the plumbing actually run it" question.

### 4.5 Webhooks — the headline feature

A webhook is an **outbound HTTP POST** your server makes when an event fires. The test
question is: *"when an insert succeeds, does the server POST the right payload to the
configured URL?"*

The trick: don't mock HTTP. Stand up a **real tiny server on localhost** with
`httptest.NewServer` and point the webhook config at *its* URL. It records what it
receives. This is the canonical Go way to test any outbound HTTP call.

```go
func TestWebhook_FiresOnInsertSuccess(t *testing.T) {
    // 1. A fake webhook receiver. It captures the request the server sends.
    received := make(chan []byte, 1)
    receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        received <- body
        w.WriteHeader(http.StatusOK)
    }))
    defer receiver.Close()

    // 2. A model whose insert-success webhook points at the fake receiver.
    cfg := basicModel()
    cfg.Webhooks = &models.EventAction{
        On_insert: &models.EventStatus{Success: []string{receiver.URL}},
    }

    // 3. Wire up the EventManager and register the topic from that config.
    em := tools.NewEventManager(fakeDB(), testLogger())
    em.EnableTopic("table:test_table", &cfg)

    // 4. Fire the event the queue would normally fire on success.
    em.Publish("table:test_table", "insert", "success", []byte(`{"id":1}`))

    // 5. Assert the receiver got the POST, with a timeout safety net.
    select {
    case body := <-received:
        if !strings.Contains(string(body), `"id":1`) {
            t.Errorf("payload missing data: %s", body)
        }
    case <-time.After(2 * time.Second):
        t.Fatal("webhook never fired")
    }
}
```

This one test exercises the whole chain: config parsing → topic registration → event
publish → callback dispatch → HTTP POST → payload shape. Adapt the constructor/method
names to the real signatures in `tools/eventsManager.go`.

Cases worth adding once the harness above works:
- Webhook fires for the **right** action/status only (insert-success URL must *not*
  receive an update-fail event).
- **`on_any` / status `all`** wildcards dispatch correctly.
- A receiver that returns 500 or hangs **doesn't crash or block** the server (the
  callback runs in a goroutine — verify a slow/erroring receiver doesn't wedge the queue;
  this is exactly the kind of bug `-race` plus a deliberately-slow receiver surfaces).
- No webhook configured → nothing is sent, no panic.

The "right action/status only" and "bad receiver doesn't block" cases are where the
real bugs live. The happy path mostly works by construction; the routing and
failure-isolation are what break.

### 4.6 Websockets — same idea, ws client

`EventManager` upgrades HTTP connections to websockets and broadcasts events to
subscribed clients. Test it with a real loopback connection: `httptest.NewServer`
wrapping the upgrade handler, then dial it with the gorilla client you already depend on.

```go
func TestWebsocket_ReceivesSubscribedEvent(t *testing.T) {
    em := tools.NewEventManager(fakeDB(), testLogger())

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        em.RegiterTopicOnly(w, r, "table:test_table") // your upgrade entrypoint
    }))
    defer srv.Close()

    // dial it: http:// -> ws://
    wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
    conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
    if err != nil {
        t.Fatalf("dial failed: %v", err)
    }
    defer conn.Close()

    // subscribe (your documented instruction format from docs/webhooks.md)
    conn.WriteJSON(map[string]string{
        "instruction": "sub", "topic": "table:test_table",
        "action": "insert", "status": "success",
    })

    // give the server a moment to register, then publish
    em.Publish("table:test_table", "insert", "success", []byte(`{"id":42}`))

    conn.SetReadDeadline(deadlineFromCtx(t)) // fail instead of hanging forever
    _, msg, err := conn.ReadMessage()
    if err != nil {
        t.Fatalf("read failed: %v", err)
    }
    if !strings.Contains(string(msg), "42") {
        t.Errorf("unexpected message: %s", msg)
    }
}
```

Always set a read deadline (`conn.SetReadDeadline`) so a bug makes the test *fail* with
a timeout instead of hanging the whole suite. Cases: subscribe→receive, unsubscribe→
silence, subscribed-to-X doesn't receive Y, client disconnect cleans up its
registration (assert the client map shrinks — this is where goroutine/connection leaks
hide). Run with `-race`.

---

## 5. Tooling: what to add, what to skip

You currently use the standard library only, and for pure functions that's genuinely
fine. Recommendations, in priority order:

1. **`httptest`** (stdlib) — already available, no install. The workhorse for handlers,
   webhooks, and websockets. Master this first.
2. **`go test -race`** (stdlib) — already available. Make it a habit, not a tool.
3. **testify** (`github.com/stretchr/testify`) — *optional* quality-of-life. `require`
   and `assert` cut boilerplate:
   ```go
   require.NoError(t, err)          // vs. if err != nil { t.Fatalf(...) }
   assert.Equal(t, want, got)       // vs. if got != want { t.Errorf(...) }
   ```
   `require` stops the test (like `Fatalf`), `assert` continues (like `Errorf`). It's
   the one external testing dep most Go teams adopt. Adopt it if the boilerplate annoys
   you; your current plain style is perfectly valid without it. Don't bother with
   testify/mock — your hand-written fakes are clearer for this codebase.
4. **testcontainers-go** (`github.com/testcontainers/testcontainers-go`) — when you want
   real-DB integration tests (§4.3). Adds Docker as a test-time requirement, so gate it
   behind a build tag.

Skip: gomock/mockgen (your interfaces are small enough to fake by hand), and any
BDD/ginkgo-style framework (adds ceremony, not value, for a project this size).

---

## 6. A learning path (do these in order)

You learn testing by writing tests, not by reading about it. Concrete progression,
each step building on the last:

1. **Extend a pure-function test you already have.** Open `tools/model_funcs_test.go`,
   pick `CoerceType`, add three edge-case rows that currently aren't covered (nil input,
   overflow, a type that should error). Watch one fail, fix the code or the
   expectation. *Goal: fluency with the table-driven loop.*
2. **Write a brand-new pure-function test.** Pick `BuildUpdate` in `queryManager.go`,
   which has thinner coverage. *Goal: writing a test file from scratch.*
3. **Write a handler test** for `addNewResource` (§4.2), faking the queue. *Goal:
   `httptest` + context injection + the "fake the dependency" instinct.*
4. **Do the interface refactor** (§3): change `QueueManager.Db` to `models.DBExecQuery`,
   confirm `go build ./...` still passes. *Goal: creating a seam.*
5. **Write the queue execution test** (§4.4) with a channel signal and `-race`. *Goal:
   concurrency without sleeps.*
6. **Write the webhook test** (§4.5) with `httptest.NewServer`. *Goal: testing outbound
   I/O — the technique that generalizes to any external call.*
7. **Write the websocket subscribe/receive test** (§4.6). *Goal: full-loop network test.*

By step 7 you'll have hit every shape in the codebase and can test anything new by
analogy.

---

## 7. Principles to keep

- **Test behavior, not implementation.** Assert "returns 400 on bad JSON," not "calls
  `json.Decode` once." The first survives refactors; the second breaks on every cleanup.
- **One reason to fail per test.** A test named `TestWebhook_FiresOnInsertSuccess` should
  fail for exactly that. Split multi-assertion tests into subtests.
- **Name tests as sentences.** `TestAddNewResource_RejectsInvalidJSON` tells you what
  broke from the failure line alone, before you read any code.
- **Test the error paths.** Happy paths usually work. Bugs live in "what if it's nil,"
  "what if the DB errors," "what if the receiver is down." Deliberately aim there.
- **Fast by default, slow on demand.** Unit tests (no Docker, no network) run on every
  save. Integration tests run behind a build tag. Keep the inner loop under a second.
- **`-race` always** for anything touching the queue or event manager.
- **When you can't test something, that's a design signal.** The fix is almost always
  "introduce an interface at the dependency," not "write a more elaborate mock."

---

## Quick reference

```bash
go test ./...                              # all
go test ./tools -v                         # one package, verbose
go test -run TestName/subcase ./pkg        # one (sub)test
go test -race ./...                        # race detector — use for queue/events
go test -cover ./...                       # coverage summary
go test -tags=integration ./...            # include DB integration tests
go test -coverprofile=c.out ./tools && go tool cover -html=c.out  # visual coverage
```

| I want to test... | Recipe | Section |
|---|---|---|
| A function with no I/O | Call it, table-driven assertions | §4.1 |
| An HTTP handler | `httptest.NewRequest` + `NewRecorder` | §4.2 |
| Code that runs SQL | Fake the `DBExecutor` interface (unit) / testcontainers (integration) | §4.3 |
| The job queue | Interface-fake the DB, signal via channel, `-race` | §4.4 |
| A webhook fires | `httptest.NewServer` as a fake receiver | §4.5 |
| A websocket broadcast | `httptest.NewServer` + gorilla dialer | §4.6 |
