# API overview

This page describes how the HTTP API is shaped, not every route. Behavior of an
individual endpoint lives in the code of the area that owns it
(`backend/internal/features/<area>/`, with the paths registered in its
`Mount`). All business routes are under `/api/v1`.

## Conventions

### Response envelope

Every JSON response under `/api/v1` uses one envelope. Success:

```json
{ "errorMsg": "", "data": { } }
```

Failure:

```json
{
  "errorMsg": "authorization header is required",
  "errorCode": "auth.invalid_token",
  "details": null,
  "requestId": "…",
  "data": null
}
```

- On success `errorMsg` is empty, `data` holds the payload, and `errorCode`,
  `details` and `requestId` are omitted. Creation answers 201, everything else 200.
- On failure `data` is `null`, `errorMsg` is a short public message and
  `errorCode` is a stable dotted code such as `request.invalid_body`,
  `auth.invalid_credentials` or `feature.disabled`. Clients should branch on the
  code, never on the message. `details` is present only where there is
  something to say, for example field-level validation errors.
- Raw causes (database or upstream errors) never reach the response; they are
  recorded server-side under the same `requestId`.
- Lists answer `data: {"total": n, "results": [...]}`. Pagination is
  `?page=` (1-based, default 1) and `?page_size=` (default 20, max 1000).
- The `X-Request-ID` response header is sent on every response (it is the
  `requestId` of a failure body) and is exposed to browsers. Quote it in bug
  reports.

Status codes follow the usual meaning: 400 invalid input, 401 missing or
invalid session, 403 role or origin refused, 404 unknown or disabled feature,
409 conflict, 413 body too large (JSON bodies are capped at 4 MiB), 429 rate
limited (with `Retry-After`), 5xx server fault.

### Authentication

Send `Authorization: Bearer <access token>` on every `Auth` and `Admin` route.
Tokens come from the sign-in endpoints in `/auth/*`:

1. `POST /auth/login` (password), or the email-code, two-factor and
   external-provider flows (`/auth/login-options`, `/auth/2fa/*`,
   `/auth/providers/{slug}/authorize|exchange|callback`).
2. The response contains a short-lived access token and sets the refresh token as
   an HttpOnly cookie scoped to `/api/v1/auth`.
3. `POST /auth/refresh` (cookie, no body) returns a new access token. A refresh
   token is bound to a server-side session; `POST /auth/logout`, `/auth/logout-all`
   and `/auth/sessions/{id}/logout` end sessions immediately.

Access-token and refresh-token lifetimes and the rate limits are business
settings (defaults: 24 hours and 30 days). Rate limiting is off by default; when
an administrator switches it on, the base budgets are 60 requests per minute per
signed-in user and 30 per minute per client address on the public `/auth/*`
routes, and other route classes get a multiple of the base (reads, writes,
polling, uploads, payment callbacks). Administrators are exempt from the
per-user limit. A refused request answers 429 with `Retry-After` and
`X-RateLimit-*` headers.

Roles are `user`, `admin` and `superadmin`; `/admin/*` needs one of the last
two. Finer limits (models, quotas) come from permission groups and billing plans.

### Streaming

Long operations stream instead of returning one document.

- **Generation streams** (`POST /conversations/{id}/messages/stream`, the media
  generation endpoints, `GET /conversation-runs/{run_id}/stream` to resume):
  `Content-Type: application/x-ndjson`, a sequence of JSON documents written
  back to back (`POST /temporary-chat/messages/stream` streams the same way). The last event states the outcome (completed, interrupted,
  blocked by moderation, failed). A failure after the headers were sent is
  reported inside the stream, not as an HTTP status.
- **Active-run feed** (`GET /conversation-runs/stream`): server-sent events
  describing runs that are in progress.

Use `fetch` (not `EventSource`) so the bearer header can be sent.
Streaming responses are never gzip-compressed.

### Feature flags

`GET /capabilities` returns ten server-level flags: `multiUser`,
`registration`, `identityProviders`, `accountSecurity`, `announcements`,
`billingGating`, `usageMetering`, `contentModeration`, `sharing` and
`desktopDownload`. Routes of a disabled feature answer 404 `feature.disabled`.

## Outside `/api/v1`

| Route | Purpose |
|---|---|
| `GET /healthz` | Liveness. Always 200 while the process runs: `{"status":"ok","version":"…"}`. |
| `GET /readyz` | Readiness: pings the database and the cache (`{"status":"ok","checks":{"db":"ok","redis":"ok"}}`, the cache key is `redis` or `memory`). 503 `degraded` when one fails, 503 `draining` during shutdown. These two answer plain JSON, not the envelope. |
| `GET /manifest.webmanifest` | Web app manifest built from the branding settings (also at `/api/v1/branding/manifest.webmanifest`). |
| everything else (GET and HEAD) | The exported web client: the exact file, then `<path>.html`, then `<path>/index.html`, then the root `index.html`, and `404.html` only if there is no root page. Other methods and anything under `/api`, `/swagger`, `/healthz` or `/readyz` get the JSON 404 envelope. Needs `FRONTEND_DIST_DIR`. |

## Platform routes

| Route | Auth | Purpose |
|---|---|---|
| `GET /api/v1/version` | public | `product`, `version`, `commit`, `buildTime`, `buildID`. |
| `GET /api/v1/capabilities` | public | `{"features": {…}}` with the flags above. |
| `GET /api/v1/branding` | public | Title, icons and logo (see [branding.md](branding.md)). |

## Routes by area

Prefixes below are a summary, not a complete route list; each area owns its prefixes exclusively. Some routes exist only while their capability flag is on (see above).

| Area | Prefixes | Auth |
|---|---|---|
| auth | `/auth/*` (login, login options, register, password reset and change, 2FA, sessions, refresh, logout, `/auth/providers/{slug}/authorize\|exchange\|callback`, local-mode `/auth/local/exchange`), `/me`, `/me/username`, `/me/onboarding`, `/me/email/*`, `/me/2fa/*`, `/me/identities/*`, `/admin/auth/providers*` | public / user / admin |
| user | `/user/settings`, `/user/stats/activity`, `/users/{public_id}/avatar`, `/admin/users*` | user / public (avatar) / admin |
| conversation | `/conversations*` (including `/messages`, `/messages/stream`, `/share`, `/media/*/stream`, `/export`), `/messages/{id}`, `/conversation-runs/*`, `/conversation-projects`, `/files*`, `/shared-conversations/{share_id}`, `/temporary-chat/messages/stream`, `/context-artifacts/{id}`, `/runtime/chat-file-policy`, `/admin/conversation-events`, `/admin/conversations/export` | user (shared view is public) / admin |
| channel | `/models`, `/llm/icon-assets/{id}` (public), `/admin/llm/*` (upstreams, models, vendors, display groups, catalog, icons, settings) | user / public / admin |
| permissiongroup | `/admin/permission-groups` | admin |
| personalprovider | `/me/model-providers*`, `/admin/model-providers*` | user / admin |
| knowledgebase | `/knowledge-bases*` (including `/mine`), `/admin/knowledge-bases*` | user / admin |
| memory | `/memories/profile` | user |
| mcp | `/mcp/tools`, `/settings/mcp-policy`, `/admin/mcp/*` | user / admin |
| skill, promptreset, uicomponent | `/skills`, `/prompt-presets`, `/ui-components` (each with `/mine` and `/admin/...`) | user / admin |
| billing | `/billing/*` (account, config, overview, plans, usage, subscriptions, redemptions, payment checkout and provider callbacks under `/billing/payments/*`), `/admin/billing/*`, `/admin/payment-orders`, `/admin/redemptions` | user / admin / provider webhooks |
| announcement | `/announcements`, `/admin/announcements` | user / admin |
| settings | `/branding`, `/branding/manifest.webmanifest`, `/settings/*` (policies shown to clients), `/admin/settings/*` | public / user / admin |
| audit, errorlog | `/admin/audit-logs`, `/admin/error-logs`, `/admin/logs`, `/admin/call-logs`, `/admin/usage-statistics` | admin |
| contentmoderation | `/admin/content-moderation/*` (config, probe, stats, events) | admin |

For exact paths, read the area's `Mount` function; for the wire types the web
client expects, read `web/src/lib/api/types.ts` and the `api.ts` file in each
`web/src/features/<area>/` folder.

## Adding an endpoint

1. Put the route in the owning area's `Mount`, relative to `/api/v1`, on the
   group that matches its audience.
2. Return data with `httpx.OK`, `httpx.Created` or `httpx.Paged`, and failures
   with `httpx.Fail` and an error declared with `apperr.Define("<area>.<code>", …)`.
3. Bind bodies with `httpx.BindJSON` and `binding:"..."` struct tags.
4. Add a test using the test kit (`testkit.New`) that covers the success path,
   the error codes and the authorization boundary.
5. Add the web wrapper to `web/src/features/<area>/api.ts`.
