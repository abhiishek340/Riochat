# Contributing to Riochat

Thank you for helping. This guide covers how to set up, what to run before you
open a pull request, and what the reviewers look for.

## Layout

| Path | What lives there |
|---|---|
| `backend/` | Go server. Platform in `internal/platform`, product areas in `internal/features`. |
| `web/` | Next.js web client, statically exported. |
| `desktop/` | Tauri desktop shell (Rust). |
| `deploy/` | Compose profiles and configuration templates (no real config or secrets). |
| `scripts/` | Version sync, server build and release helpers, with Node tests. |
| `docs/` | Architecture, configuration, branding, API and Windows deployment notes. |

Read [`backend/ARCHITECTURE.md`](../backend/ARCHITECTURE.md) before touching the
server and [`web/ARCHITECTURE.md`](../web/ARCHITECTURE.md) before touching the
client. They say where code goes and which rules are enforced.

## Set up

Requirements: Go (the version in `backend/go.mod`) with a C compiler, Node.js
20.9 or newer, and pnpm (the version in `web/package.json`). The desktop shell
additionally needs a Rust toolchain and the Tauri platform prerequisites.

```sh
cd web && pnpm install
cd ../backend && go mod download
```

`make setup` does both from the repository root. See the README for a quick start
that runs the server and the web client together.

## Checks to run

Backend, from `backend/`:

```sh
make check        # go build ./... && go vet ./... && go test ./...
```

Web, from `web/`:

```sh
pnpm typecheck
pnpm lint
pnpm test
pnpm build
```

Repository scripts and version files, from the root:

```sh
make version-check   # every manifest matches VERSION; `make version` fixes drift
make test            # workspace tests plus the Node tests in scripts/
```

Change `VERSION` only through that file, then run `make version`.

Desktop, from `desktop/src-tauri/`, once the shell exists:

```sh
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test --lib
```

CI runs the same commands (`.github/workflows/`). A pull request is mergeable
when they pass.

## Tests

Add a regression test with your change when it touches any of these:

- authentication, sessions, permissions or role checks
- billing, quotas, usage metering or payments
- streaming or the generation protocol
- adapters to external providers or tool servers
- request and response shapes that clients depend on (error codes, field names)
- data access that must stay scoped to the signed-in user

Server areas test themselves through HTTP with `internal/testkit`. Pure web
logic (formatters, stream parsing, catalogs) is tested with Vitest. Test names
should describe the behavior, not the function.

## Rules of the codebase

- Server: an area edits only its own directory and tables. Cross-area needs go
  through `internal/integration`. Do not add dependencies, config keys or
  platform helpers from inside an area; open an issue first.
- Server: every error code an area returns is declared with `apperr.Define`
  and namespaced by the area. Never put `err.Error()` in a response. Never log
  secrets, tokens or raw upstream bodies.
- Server: migrations run on every start, so each step must be idempotent, and
  SQL must work on both SQLite and PostgreSQL.
- Web: route files stay thin; features are imported only through their
  `index.ts`; text goes in both locale catalogs; colours come from semantic
  classes, not raw values.
- Where the specification is silent, decide, and record the decision in the
  relevant `GAPS.md` (one line, appended under your area).

## What not to commit

Build output (`web/out`, `.next`, `target/`), `node_modules`, caches, logs,
local databases and storage directories, `.env` files, any real
`config.yaml`, and signing keys or certificates. Run a quick `git status`
before committing.

## Commit messages

Each commit subject must look like `type: subject`, where `type` is one of
`build`, `chore`, `ci`, `docs`, `feat`, `fix`, `perf`, `refactor`, `revert`,
`style` or `test`, and the subject is plain printable ASCII. Merge commits
(subjects starting with `Merge `) are exempt. CI checks every commit in a pull
request. Examples:

```
feat: add export button to shared conversations
fix: keep refresh cookie path stable after logout
```

## Pull requests

- Keep a pull request to one concern; explain the why in the description.
- Fill in the template, including how you verified the change and any
  configuration or migration impact.
- Update the docs (`README.md`, `docs/`, or the architecture files) when
  behavior, configuration or structure changes.

## Security

Do not open public issues for vulnerabilities. Follow
[`SECURITY.md`](SECURITY.md).

## Conduct

By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
