# Riochat

Riochat is a self-hosted, multi-model AI chat system. One Go server provides the
HTTP API, background workers and the hosting of a statically exported web client.
An optional desktop shell can bundle the same server as a local, single-user
sidecar.

Highlights:

- Conversations with streaming replies, branching (fork), edit, regenerate,
  starring, labels, projects, search, export and public share links.
- Several upstream model providers behind one routing layer, plus per-user
  personal providers and tool servers (MCP).
- File uploads, knowledge bases and retrieval-augmented answers.
- Accounts with password, email code, two-factor and external identity
  providers; roles and permission groups.
- An administration console: users, groups, models and upstreams, billing and
  redemption codes, announcements, content moderation, logs and statistics.
- Branding you can change without touching code (title, icons, logo, accent).
- English and Simplified Chinese interface.

## Repository layout

| Path | What it is |
|---|---|
| `backend/` | The Go server (module `github.com/riochat/riochat/backend`). Entry point `backend/cmd/riochat`. |
| `web/` | The Next.js web client, statically exported to `web/out`. |
| `desktop/` | The Tauri desktop shell (in progress). |
| `deploy/` | Compose profiles, configuration templates, Windows policy templates, desktop signing notes. See [`deploy/README.md`](deploy/README.md). |
| `Dockerfile` | Builds one image holding the server and the exported web client. |
| `scripts/` | Version sync, server build, clean and desktop release helper scripts. |
| `docs/` | Architecture, configuration, branding, API and Windows deployment notes. |
| `.github/` | Contribution guide, policies, issue and pull request templates, CI workflows. |
| `GAPS.md` (in the root, `backend/` and `web/`) | Design decisions taken where the specifications were silent. |

Status: the server, the web client, the container image, the Compose profiles,
the build tooling and the Windows policy templates are in this tree. The desktop
shell (`desktop/`) is still being assembled: its configuration, icons and
supporting modules exist, but the application entry point is still empty, so
there is no runnable desktop app yet (see `docs/architecture.md`, section 7).

## Quick start (container)

```sh
docker build -t riochat:local .
cp deploy/config.sqlite.example.yaml deploy/config.yaml
RIOCHAT_CHAT_IMAGE=riochat:local docker compose -f deploy/docker-compose.sqlite.yml up -d
docker logs riochat-app 2>&1 | grep bootstrap_admin_created    # first administrator password
```

Without `RIOCHAT_CHAT_IMAGE` the profiles pull `ghcr.io/riochat/riochat:latest`.
Open <http://127.0.0.1:8080> (the port is published on loopback only).

Three Compose profiles (app only with external services, SQLite, and the full
app + PostgreSQL + Redis stack) are described in [`deploy/README.md`](deploy/README.md),
including what to replace before a production deployment.

## Developer commands

`make` lists every target. The ones used most: `make setup`, `make dev` (API and
web dev server; the API reads `deploy/config.yaml`, which you create with
`cp deploy/config.example.yaml deploy/config.yaml`; set `database.driver: sqlite`
and `cache.driver: memory` in it to run without PostgreSQL and Redis),
`make verify` (check, test, build), `make build-api` (server binary in
`.cache/riochat/`), `make version` (propagate `VERSION` to every manifest) and
`make version-check` (fail on drift).
The repository is a pnpm + turbo workspace (`backend`, `web`, `desktop`).

## Quick start (local, SQLite, no external services)

Requirements: Go 1.26 or newer with a C compiler (SQLite is built through cgo),
Node.js 20.9 or newer and pnpm.

1. Build the web client:

   ```sh
   cd web
   pnpm install
   pnpm build            # writes web/out
   ```

2. Start the server with the web client mounted:

   ```sh
   cd backend
   APP_ENV=dev \
   DATABASE_DRIVER=sqlite CACHE_DRIVER=memory \
   SQLITE_PATH=./data/riochat.db STORAGE_ROOT_DIR=./storage \
   FRONTEND_DIST_DIR=../web/out \
   go run ./cmd/riochat
   ```

3. Open <http://localhost:8080>. On the first start with an empty database the
   server creates an administrator named `admin` and logs the generated password
   once, in a warning event called `bootstrap_admin_created`. Copy it from the
   log; it is not shown again. The account is asked to set its own password at
   first sign-in.

4. Sign in, open the admin console (`/admin`), add a model upstream and a model,
   and start chatting.

`APP_ENV=dev` allows the built-in placeholder secrets and prints a warning for
each; it also logs at debug level, which includes every SQL statement. Never run a public deployment that way (see Configuration).

### Single-user local mode

```sh
cd backend
go run ./cmd/riochat --local --data-dir ./data     # or: make run-local
```

Local mode forces SQLite, an in-process cache, local file storage (all under the
data directory, together with generated secrets in `secrets.json`) and a
loopback-only listener on a random port, writes logs to standard error, prints
exactly one JSON line `{"type":"ready","version","origin","grant","pid"}` on
standard output once it is listening, and exits when standard input is closed.
It serves the API only, with a single passwordless `owner` account that the
caller signs into with the one-time `grant`. It is the contract the desktop
shell uses to supervise the server.

### Front-end development

Run the server as above, then in a second terminal:

```sh
cd web
pnpm dev                      # http://localhost:3000, proxies /api to the server
```

The dev server forwards the browser's `Origin: http://localhost:3000`, so start
the backend with `CORS_ALLOW_ORIGIN=http://localhost:3000` (this replaces the
default list, which only covers port 8080; `make dev` uses a template that
already lists it). Use
`RIOCHAT_BACKEND_URL` to point the proxy at another backend address.

## Configuration

Precedence: environment variables, then the YAML file, then built-in defaults.
The file is found through `CONFIG_FILE`, otherwise `config.yaml` in the working
directory or its parent. A short template is
[`backend/config.example.yaml`](backend/config.example.yaml), full ones are in
[`deploy/`](deploy/); the complete list of
keys and variables is in [`docs/configuration.md`](docs/configuration.md).

Business settings (sign-up rules, model catalogue, billing, moderation,
announcements and so on) are not in the file. They live in the database and are
edited in the admin console; changes apply without a restart. Interface identity (title,
icons, logo, accent colours) is covered in [`docs/branding.md`](docs/branding.md).

The values that matter most for a real deployment:

| Setting | Variable | Notes |
|---|---|---|
| Environment | `APP_ENV` | `prod` rejects placeholder secrets. |
| Session signing secret | `JWT_SECRET` | Random; at least 16 characters, 32 or more recommended. |
| Stored-secret key | `DATA_ENCRYPTION_KEY` | Random, 32 or more characters. Protects provider keys and other stored secrets. To rotate, move the old key to `DATA_ENCRYPTION_KEYS_PREVIOUS` and keep it there (nothing re-encrypts old values yet). |
| Database | `DATABASE_DRIVER`, `POSTGRES_DSN` / `SQLITE_PATH` | `postgres` or `sqlite`. |
| Cache | `CACHE_DRIVER`, `REDIS_ADDR` | `redis` for more than one process; `memory` only for a single process. |
| Files | `STORAGE_BACKEND`, `STORAGE_ROOT_DIR`, `STORAGE_S3_*` | `local` or `s3`. |
| Web client | `FRONTEND_DIST_DIR` | Directory holding the exported client (`web/out`; `/app/frontend/out` in the image). The built-in default points nowhere in this layout. |
| Origins | `CORS_ALLOW_ORIGIN`, `PUBLIC_API_BASE_URL`, `PUBLIC_WEB_BASE_URL` | Restrict CORS to your own origins. In `prod` both public URLs must be https URLs. |
| Proxy trust | `TRUSTED_PROXIES` | Only these peers may set `X-Forwarded-For`; empty by default, so list your reverse proxy. |
| Outbound safety | `SSRF_PROTECTION_ENABLED` | Turn on in production; it takes effect only with `APP_ENV=prod`. |
| Location lookup | `GEOIP_PROVIDER` | Defaults to `ipwhois`, which sends client addresses to an external service; set `none` to turn it off. |

The HTTP API conventions (envelope, authentication, streaming) are summarised in
[`docs/api.md`](docs/api.md).

Production checklist: `APP_ENV=prod`, new random secrets, a PostgreSQL database
and Redis, HTTPS terminated by a reverse proxy, narrow CORS origins, SSRF
protection on, and a backup plan for both the database and the storage
directory.

## Architecture in brief

```
 browser / desktop shell
        |  static files + /api/v1 (JSON envelope, NDJSON and SSE streams)
        v
 +------------------ riochat server (Go) -------------------+
 |  platform: config, database, cache, auth tokens, HTTP     |
 |  areas: auth, user, conversation, knowledge base, ...     |
 |  integration: the only place areas are connected          |
 +----------------------------+------------------------------+
              PostgreSQL or SQLite     Redis or memory     local disk or S3
```

The server is organised as a platform foundation plus independent feature areas
that are registered in a single list. The web client is organised the same way,
one folder per product area. Details are in
[`docs/architecture.md`](docs/architecture.md), [`backend/ARCHITECTURE.md`](backend/ARCHITECTURE.md)
and [`web/ARCHITECTURE.md`](web/ARCHITECTURE.md).

## Development

Backend (from `backend/`):

```sh
make check            # go build ./... && go vet ./... && go test ./...
make lint             # go vet plus a gofmt cleanliness gate
```

Web (from `web/`):

```sh
pnpm typecheck && pnpm lint && pnpm test && pnpm build
```

Repository scripts (from the root): `make version-check` and `make test`, which
also runs the Node tests in `scripts/`. The same checks run in CI
(`.github/workflows/ci.yml`). Read
[`.github/CONTRIBUTING.md`](.github/CONTRIBUTING.md) before opening a pull request.

## Known limits

- Retrieval vectors are stored in an ordinary table and ranked inside the server
  process. This is fine for modest knowledge bases and slow for large ones; a
  vector index is future work.
- The PostgreSQL code paths are compiled and share the SQLite-tested logic, but
  were not exercised against a live PostgreSQL server in the build
  environment. Run the test suite against your own PostgreSQL before
  relying on it.
- Trace export uses OTLP over HTTP only; the `grpc` protocol setting is accepted
  but still sent over HTTP.
- Changing the data-encryption key keeps old values readable through
  `DATA_ENCRYPTION_KEYS_PREVIOUS`, but nothing re-encrypts them in the
  background yet.

`backend/GAPS.md` and `web/GAPS.md` record every decision of this kind.

## Security

Report vulnerabilities privately; see [`.github/SECURITY.md`](.github/SECURITY.md).

## License

No license file has been added yet (see `NOTICE`). The repository owner decides the terms; until
then, all rights are reserved by default.
