# Riochat desktop shell

A Tauri 2 host for the statically exported web client. It adds a tab strip (one
webview per server), an optional bundled server ("local mode"), keychain storage
for sign-in tokens, tray, macOS menu, downloads, proxy support, enterprise policy
and auto-update. It contains no chat logic.

```
desktop/
  src-tauri/        Rust crate (riochat_desktop_lib) and Tauri configuration
    src/            modules, see below
    capabilities/   permissions per webview class (chrome strip, content tabs)
    binaries/       the riochat-server sidecar, built by scripts/build-sidecar.mjs
  scripts/          build, packaging and release tooling (Node, no dependencies)
  tests/            Node tests for the tooling (node:test)
```

## Rust modules

| Module | Role |
|---|---|
| `shell` | ties the tab model to webviews, sessions, proxy and commands |
| `tabs` | pure tab model, persistence (`tabs.json`) and restore rules |
| `commands` | the IPC surface; also declared in `build.rs` |
| `sidecar` | supervises `riochat-server --local --data-dir ...` and its handshake |
| `session` | refresh and local-grant HTTP calls (no redirects, 256 KiB cap) |
| `creds` | OS credential store (`refresh-token:<server>`, `proxy-password`) |
| `proxy` | settings, bypass lists, OS/env detection, resolution, client routing |
| `policy` | Windows registry policy (`SOFTWARE\Policies\Riochat`) |
| `oauth` | loopback listener and response page for third-party sign-in |
| `downloads`, `window`, `menu`, `tray` | OS integration |
| `portable`, `distribution`, `bootstrap`, `updates` | distribution kinds and updates |

## Everyday commands

Run from `desktop/` (or with `pnpm --filter @riochat/desktop <script>`):

| Script | What it does |
|---|---|
| `dev` | builds the sidecar, then `tauri dev` with `tauri.dev.conf.json` (separate identifier, keychain service and data); reuses a web dev server already on port 3000 |
| `bundle` | version check, sidecar, `tauri build` |
| `bundle:fast` | sidecar, then an app-only bundle with thin LTO |
| `bundle:signed` | signed local build using `deploy/secrets/desktop-signing.env` |
| `sidecar` | only the server binary (`--target <triple>` to cross-select) |
| `check` | version check, sidecar, `cargo check` |
| `version:check` / `version:sync` | keep every manifest equal to `VERSION` |
| `package:portable` | Windows portable zip |
| `test` / `test:rust` | tooling tests / crate unit tests |

`cargo test --lib`, `cargo clippy --all-targets -- -D warnings` and
`cargo fmt --check` run in `src-tauri/` and need `binaries/riochat-server-<host
triple>` to exist (any file will do for these commands; `pnpm sidecar` builds the
real one) and `../web/out` to exist.

Release helpers (`release-config.mjs`, `release-assets.mjs`, `checksums.mjs`,
`refresh-beta-manifest.mjs`, `prepare-appimage.mjs`, `verify-macos-bundle.mjs`,
`post-dmg.mjs`, `apple-identity.mjs`) are documented at the top of each file.
Decisions taken where the specification was silent are in the repository
`GAPS.md`, section "desktop".
