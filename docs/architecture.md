# Architecture

This is the system-level view of Riochat. The two code-level contracts it builds
on are [`backend/ARCHITECTURE.md`](../backend/ARCHITECTURE.md) (how a server
feature area plugs into the platform) and [`web/ARCHITECTURE.md`](../web/ARCHITECTURE.md)
(how the web client is organised). Read those before changing code; read this
page to understand how the pieces fit.

## 1. Moving parts

```
  browser (PWA)            desktop shell (optional)
        \                      /
         \   static files      /   /api/v1: JSON envelope,
          \  + /api/v1        /    NDJSON and SSE streams
           v                 v
        +---------------------------+
        |      riochat (Go)         |   one process
        |  platform + feature areas |
        +-----+---------+-----+-----+
              |         |     |
        PostgreSQL    Redis   files: local disk or S3
        or SQLite     or memory
              |
        outbound HTTPS: model providers, tool servers (MCP),
        identity providers, payment and moderation services
```

- **Server** (`backend/`): a single Go binary. It serves the JSON API, runs
  background workers, and hosts the statically exported web client from the
  directory named by `FRONTEND_DIST_DIR`.
- **Web client** (`web/`): a Next.js app built with `output: "export"`. There is
  no Node process in production; the export is plain files.
- **Desktop shell** (`desktop/`): a Tauri 2 application that starts the same
  server binary (bundled as the sidecar `riochat-server`) as a child process in
  local mode and shows the web client in a native window.

## 2. Server structure

The server is a platform plus feature areas.

- `internal/platform/` is the shared foundation: configuration, database,
  cache, HTTP envelope and middleware, token handling, secret encryption,
  migrations, logging, lifecycle (workers and shutdown) and the static file
  host. Areas import it and never edit it.
- `internal/features/<area>/` holds one directory per product area. The
  registered areas are: platformllm, audit, settings, errorlog, auth, user,
  announcement, channel, permissiongroup, billing, personalprovider,
  knowledgebase, ragindex, memory, mcp, skill, uicomponent, contentmoderation,
  promptreset and conversation. Every area exports `Migrations()` and `Mount()`.
- `internal/registry/` is the single list of areas and the order they start in.
- `internal/integration/` is the only code that knows two areas at once. When
  one area needs another (for example the conversation area needs billing,
  retrieval and moderation), the integration `Hub` hands it a small adapter,
  so areas stay independently testable.
- `internal/app/` builds and runs the process: configuration, database, cache,
  migrations, router, area mounting, graceful shutdown and the command line.
- `internal/testkit/` builds the real application in-process (temporary SQLite
  database, memory cache, fake clock) so each area can test itself through HTTP.

### Startup order

validate configuration, open database, open cache, run the migrations of all
areas (every schema step, then every seed step), build the router, mount every
area in registry order, run start hooks, listen.

Migrations have no version table. Every step runs on every start and is written
to be idempotent, so a start against an already migrated database is a no-op.
With PostgreSQL an advisory lock serialises concurrent starters.

### Shutdown

On SIGINT or SIGTERM the server flips `/readyz` to 503 (`draining`), stops
accepting connections, waits up to `HTTP_SHUTDOWN_TIMEOUT_SECONDS` for in-flight
requests (then closes the rest), stops workers (up to 10 s), waits up to 5 s for
detached tasks, runs stop hooks in reverse order, then closes the cache and the
database. The HTTP server has no write timeout, so long streams are not cut.

## 3. Request pipeline

Outermost first: panic recovery, trusted-proxy client IP, request and trace ids,
access log (API paths only, with query strings and errors redacted), security
headers, CORS allow-list, gzip (skipped for streams), then one of three route
groups under `/api/v1` (rate limits apply only when an administrator turns them
on, see [api.md](api.md)):

| Group | Middleware | Used for |
|---|---|---|
| Public | per-IP rate limit | sign-in, registration, branding, shared conversations |
| Auth | bearer token, session check, per-user rate limit | everything a signed-in user does |
| Admin | bearer token, session check, administrator role | `/admin/*` |

A feature area can also request a group that is hidden behind a capability flag;
while the flag is off those routes answer 404 `feature.disabled`.

Every JSON response uses one envelope (see [api.md](api.md)). Handlers never
write error bodies themselves: they return a typed error and the HTTP layer
turns it into a public code and message, keeping the raw cause only for logs.

## 4. State and data

| Kind | Where | Notes |
|---|---|---|
| Static configuration | environment, YAML file | read once at start; see [configuration.md](configuration.md) |
| Business settings | database table `system_settings` | edited in the admin console, applied without restart |
| Records | PostgreSQL or SQLite | table names are `<area prefix>_<noun>`; no foreign-key constraints, relations are enforced in code |
| Hot state | Redis or in-process memory | rate limits, one-time codes, short-lived caches; every key has a TTL |
| Files | local directory or S3-compatible bucket | uploads, generated media, knowledge-base documents |
| Secrets at rest | AES-GCM key ring | provider keys, identity-provider secrets, tool-server tokens; rotate with `DATA_ENCRYPTION_KEYS_PREVIOUS` |

Each area owns its tables. Reading another area's data goes through that area's
exported Go API, never through its tables.

## 5. Conversations and streaming

Sending a message creates a run. The reply is streamed to the caller as
back-to-back JSON documents (NDJSON). A run can be resumed or cancelled by id,
and a separate server-sent-events feed reports which runs are active so other
tabs can follow them. The generator in `internal/integration` assembles the
prompt (history, attachments, retrieved passages, memories, enabled tools),
calls the chosen model provider, executes tool calls, and records usage for
billing. Content moderation and quota checks run before the model is called.

Knowledge bases and file retrieval: uploaded documents are chunked and embedded
by the `ragindex` area through a durable job table; at send time the retrieval
plan decides, per file, whether to inline the text or retrieve passages.
Vectors are stored in an ordinary table and ranked inside the server process
(see "Known limits" in the README).

## 6. Web client structure

`src/app/` contains routes only. Each product area lives in
`src/features/<area>/` and is imported only through its `index.ts`. Shared code
sits in `src/lib/` (API client, session, capabilities, branding, i18n, theme)
and `src/components/` (UI kit, shell, providers). Because the export is static
there are no dynamic route segments; ids travel in query strings.

The access token lives in memory only. The refresh token is an HttpOnly cookie
scoped to `/api/v1/auth`. On a 401 the client refreshes once (coalesced across
tabs) and retries once. Branding, capabilities and the message catalog are
loaded at start; the page body stays hidden for at most three seconds while they
load, to avoid a flash of default branding.

## 7. Local mode, desktop shell and automation

### Local mode

`riochat --local --data-dir <absolute path>` is the contract between the server
and the desktop shell. It forces SQLite, the in-process cache, local storage
and a loopback-only listener under the data directory, generates and stores its
own secrets there, writes logs to standard error, and prints a single JSON line
on standard output when it is listening:

```json
{"type":"ready","version":"...","origin":"http://127.0.0.1:...","grant":"...","pid":1234}
```

The shell exchanges the one-time `grant` for a session through
`POST /api/v1/auth/local/exchange` with body `{"grant": "..."}` (the grant lives
two minutes and works once). The server exits when its standard input is closed,
so the parent process never leaves an orphan behind. Local mode serves the API
only; the web client is the shell's own bundled copy. See
[configuration.md](configuration.md) for what local mode forces.

### Desktop shell

`desktop/` is a Tauri 2 application (Rust). Its crate and configuration
(`desktop/src-tauri/tauri.conf.json`, capabilities, icons, the `riochat-server`
sidecar entry) are in place and supporting modules of the crate are being
written, but the application entry point (`run()` in `src-tauri/src/lib.rs`) is
still empty, so the shell does not start yet. The web client is expected to talk to it through the
`window.__RIOCHAT_DESKTOP__` bridge. The default CORS list already contains the
Tauri origins (`tauri://localhost` and `http://tauri.localhost`). Enterprise
rollout policy for Windows is in [windows-deployment.md](windows-deployment.md).

Build the sidecar with `node scripts/build-api.mjs --sidecar` (output
`desktop/src-tauri/binaries/riochat-server-<target triple>[.exe]`); `make desktop`,
`make build-desktop` and `make release-desktop` do it for you when it is missing.

### Repository automation

| Workflow | Purpose |
|---|---|
| `.github/workflows/ci.yml` | Version-manifest check and script tests (always); Go gofmt, build, vet and tests; web typecheck, lint, tests and static build; pull-request commit subject check. The backend and web jobs run only for the areas a change touches. |
| `.github/workflows/desktop.yml` | Rust format, lint and tests for the shell, and installer packaging for macOS, Linux and Windows. Skips its jobs while `desktop/src-tauri/tauri.conf.json` is missing. |

The release version is the single line in `VERSION`. `node scripts/sync-version.mjs`
(`make version`) copies it into every manifest and `--check` (`make version-check`)
fails on drift.
