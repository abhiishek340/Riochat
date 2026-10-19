## Summary

<!-- What changes, and why? Link related issues. -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Documentation
- [ ] Refactor (no behavior change)
- [ ] Configuration or deployment
- [ ] Security hardening
- [ ] Other:

## Areas touched

- [ ] Web client
- [ ] Backend / API
- [ ] Authentication and sessions
- [ ] Conversations and streaming
- [ ] Files, retrieval, knowledge bases
- [ ] Model routing and providers
- [ ] Tool servers (MCP) and skills
- [ ] Billing and payments
- [ ] Admin console
- [ ] Desktop shell
- [ ] CI, packaging or configuration
- [ ] Documentation

## How it was verified

<!-- Commands you ran and their result, tests added, manual steps. -->

- [ ] `make check` in `backend/` passes (if the server changed)
- [ ] `pnpm typecheck && pnpm lint && pnpm test && pnpm build` in `web/` pass (if the client changed)

## Screenshots or logs

<!-- Before/after images for UI changes; redacted logs for behavior changes. -->

## Configuration, migration and compatibility notes

<!-- New or changed settings, schema changes, API shape changes, breaking changes, upgrade steps. Write "none" if none. -->

## Checklists

Documentation

- [ ] README, `docs/` or the architecture files are updated where needed
- [ ] Decisions on unclear requirements are recorded in `GAPS.md`

Security and privacy

- [ ] No secrets, tokens or personal data are committed or logged
- [ ] Data is scoped to the authenticated user and roles are checked
- [ ] Security-sensitive behavior has been reviewed and has a regression test

General

- [ ] The change is focused on one concern
- [ ] Commit subjects follow `type: subject`
- [ ] No build output, caches, `.env` files or local data are included
