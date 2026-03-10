# Riochat backend architecture

This document is the contract between the platform foundation and the feature
areas built on top of it. Read it fully before writing an area. Behavior comes
from the specs in `riochat-specs/`; this document says *where* code goes and
*how* it plugs in.

## 1. Layout

```
cmd/riochat/                 process entry point (flags only; all logic is in internal/app)
internal/
  app/                       wiring: Build, Run, graceful shutdown, CLI, local mode
  deps/                      Deps: the single value every area receives
  testkit/                   in-process test application for area tests
  features/
    feature.go               Feature type, Validate, MountAll, MigrationSets   (platform-owned)
    example/                 reference area, never registered; copy it
    <area>/                  one directory per feature area                      (area-owned)
  registry/                  THE list of registered areas (registry.Registered())  (integrator-owned)
  integration/               adapters between areas (Hub, Mount* functions)         (integrator-owned)
  platform/                  shared foundation; areas import it, never edit it
    apperr/                  typed errors, error-code allowlist
    buildinfo/               version metadata
    cache/                   Cache interface, memory + redis backends
    capability/              feature flags
    clock/                   Clock, System, Fake
    config/                  loading, validation, Holder, local mode
    database/                Open (sqlite/postgres), error translation, base models, dbtest
    httpx/                   response envelope, JSON binding + validation
    lifecycle/               workers, detached tasks, start/stop hooks, drain signal
    logx/                    structured logger
    middleware/              recovery, ids, access log, CORS, gzip, auth, rate limit, gates
    migrate/                 migration runner and step helpers
    pagination/              page/page_size parsing and GORM scope
    redact/                  secret scrubbing for logs
    reqctx/                  per-request state in context.Context
    secretbox/               AES-GCM key ring for stored secrets
    server/                  gin engine, platform routes, static hosting
    token/                   HS256 access/refresh tokens
```

Technology: Go, gin (HTTP), GORM (PostgreSQL via pgx, SQLite via cgo
`mattn/go-sqlite3`), go-redis, golang-jwt, go-playground/validator, yaml.v3.
Do not add dependencies from an area (see section 9).

## 2. The area contract

An area is a package at `internal/features/<area>` exporting exactly:

```go
func Migrations() []migrate.Step   // its tables, indexes, seeds; may return nil
func Mount(d deps.Deps) error      // routes, hooks, workers
```

`<area>` is lower case letters, digits and underscores, equals the package
directory and is the name used in the registry, in the logger's `area` field
and in migration error messages. The integrator registers it with one line in
`internal/registry/registry.go` (not under `internal/features`: the test kit imports that package, so the list of areas there would make every area's tests import themselves). When an area needs another area's service, the entry's `Mount` is a method of the integration `Hub` (`h.MountBilling`), which passes the adapters:

```go
{Name: "announcement", Migrations: announcement.Migrations, Mount: announcement.Mount},
```

Startup order: validate configuration -> open database -> open cache ->
**migrations of all areas** (all schema steps, then all seed steps) -> build
router -> **`Mount` of every area in registry order** -> start hooks -> serve.
`Mount` runs once, never concurrently, after the database is migrated.

### 2.1 `deps.Deps`

| Field | Use |
|---|---|
| `Routes.Public` / `Routes.Auth` / `Routes.Admin` | `/api/v1` route groups (see 3) |
| `DB` | `*gorm.DB`, migrated, ready |
| `Config` | `*config.Holder`; call `Config.Load()` per use, never cache the snapshot |
| `Logger` | already scoped with `area=<name>` |
| `Cache` | memory or Redis behind one interface |
| `Clock` | use instead of `time.Now()`; tests control it |
| `Secrets` | `*secretbox.KeyRing` for stored secrets |
| `Tokens` | issue/parse access and refresh tokens |
| `Capabilities` | feature flags, read per request |
| `Lifecycle` | `Go` (worker), `Detach` (outlives the request), `OnStart`, `OnStop` |
| `Sessions` | auth area calls `Sessions.Set(validator)` |
| `ErrorRecorder` | error-log area calls `ErrorRecorder.Set(recorder)` |
| `LocalGrant` | auth area calls `LocalGrant.Set(issuer)` for desktop mode |
| `RateLimit` | options for adding limiter middleware yourself |
| `Gated(flag)` | route groups hidden behind a capability flag |
| `ProbeLimit(n)` | per-user limiter for endpoints that call user-supplied addresses |

Everything on `Deps` is safe for concurrent use. Do not use process globals,
`time.Now`, `os.Getenv`, or `log` directly.

## 3. Routes

All business routes live under `/api/v1`. Register paths **relative to it**
(`"/announcements"`, not `"/api/v1/announcements"`).

- `Public`: no authentication, per-IP fixed-window rate limit.
- `Auth`: bearer token + session validation + per-user rate limit.
- `Admin`: bearer token + session validation + administrator role (`admin` or
  `superadmin`).
- `d.Gated(capability.X).Public|Auth|Admin`: same groups, answering 404
  `feature.disabled` while flag X is off. An unknown flag name panics at
  startup. Use it for whole feature areas that the capability flags switch off.

Handlers read the caller with `middleware.MustPrincipal(c)` (user id, username,
role, session id) and the context with `c.Request.Context()`.

**Path ownership.** Each area owns the URL prefixes listed in its spec. Two
areas must never register the same method+path; gin panics on a duplicate,
`MountAll` reports it with both the area name and the route, and startup fails.
Do not register catch-all (`/*x`) routes.

Platform routes outside areas: `GET /healthz`, `GET /readyz`,
`GET /api/v1/version`, `GET /api/v1/capabilities`, and the front-end/404
fallback. Areas must not register these.

## 4. Responses and errors

Every JSON response under `/api/v1` is the envelope
`{"errorMsg","errorCode"?,"details"?,"requestId"?,"data"}`:

```go
httpx.OK(c, data)                    // 200
httpx.Created(c, data)               // 201
httpx.Paged(c, total, results)       // data = {total, results}; nil results -> []
httpx.Fail(c, status, err)           // failure; aborts the chain
httpx.InvalidQuery(c, "status")      // 400 request.invalid_query "invalid status"
httpx.InvalidID(c)                   // 400 request.invalid_id
```

Never write your own JSON error body and never put `err.Error()` in a response.
`Fail` resolves the public code/message from the first *registered typed error*
in the error chain and otherwise from the status's generic contract
(`internal.error` for 500, and so on). The raw cause is kept on the request for
the access log and error log, redacted.

Declare your area's errors where they are used, namespaced by the area:

```go
var ErrNotFound = apperr.Define("announcement.not_found", "announcement not found")
```

`Define` registers the code with the boundary allowlist. A code that is not
registered degrades to the generic code, so an area must `Define` every code it
returns. Codes in the specs are API contracts: reproduce them exactly.
Variants: `err.WithCause(cause)` (public message unchanged, internal text for
logs), `.WithMessage`, `.WithDetails`, `.WithStatus`, `.WithDiagnostic`.
`errors.Is(err, ErrNotFound)` works across variants.

Request bodies: `httpx.BindJSON(c, &req)` decodes and validates using
`binding:"required,min=1,max=128"` struct tags and writes the 400
`request.invalid_body` envelope itself; on `false` just `return`.

Pagination: `p := pagination.FromRequest(c.Request)` (page 1-based, default size
20, max 1000), `db.Scopes(pagination.Scope(p))`.

Streaming handlers: set the status/headers, write frames, and on a failure after
the header record it with `reqctx.FromRequest(c.Request).RecordFailure(code,
cause, detail)` so it reaches the access log and error log; send a terminal
event built with `httpx.BuildFailure`.

## 5. Database and migrations

- Models embed `database.Model` (soft delete: `id, created_at, updated_at,
  deleted_at`) or `database.HardModel`, and declare `TableName()`.
- Table names are `<prefix>_<noun>` with a prefix from `database.TablePrefixes`
  (`announcement, audit, billing, chat, content_moderation, file, identity,
  knowledge_base, llm, mcp, permission_group, prompt, skill, system, ui, user`).
  Use the exact table and column names from your spec. `migrate.AutoMigrate`
  rejects anything else.
- No foreign-key constraints are created by migrations. Enforce relations in
  code.
- Each area owns its tables **and only its tables**. To read another area's
  data, call that area's exported service; do not query its tables.
- Wrap driver errors with `database.Translate(err)`; test with
  `database.IsNotFound` / `database.IsDuplicate`.
- Always pass `ctx`: `db.WithContext(ctx)`.
- SQLite and PostgreSQL must both work: no dialect-specific SQL outside a
  `migrate.Exec(name, phase, map[string]string{"sqlite": ..., "postgres": ...})`
  step.

Migrations (`migrations.go`, owned by the area):

```go
func Migrations() []migrate.Step {
    return []migrate.Step{
        migrate.AutoMigrate("tables", &Thing{}),
        migrate.Exec("idx", migrate.PhaseSchema, map[string]string{ "sqlite": "...", "postgres": "..." }),
        migrate.Seed("defaults", func(ctx context.Context, db *gorm.DB) error { ... }),
    }
}
```

There is no version table. **Every step runs on every start and must be
idempotent** (`IF NOT EXISTS`, `FirstOrCreate`, conditional updates). All schema
steps of all areas run before any seed step, so a seed may rely on another
area's tables existing. Step names are unique within the area. A seed must not
overwrite administrator-edited rows.

## 6. Configuration

`d.Config.Load()` returns an immutable snapshot (`*config.Config`). Static
sections (`Server`, `Security`, `Database`, `Storage`, ...) come from env/YAML.
`Dynamic` is the database-backed layer, overlaid by the settings area; it is
re-read per call so changes apply without a restart.

Config keys, environment variable names and defaults are in the platform spec
and are matched exactly by `internal/platform/config`. If your area needs a new
**static** key, ask the integrator (it touches shared code). Business settings
live in the settings area's `system_settings` table, not in `Config`.

### Settings overlay

The settings area (owner of `system_settings`) is responsible for: seeding
built-in rows, reading all rows at startup (an `OnStart` hook), decrypting
sensitive ones with `d.Secrets`, applying them with `d.Config.Update(func(c
*config.Config) { ... })`, applying the cross-field normalization rules, and
re-applying after an administrator changes a setting. Other areas only read.

## 7. Cache, secrets, time, background work

- Cache keys: prefix every key with your area (`announcement:...`) unless the
  spec dictates a key (such as `ratelimit:*`, `settings:*`). Always set a TTL.
  Cache failures are non-fatal unless atomicity is the point: use `SetNX`,
  `GetDel` (consume once), `Incr`, and the rate limiters for those.
- Secrets: `d.Secrets.Encrypt/Decrypt`, or `EncryptBound(plain, binding)` for
  values tied to an owner. Never log or return plaintext secrets. Lookup
  hashes via `LookupHash/LookupHashes`.
- Time: `d.Clock.Now()` only.
- Workers: `d.Lifecycle.Go(name, func(ctx))` for long-running loops (stop when
  `ctx` is done). `d.Lifecycle.Detach(ctx, name, timeout, fn)` for follow-up
  work that must outlive a request (keeps request ids, panic-safe, tracked at
  shutdown). One-shot startup work: `d.Lifecycle.OnStart(name, fn)` (runs after
  every area is mounted, before traffic; an error aborts startup). Cleanup:
  `d.Lifecycle.OnStop(name, fn)` (runs after workers drain, before the cache
  and database close). Never start a bare `go func()`.
- Logging: `d.Logger.Info("event_name", logx.F("key", value))`. Fields fold into
  the message. Event names are snake_case. No secrets, no tokens, no raw
  upstream bodies.

## 8. Testing an area in isolation

```go
env := testkit.New(t, []features.Feature{{Name: "thing", Migrations: Migrations, Mount: Mount}})
w := env.Do("POST", "/api/v1/things", env.Bearer(testkit.User(7)), map[string]string{"name": "x"})
testkit.Expect(t, w, 201, "")
```

`testkit.New` builds the real application (SQLite in a temp dir, memory cache,
fake clock, real middleware and router) with only your area. Use
`testkit.Admin(id)` for administrators, `env.Clock.Advance(...)` to move time,
`env.DB` for assertions, `testkit.WithConfig(...)` to change configuration
(set `c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"}` to test
desktop-mode capabilities), and `database/dbtest` for pure data-layer tests.
Redis behavior is covered by the platform; areas test against the memory cache.

Every area ships its own tests next to its code, covering the spec's test
scenarios. The gate for any change is:

```
go build ./... && go vet ./... && go test ./...
```

## 9. Ownership rules (how engineers avoid conflicts)

| Path | Owner | Rule |
|---|---|---|
| `internal/features/<area>/**` | the area's engineer | edit freely; nothing else |
| `internal/registry/**`, `internal/integration/**` | the **integrator** | only place where areas are listed and wired to each other |
| `internal/platform/**`, `internal/deps`, `internal/app`, `internal/testkit`, `internal/features/{feature.go}` | platform owner | areas do not edit; request a change |
| `go.mod`, `go.sum` | the integrator | areas do not add dependencies; ask |
| `GAPS.md` | everyone | append-only, one line per decision, under your area |
| `ARCHITECTURE.md` | platform owner | |

Why this prevents collisions: area code lives in its own directory; tables are
owned per area (prefix and spec); error codes are namespaced per area and
declared in the area; URL prefixes are assigned per spec; cache keys are
prefixed per area; migration steps are named per area; and the only shared
write is one registry line. An area needs nothing else to compile and pass its
tests.

If you need something from another area, depend on its exported Go API at
integration time; for parallel work, define a small interface in your own
package and let the integrator wire it in `Mount` via a registry-level adapter.
If you need a new platform capability (a helper, a config key, a dependency),
stop and ask: do not fork it into your area.

## 10. Request pipeline (outermost first)

1. `Recovery` - panic -> `http_panic_recovered` log, error-log record, 500 envelope
2. `TrustedProxy` - per-request state, client IP (honors `X-Forwarded-For`/`X-Real-IP` only from trusted proxies)
3. `RequestID` - server-controlled request id and trace id; response headers
4. `AccessLog` - `/api/` paths only; query and error text redacted; persists 5xx
5. `SecurityHeaders`
6. `CORS` - allowlist from config; 403 `cors.origin_forbidden`; OPTIONS -> 204
7. `Gzip`
8. group middleware: `PublicRateLimit` | `Authenticate` + `UserRateLimit` | `Authenticate` + `AdminOnly`

## 11. Startup and shutdown

Start: `app.Build` (validate config, key ring, database, cache, migrations,
router, mount areas) then `App.Run`: `OnStart` hooks -> listen -> serve.
Local mode (`--local --data-dir <abs>`) forces SQLite + memory cache + local
storage + loopback listener, logs to stderr, prints exactly one JSON line
`{"type":"ready","version","origin","grant","pid"}` on stdout once listening,
and shuts down when stdin reaches EOF.

Shutdown (SIGINT/SIGTERM/parent exit): begin drain (`/readyz` -> 503
`draining`) -> stop accepting and wait up to `HTTP_SHUTDOWN_TIMEOUT_SECONDS` for
in-flight requests -> on timeout force-close (`server_drain_timeout_force_close`
warning, still exit 0) -> cancel workers and wait -> wait <= 5 s for detached
tasks -> run `OnStop` hooks in reverse order -> close cache -> close database.

## 12. What the foundation does not include

Intentionally left to areas or later platform work: object storage (spec 7),
outbound HTTP safety / SSRF policy (8), model-provider clients (11), embedding,
MCP, moderation, GeoIP and payment clients (13), OpenTelemetry tracing (14.2),
the secret re-encryption job (15.7), vector storage (5.7), the settings overlay
and bootstrap administrator (see GAPS.md). Where a future platform package is
needed, it is added under `internal/platform/` by the platform owner and exposed
on `deps.Deps` in a backward-compatible way.
