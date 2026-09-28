# Deployment

Everything needed to run Riochat from the container image lives here. This whole
directory is excluded from Docker build contexts, and `config.yaml`, `.env` and
`secrets/*` (except the documented templates) are ignored by git.

| File | Purpose |
|---|---|
| `docker-compose.yml` | Default profile: the app only, with an external PostgreSQL and Redis. |
| `docker-compose.sqlite.yml` | Lightweight profile: the app with SQLite and an in-memory cache. |
| `docker-compose.full.yml` | Full profile: the app, PostgreSQL (pgvector) and Redis. |
| `config.example.yaml` | Template for the default profile and for local development (`make api`). |
| `config.sqlite.example.yaml` | Template for the lightweight profile. |
| `config.full.example.yaml` | Template for the full profile. |
| `.env.example` | Optional Compose variables (image, datastore passwords). |
| `services/` | Optional extraction services (RapidOCR, Tesseract, Docling, Tika), one compose file each. |
| `windows/` | Group Policy templates and a sample `.reg` file for the desktop app. |
| `secrets/` | Local-only desktop signing material (see its README). |

## Run it

1. Pick a profile and copy its template next to the Compose files:

   ```sh
   cp deploy/config.sqlite.example.yaml deploy/config.yaml     # or config.full.example.yaml / config.example.yaml
   ```

   The file must be readable by the container user (uid 10001), for example
   mode `0644`.

2. Start it:

   ```sh
   docker compose -f deploy/docker-compose.sqlite.yml up -d     # or ...full.yml / docker-compose.yml
   ```

3. Open <http://127.0.0.1:8080>. On the first start with an empty database the
   server creates an administrator named `admin` and logs the generated password
   once, in an event named `bootstrap_admin_created`:

   ```sh
   docker logs riochat-app 2>&1 | grep bootstrap_admin_created
   ```

The image comes from `RIOCHAT_CHAT_IMAGE` (default `ghcr.io/riochat/riochat:latest`).
To run your own build:

```sh
docker build -t riochat:local .
RIOCHAT_CHAT_IMAGE=riochat:local docker compose -f deploy/docker-compose.sqlite.yml up -d
```

Build arguments of the Dockerfile: `NEXT_PUBLIC_API_BASE_URL` (empty means same
origin), `GIT_COMMIT`, `BUILD_TIME`, `REFRESH_CATALOG_SNAPSHOTS`. The build
metadata is reported by `GET /api/v1/version`.

## What every profile has in common

* Compose project `riochat`, container `riochat-app`, network `riochat-network`.
* The app is published on `127.0.0.1:8080` only. Put a TLS reverse proxy in front
  of it for public access. If you change `server.http_port`, change the port
  mapping to match.
* `deploy/config.yaml` is mounted read-only at `/app/config.yaml`.
* Named volumes `riochat-app-data` (`/app/data`, the SQLite database) and
  `riochat-app-storage` (`/app/storage`, uploaded files). Back both up.
* `host.docker.internal` resolves to the Docker host, so a PostgreSQL or Redis on
  the host can be reached from the container.

Configuration precedence is environment variables, then `config.yaml`, then
built-in defaults. The full profile sets `POSTGRES_DSN`, `REDIS_ADDR`,
`REDIS_USERNAME` and `REDIS_PASSWORD` on the app service, so they win over the
file. Business settings (models, sign-up rules, billing, moderation) are stored
in the database and edited in the admin console, not here.

## Optional extraction services

`services/rapidocr` (port 8002), `services/tesseract` (8004), `services/docling`
(8005) and `services/tika` (9998) each have their own `docker-compose.yml`. They
join the existing `riochat-network` (start an application profile first, or run
`docker network create riochat-network`) and publish on loopback only:

```sh
docker compose -f deploy/services/rapidocr/docker-compose.yml up -d --build
```

Point the application at `http://host.docker.internal:8002/ocr` (RapidOCR),
`:8004/ocr` (Tesseract), `:8005/ocr` (Docling) or `http://host.docker.internal:9998`
(Tika), or at `http://riochat-<name>:<port>/ocr` from inside the network. The
three Python services answer `GET /healthz`, take a multipart `file` (and an
optional `page_ranges`) on `POST /ocr`, and reply 204 when no text was found.
Tesseract reads `TESSERACT_LANG`, `TESSERACT_PSM`, `TESSERACT_OEM`,
`TESSERACT_RENDER_SCALE` and `TESSERACT_GRAYSCALE` from the host environment;
RapidOCR reads `RAPIDOCR_RENDER_SCALE`.

## Replace the demo values before production

The templates and the full profile ship **demo credentials for local use only**:
the development JWT secret and data-encryption key, PostgreSQL password
`riochat-demo-password` and Redis password `riochat-demo-redis`. Anyone who has
read this repository knows them.

Before the stack is reachable by anyone else:

1. In `config.yaml` set `app.env: prod` (the server then refuses placeholder
   secrets) and replace `security.jwt_secret` and `security.data_encryption_key`
   with new random values of 32 or more characters, for example
   `openssl rand -hex 32`.
2. Set `server.public_api_base_url` and `server.public_web_base_url` to your
   `https://` address (required in prod) and narrow `server.cors_allow_origin`
   to your own origin.
3. Set `security.ssrf_protection_enabled: true`.
4. For the full profile export `RIOCHAT_POSTGRES_PASSWORD` and
   `RIOCHAT_REDIS_PASSWORD` (letters and digits) or put them in `deploy/.env`
   before the first start. PostgreSQL only reads its password when the data
   volume is first created; changing it later requires `ALTER USER`.
5. Rotating the encryption key: put the new key in `data_encryption_key`, move
   the old one into `data_encryption_keys_previous`, restart. Stored secrets stay
   readable through the old key. This build has no background job that
   re-encrypts them with the new key (see `backend/GAPS.md`), so keep the old key
   listed.

## Logs, health and shutdown

`GET /healthz` is a liveness check and `GET /readyz` checks the datastores. The
image has no `HEALTHCHECK` because it contains no HTTP client; probe from the
orchestrator or the reverse proxy. Keep `server.shutdown_timeout_seconds` below
the orchestrator's termination grace period (Compose waits 10 seconds by
default, `docker compose stop -t 30` waits longer).
