# Desktop signing secrets

This directory is local only. Git ignores everything in it except this file and
`desktop-signing.env.example`; the whole `deploy/` directory is also kept out of
every Docker build context. Do not put anything here that you are not prepared
to lose, and never commit it.

Expected contents:

| File | What it is |
|---|---|
| `updater.key` | Updater private key, password protected. |
| `updater.key.pub` | Matching public key. It is embedded in the desktop updater configuration. |
| `developer-id.p12` | Apple Developer ID Application certificate with its private key. |
| `desktop-signing.env` | Your copy of `desktop-signing.env.example`. |

## Back up the updater key

Installed clients accept an update only if it is signed with the key whose
public half they were built with. If the private key or its password is lost,
installed clients can never be updated again and every user has to reinstall by
hand. Keep an offline, durable backup of the private key and the password in
separate places before the first public release.

## Environment keys

See `desktop-signing.env.example` for the full list:
`TAURI_SIGNING_PRIVATE_KEY_PATH`, `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`,
`APPLE_CERTIFICATE_PATH`, `APPLE_CERTIFICATE_PASSWORD`, `APPLE_ID`,
`APPLE_PASSWORD`, the optional `SIGN_AND_NOTARIZE_*` group (all four values or
none) and `WINDOWS_CERTIFICATE_PATH`, `WINDOWS_CERTIFICATE_PASSWORD`.

Continuous integration uses the same names as repository secrets. There, the
certificate entries hold the base64 of the file instead of a path.

## Local signed build

`make release-desktop` loads `desktop-signing.env` when it exists, builds the
desktop app, and on macOS packages the DMG and verifies the result: signature,
architecture and a Gatekeeper assessment. Without the file it builds unsigned
and says so.
