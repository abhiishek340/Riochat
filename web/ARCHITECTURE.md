# Riochat web: architecture

The web client is a Next.js (App Router, TypeScript strict, Tailwind 4) app that is
**statically exported** (`out/`). The Go backend hosts the export; there is no Node
runtime. Everything below follows from that: no server components that need data, no
dynamic route segments (ids travel in query strings), no middleware.

Read this before adding a feature. `GAPS.md` lists the decisions taken where the specs
were silent.

## 1. Commands

| Command | What it does |
|---|---|
| `pnpm install` | install (native build scripts are allow-listed in `pnpm-workspace.yaml`) |
| `pnpm dev` | dev server on :3000; `/api/*` and `/manifest.webmanifest` are proxied to `RIOCHAT_BACKEND_URL` (default `http://127.0.0.1:8080`) |
| `pnpm typecheck` / `pnpm lint` / `pnpm test` | `tsc --noEmit`, ESLint, Vitest (pure logic) |
| `pnpm build` | static export into `out/` |
| `pnpm start` | tiny static server for `out/` (convenience only) |
| `pnpm icons` | regenerate `public/icon.svg`, `favicon.ico`, `public/pwa/*.png` |

Dev proxy note: the browser sends `Origin: http://localhost:3000`, which the proxy
forwards, and the backend rejects origins that are not in its CORS allow-list. Start the
backend with `CORS_ALLOW_ORIGIN=http://localhost:3000` (add the dev port you use).

## 2. The feature convention

```
src/
  app/                  ROUTES ONLY. Thin files: import a page component, render it.
  features/<area>/      one folder per product area; index.ts is its public surface
  components/ui/        UI kit (button, input, field, dialog, dropdown, tooltip, toast, table, tabs, ...)
  components/shell/     app shell, sidebar, header, guards, layout frames
  components/providers/ root provider stack
  components/brand/     logo / mark
  lib/api/              HTTP client, session store, refresh, streaming, wire types
  lib/session/          session provider, auth guard, feature policy
  lib/capabilities/     server feature flags, FeatureGate, section guard
  lib/branding/         branding fetch + <head> application
  lib/i18n/             provider, ICU-subset formatter, catalogs, error text
  lib/theme/            tokens' runtime half: mode + accent, no-flash script
  config/navigation.ts  sidebar entries (app + admin) with capability gating
  hooks/                small shared hooks
```

Rules:

1. **A route file is thin.** `src/app/(app)/files/page.tsx` is
   `import { FilesPage } from "@/features/files"; export default () => <FilesPage />;`
   plus a `<Suspense>` when the page reads search params (required by static export).
   No logic, no data fetching, no styling in `app/`.
2. **A feature exports its routes' page components from `src/features/<area>/index.ts`**
   and nothing else that other features should depend on (shell slots such as
   `ConversationSidebar` are the one exception). Other code imports a feature only
   through that index; ESLint (`no-restricted-imports`) enforces it.
3. **Features own their code, messages and API calls.** API wrappers for an area live in
   `src/features/<area>/api.ts`, built on `api` from `@/lib/api/client`. Features do not
   import each other; shared needs move to `lib/` or `components/`.
4. **Pages are client components** (`"use client"` at the top of the feature file).
5. **Messages**: add `src/lib/i18n/catalog/en-US/<ns>.ts`, then one line in its
   `index.ts`. Keys are typed (`t("ns.group.key")`).
6. **Navigation**: add an entry to `src/config/navigation.ts` (with `feature:` for a
   server capability or `policy:` for an admin toggle). The shell hides it automatically.

A feature may split its pages into as many files and subfolders as it needs; keep the
export names of its `index.ts` stable, because the thin route files import them.

| Area | Folder | Routes |
|---|---|---|
| auth | `features/auth` | `/login`, `/auth/callback` (minimal login included) |
| chat | `features/chat` | `/chat`; `ConversationSidebar` slot in the app sidebar |
| files, recent, library, knowledge-bases | same names | `/files`, `/recent`, `/library`, `/knowledge-bases` |
| settings | `features/settings` | `/settings/*` (layout + 7 pages), `/import` |
| share | `features/share` | `/share?id=...` (public) |
| admin | `features/admin` | `/admin/*` (16 pages) |

## 3. Route groups and layouts

- `app/layout.tsx`: root. Starts with `data-branding="pending"` on `<html>` (body hidden
  until branding and the catalog are ready, capped at 3 s), inlines the theme script, and
  mounts `RootProviders`.
- `app/(auth)/`: signed-out pages in a centred card (`AuthLayoutFrame`).
- `app/(app)/`: signed-in workspace: `AuthGuard` -> `FeaturePolicyProvider` -> `AppShell`
  (sidebar + header). Settings pages sit under `(app)/settings/layout.tsx`, which renders
  the feature's `SettingsLayout`.
- `app/admin/`: `AuthGuard` -> `AdminGate` (role check) -> `AppShell` with admin nav.
- `app/share/`: public frame, no session. `app/import/`: deliberately outside `(app)`;
  metadata sets `referrer: no-referrer` and noindex so a key in the URL fragment never leaks.

Provider order (`components/providers/root-providers.tsx`): Branding -> I18n -> Theme ->
Tooltip -> Session -> Capabilities; the toast host and `AppVersionGuard` sit beside them.

Shell extension points for pages: `usePageTitle("...")` overrides the header title;
`<HeaderActions>...</HeaderActions>` portals buttons into the header.

## 4. API client (`lib/api`)

- Everything is under `/api/v1`; base URL = runtime override -> `NEXT_PUBLIC_API_BASE_URL`
  -> same origin. Resolved for each request.
- `request()` unwraps the envelope `{data, errorMsg, errorCode, requestId, details}` and
  throws `ApiError` (status, errorCode, details, requestId, retryAfterSeconds). Fetch
  failures become `NetworkError` (`errors.network.unavailable`); aborts pass through.
- **Use `api`** (`api.get/post/put/patch/delete/raw`) for authenticated calls and
  `publicApi` for open endpoints. `api` sends the in-memory bearer token; on a 401 that is
  not session-ending it refreshes once (coalesced, cross-tab locked via Web Locks) and
  retries once. A session-ending 401 clears the session; `AuthGuard` then sends the user
  to `/login?next=...`.
- Session (`session-store.ts`): token in memory only (never storage), revision counter,
  BroadcastChannel sync between tabs, window events `riochat:session-snapshot-changed`
  and `riochat:session-cleared`. The refresh token is an HttpOnly cookie (path
  `/api/v1/auth`).
- Streaming (`stream.ts`): `postConversationStream` / `resumeRunStream` consume the
  back-to-back-JSON generation protocol with the spec's outcome rules (completed,
  interrupted, moderation block, missing payload); `followActiveRuns` reads the SSE
  active-run feed. Both use fetch so the bearer header works.
- Wire types for shared endpoints are in `types.ts`; features declare their own.

Typical feature call:

```ts
const files = await api.get<Paged<FileObject>>("/files", { query: { page: 1, page_size: 20 } });
```

## 5. Capabilities and feature policy

`useCapabilities()` gives the 10 server flags (all on until the server says otherwise);
`<FeatureGate feature="sharing">` hides UI; `useSectionGuard("sharing")` redirects away
from a disabled section. A `feature.disabled` response refreshes the cache. Admin toggles
(`/settings/feature-policy`) come from `useFeaturePolicy()` (fail-open).

## 6. i18n

Own small implementation (static export makes URL-based locales awkward). The only locale is
`en-US`; the plumbing for more stays in place. Cookie `riochat_locale`, account locale applied once the
user loads. Messages use an ICU subset: `{name}`, `plural`, `select`. Every message
receives `{brand}` automatically (the branding title), so catalogs never hard-code the
product name. `useErrorMessage()` turns any thrown value into localised text: API error
codes map to `errors.<camelCased.code>`, with special cases for `feature.disabled` and
`request.invalid_body` field errors; areas can add code families with
`registerErrorFormatter("settings.", fn)`.

## 7. Design system

Tokens are CSS variables in `src/app/globals.css` (OKLCH), mapped into Tailwind through
`@theme inline`. Neutrals are warm paper (light) and blue ink (dark); one accent hue
drives all coloured surfaces, so presets (`tide`, `ember`, `moss`, `dusk`) are two lines
(`[data-accent=...]`). Mode and accent are classes/attributes on `<html>` set before first
paint. Use semantic classes (`bg-card`, `text-muted-foreground`, `border`), never raw
colours. UI kit components are in `components/ui` and re-exported from `@/components/ui`;
`Field` + `FieldLabel/Hint/Error` wire ids and ARIA to inputs automatically.

## 8. Branding and PWA

`GET /branding` (cached per server, one retry, 3 s cap) updates title, description,
icons and the manifest link in `<head>` (static defaults are declared in the root layout
metadata, so nothing duplicates). The manifest itself is the backend's
`/api/v1/branding/manifest.webmanifest`. Default icon files the backend names
(`/favicon.ico`, `/pwa/*.png`, `/riochat-icon.svg`) ship in `public/`.

## 9. Not built yet

The `/desktop/tabs` screen and the other items listed in `GAPS.md` are not built.
`window.__RIOCHAT_DESKTOP__` and `setApiBaseOverride` are the hooks the desktop layer is
expected to use.
