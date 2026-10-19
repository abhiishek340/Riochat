# Security policy

## Reporting a vulnerability

Please report security problems privately. Open a report through the
repository's **Security** tab ("Report a vulnerability", the private security
advisory form). Do not file a public issue, discussion or pull request for a
suspected vulnerability.

Include as much of the following as you can:

- what is affected (component, route, version from `GET /api/v1/version` or the
  `VERSION` file) and how you deployed it
- the impact you believe it has
- step-by-step reproduction, or a proof of concept
- relevant logs, with secrets removed
- whether you have already told anyone else

We aim to acknowledge a report within a few working days, keep you informed
while we investigate, and credit you in the advisory if you wish.

## Scope

In scope:

- authentication, session and token handling, two-factor flows
- authorization: access to another user's data, role or permission bypass
- injection, cross-site scripting, request forgery, open redirects
- server-side request forgery through user-supplied addresses
- exposure or weak handling of stored secrets, keys and tokens
- unsafe file upload, download or parsing behavior
- billing, quota or payment-callback bypasses
- vulnerabilities in the desktop shell or its updater

Out of scope:

- attacks that need physical access to a device or server
- social engineering of maintainers or users
- denial of service without a practical exploit (for example raw traffic floods)
- flaws in third-party services or in upstream dependencies that we do not
  ship in modified form (report those upstream)
- findings that only apply to a deployment that ignores the guidance below
- reports produced by automated scanners with no demonstrated impact

## Supported versions

Fixes land on the `main` branch first. Only the latest release is supported
unless an advisory says otherwise.

## Deploying safely

The development defaults are not secure. For a production deployment:

- run with `APP_ENV=prod` and set strong random values for `JWT_SECRET` and
  `DATA_ENCRYPTION_KEY`; never reuse the placeholder values
- serve over HTTPS (terminate TLS at a reverse proxy) and set
  `PUBLIC_API_BASE_URL` and `PUBLIC_WEB_BASE_URL` to https URLs
- restrict `CORS_ALLOW_ORIGIN` to your own origins and list your reverse
  proxies in `TRUSTED_PROXIES`
- enable `SSRF_PROTECTION_ENABLED` (it is enforced only when `APP_ENV=prod`)
- use PostgreSQL and Redis rather than SQLite and the in-memory cache
- do not use the sample configuration file or any development setup as-is
- back up the database and the file storage, and protect the backups like the
  live data

See `docs/configuration.md`.
