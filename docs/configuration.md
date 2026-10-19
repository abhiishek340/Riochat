# Configuration reference

Riochat reads static configuration once at start from three layers. The first
that defines a value wins:

1. environment variables
2. the YAML file: `CONFIG_FILE` if set, otherwise `config.yaml` in the working
   directory or its parent. Relative paths inside the file resolve against the
   file's own directory.
3. built-in defaults

[`backend/config.example.yaml`](../backend/config.example.yaml) is a minimal
starting point; the templates in [`deploy/`](../deploy/) (`config.example.yaml`,
`config.sqlite.example.yaml`, `config.full.example.yaml`) list every key and
match the Compose profiles (see [`deploy/README.md`](../deploy/README.md)). List-valued
keys (`cors_allow_origin`, `trusted_proxies`, `ssrf_allowed_*`) accept either a
comma separated string or a YAML list. A text variable that is set wins even
when it is empty; an unparsable number or boolean in a variable is ignored and the next
layer is used. A file that cannot be read or parsed is skipped with a warning on
standard error, so check the first log lines when a setting seems ignored.

Everything that is not listed here (sign-up rules, models and upstreams,
billing, moderation, announcements, token lifetimes, rate limits) is a business
setting stored in the database and edited in the admin console. Those apply
without a restart.

## Production rules

Whatever the environment, the server refuses to start when `DATABASE_DRIVER`,
`CACHE_DRIVER` or `STORAGE_BACKEND` is unknown, `DATA_ENCRYPTION_KEY` is empty, a
`TRUSTED_PROXIES` or `SSRF_ALLOWED_*` entry is malformed, an S3 backend has no
bucket or region, the SQLite pragma values are not in the allowed set, or
`TURNSTILE_SITEVERIFY_URL` is not a plain http(s) URL.

With `APP_ENV=prod` (the default when unset) the server also refuses to start unless:

- `JWT_SECRET` is set explicitly and is at least 16 characters (use 32 or more)
- `DATA_ENCRYPTION_KEY` is set explicitly and is at least 32 characters
- `CORS_ALLOW_ORIGIN` is not empty and does not contain `*`
- `PUBLIC_API_BASE_URL` and `PUBLIC_WEB_BASE_URL` are both set to https URLs with a host (not
  enforced in local mode)

`APP_ENV=dev` accepts built-in placeholder secrets and logs a warning for each
(`security_config_warning`). It also switches the log level from `info` to
`debug`, which includes every SQL statement; use it for development only.

## Variables

YAML paths are shown relative to the document root.

### Application and server

| Variable | YAML key | Default | Meaning |
|---|---|---|---|
| `APP_NAME` | `app.name` | `Riochat` | Name used in logs. |
| `APP_ENV` | `app.env` | `prod` | `dev` or `prod` (`development` and `production` are accepted). |
| `HTTP_PORT` | `server.http_port` | `8080` | Listen port. |
| `CORS_ALLOW_ORIGIN` | `server.cors_allow_origin` | `http://127.0.0.1:8080`, `http://localhost:8080` and the desktop origins | Allowed browser origins. A value replaces the default list; `tauri://localhost` and `http://tauri.localhost` are always allowed on top. A request whose `Origin` matches nothing gets 403 `cors.origin_forbidden`. An empty list means `*` (rejected in `prod`). |
| `TRUSTED_PROXIES` | `server.trusted_proxies` | empty | IPs or CIDRs of reverse proxies allowed to set `X-Forwarded-For` / `X-Real-IP`. Empty means those headers are ignored, so behind a proxy every client appears to have the proxy's address until this is set. The `deploy/` templates pre-fill loopback and the private ranges. |
| `PUBLIC_API_BASE_URL` | `server.public_api_base_url` | empty | Externally visible API origin. |
| `PUBLIC_WEB_BASE_URL` | `server.public_web_base_url` | empty | Externally visible web origin; used to absolutise manifest and icon URLs. |
| `FRONTEND_DIST_DIR` | `server.frontend_dist_dir` | `../apps/web/out` | Directory with the exported web client. The built-in default does not exist in this repository's layout, so set it (`../web/out` when running from `backend/`; the container image sets `/app/frontend/out`). If the directory is missing the server logs `frontend_static_disabled` and answers every non-API path with the 404 envelope. A relative path in the YAML file resolves against the file's directory, one in the variable against the working directory. |
| `HTTP_READ_HEADER_TIMEOUT_SECONDS` | `server.read_header_timeout_seconds` | `10` | |
| `HTTP_READ_TIMEOUT_SECONDS` | `server.read_timeout_seconds` | `120` | |
| `HTTP_IDLE_TIMEOUT_SECONDS` | `server.idle_timeout_seconds` | `120` | |
| `HTTP_MAX_HEADER_BYTES` | `server.max_header_bytes` | `1048576` | |
| `HTTP_SHUTDOWN_TIMEOUT_SECONDS` | `server.shutdown_timeout_seconds` | `10` | Keep below your orchestrator's termination grace period. |

### Security

| Variable | YAML key | Default | Meaning |
|---|---|---|---|
| `JWT_SECRET` | `security.jwt_secret` | development placeholder | Signs access and refresh tokens. |
| `DATA_ENCRYPTION_KEY` | `security.data_encryption_key` | development placeholder | Encrypts stored secrets. |
| `DATA_ENCRYPTION_KEYS_PREVIOUS` | `security.data_encryption_keys_previous` | empty | Retired keys, decrypt-only (comma separated in the variable). To rotate: set the new key, move the old one here, restart. Existing values stay readable through the old key. Automatic background re-encryption is not wired in this build (see `backend/GAPS.md`), so keep the old key listed. |
| `MCP_USER_CONTEXT_SECRET` | `security.mcp_user_context_secret` | empty | Needed only when a tool server config uses the signed user-context placeholder. |
| `SSRF_PROTECTION_ENABLED` | `security.ssrf_protection_enabled` | `false` | Blocks outbound requests to private and metadata networks. Turn on in production. It is enforced only when `APP_ENV=prod`; in `dev` the switch has no effect. |
| `SSRF_ALLOWED_HOSTS` | `security.ssrf_allowed_hosts` | empty | Exact hostnames exempt from the block (no scheme, port, path, IP or wildcard). |
| `SSRF_ALLOWED_CIDRS` | `security.ssrf_allowed_cidrs` | empty | CIDR ranges exempt from the block. |
| `TURNSTILE_SITEVERIFY_URL` | `security.turnstile_siteverify_url` | Cloudflare's endpoint | Override only for a compatible proxy. |

### Database and cache

| Variable | YAML key | Default |
|---|---|---|
| `DATABASE_DRIVER` | `database.driver` | `postgres` (`postgres` or `sqlite`; `postgresql`, `pg` and `sqlite3` are accepted) |
| `POSTGRES_DSN` | `database.postgres.dsn` | `host=127.0.0.1 port=5432 user=riochat password=riochat dbname=riochat sslmode=disable TimeZone=Asia/Shanghai` (a percent-encoded `TimeZone` is decoded) |
| `POSTGRES_MAX_OPEN_CONNS` / `POSTGRES_MAX_IDLE_CONNS` | `database.postgres.max_open_conns` / `max_idle_conns` | `30` / `10` |
| `POSTGRES_CONN_MAX_LIFETIME_MINUTES` / `POSTGRES_CONN_MAX_IDLE_TIME_MINUTES` | `database.postgres.conn_max_lifetime_minutes` / `conn_max_idle_time_minutes` | `60` / `10` |
| `SQLITE_PATH` | `database.sqlite.path` | `./data/riochat.db` |
| `SQLITE_DSN` | `database.sqlite.dsn` | empty (overrides path-derived DSN) |
| `SQLITE_MAX_OPEN_CONNS` | `database.sqlite.max_open_conns` | `1` |
| `SQLITE_BUSY_TIMEOUT_MS` | `database.sqlite.busy_timeout_ms` | `5000` |
| `SQLITE_CACHE_SIZE_KB` | `database.sqlite.cache_size_kb` | `20480` |
| `SQLITE_MMAP_SIZE_BYTES` | `database.sqlite.mmap_size_bytes` | `268435456` |
| `SQLITE_SYNCHRONOUS` / `SQLITE_TEMP_STORE` | `database.sqlite.synchronous` / `temp_store` | `NORMAL` / `MEMORY` (allowed: `OFF`, `NORMAL`, `FULL`, `EXTRA` / `DEFAULT`, `FILE`, `MEMORY`) |
| `CACHE_DRIVER` | `cache.driver` | `redis` (`redis` or `memory`; `mem` and `in-memory` are accepted) |
| `REDIS_ADDR` | `database.redis.addr` | `127.0.0.1:6379` |
| `REDIS_USERNAME`, `REDIS_PASSWORD`, `REDIS_DB` | `database.redis.username`, `password`, `db` | empty, empty, `0` |
| `REDIS_TLS_ENABLED`, `REDIS_TLS_INSECURE_SKIP_VERIFY` | `database.redis.tls_enabled`, `tls_insecure_skip_verify` | `false` |

Use Redis whenever more than one server process runs; the memory cache is
per process, so rate limits and one-time codes would not be shared.

### File storage

| Variable | YAML key | Default |
|---|---|---|
| `STORAGE_BACKEND` | `storage.backend` | `local` (`local` or `s3`) |
| `STORAGE_ROOT_DIR` | `storage.local.root_dir` | `./storage` (`/app/storage` in the container, a volume) |
| `STORAGE_S3_ENDPOINT`, `STORAGE_S3_REGION`, `STORAGE_S3_BUCKET`, `STORAGE_S3_PREFIX` | `storage.s3.*` | region `auto`, others empty |
| `STORAGE_S3_ACCESS_KEY_ID`, `STORAGE_S3_SECRET_ACCESS_KEY` | `storage.s3.*` | empty; when empty the standard `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` variables are used |
| `STORAGE_S3_FORCE_PATH_STYLE` | `storage.s3.force_path_style` | `true` |

### Geolocation and tracing

| Variable | YAML key | Default |
|---|---|---|
| `GEOIP_PROVIDER` | `geoip.provider` | `ipwhois`. Also `ipinfo`, `mmdb`, or `none` (empty and `disabled` also turn it off). |
| `GEOIP_BASE_URL`, `GEOIP_TOKEN`, `GEOIP_TIMEOUT_MS` | `geoip.*` | `https://ipwho.is` (`https://ipinfo.io` for `ipinfo`), empty, `2500` |
| `GEOIP_DATABASE_URL`, `GEOIP_DATABASE_PATH`, `GEOIP_DATABASE_MAX_BYTES`, `GEOIP_REFRESH_INTERVAL_HOURS` | `geoip.*` | empty, `./data/geoip/geoip.mmdb`, 100 MiB, `168` (only used by `mmdb`) |
| `OTEL_ENABLED` | `observability.tracing.enabled` | unset: tracing is on exactly when an endpoint is configured |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_INSECURE`, `OTEL_EXPORTER_OTLP_PROTOCOL` | `observability.tracing.*` | empty, empty, `false`, `grpc` |
| `OTEL_TRACES_SAMPLER_ARG` or `OTEL_SAMPLING_RATE` | `observability.tracing.sampling_rate` | `1` |

GeoIP is used to record a coarse location (country, region, city) for sign-in
sessions. The default provider sends the client address to an external lookup
service; set `none` if that is not acceptable, or use `mmdb` with a MaxMind-format
database file (downloaded from `GEOIP_DATABASE_URL` and refreshed every
`GEOIP_REFRESH_INTERVAL_HOURS` when a URL is given). Local mode forces `none`.

Tracing exports spans as OTLP over HTTP (JSON). The `grpc` protocol value is
accepted but is still sent over HTTP: the server logs `otel_grpc_exporter_unavailable`,
so point the endpoint at the collector's HTTP port and set the protocol to
`http`. An endpoint without a path gets `/v1/traces`; one without a scheme
becomes `https://` (`http://` when `OTEL_EXPORTER_OTLP_INSECURE` is true).
Enabling tracing without an endpoint is a startup error. Spans cover outbound
HTTP calls and embedding requests; database and Redis calls are not instrumented.

### Other environment variables

| Variable | Meaning |
|---|---|
| `CONFIG_FILE` | Path of the YAML file. Without it, `config.yaml` in the working directory, then in its parent. The container image reads `/app/config.yaml`. |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` (and lower-case spellings) | Proxy for outbound calls to providers and tool servers. `ALL_PROXY` is not honored and is reported in the log. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | Fallback S3 credentials (see above). |
| `KUBERNETES_NAMESPACE`, `KUBERNETES_POD_NAME`, `KUBERNETES_POD_UID` | Added as resource attributes on exported traces. |
| `RIOCHAT_CHAT_IMAGE`, `RIOCHAT_POSTGRES_PASSWORD`, `RIOCHAT_REDIS_PASSWORD` | Read by the Compose files in `deploy/`, not by the server. |

### Branding (YAML only)

See [branding.md](branding.md). Keys live under `branding:`.

### Command line

| Flag | Meaning |
|---|---|
| `--local` | Single-user local mode (SQLite, memory cache, local storage, loopback). |
| `--data-dir <path>` | Data directory for local mode. Required with `--local`, rejected without it; a relative path is made absolute. |

Local mode keeps `riochat.db`, `storage/` and `secrets.json` (generated random
session and encryption secrets, created with mode 0600) under the data
directory, listens on `127.0.0.1` with a random port, and serves the API only
(no web client is mounted; the desktop shell brings its own). Its account is a
single passwordless `owner`, and only the usage metering feature flag is on.
Environment variables and the YAML file still apply to everything local mode
does not force, such as the HTTP timeouts.

### Web client build and dev

| Variable | Used by | Meaning |
|---|---|---|
| `NEXT_PUBLIC_API_BASE_URL` | `pnpm build` | API origin baked into the export. Empty (default) means same origin. |
| `RIOCHAT_BACKEND_URL` | `pnpm dev` | Where the dev server proxies `/api/*` and `/manifest.webmanifest`. Default `http://127.0.0.1:8080`. |
| `PORT` | `pnpm start` | Port of the convenience server that serves `web/out` (default 3000). Production hosting is the Go server. |
