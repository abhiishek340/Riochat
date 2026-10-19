# Windows rollout guide

For administrators deploying the Riochat desktop application to Windows
machines. The policy templates are in `deploy/windows/`.

## Choosing a package

| Package | Install scope | Updates | Notes |
|---|---|---|---|
| MSI | Per machine | None inside the app; deploy new MSIs | Has a fixed upgrade code that never changes, so a newer MSI upgrades an older one in place. Preferred for managed fleets. |
| NSIS setup (`.exe`) | Per user | Automatic, in the app | Needs no administrator rights. |
| Portable zip | None (unpack anywhere) | Notify only; the user installs the new zip | Keeps its data in a `data` folder next to the executable. |
| Offline portable zip | None | Notify only | Includes the WebView2 fixed runtime. Published only when it was built. |

Prerequisites: Windows 10 or later and the Microsoft Edge WebView2 runtime. The
offline portable zip carries its own runtime and does not need it installed.

## Verify a download

Each release has a `SHA256SUMS` file, signed Windows binaries and GitHub build
provenance attestations.

```powershell
Get-FileHash .\Riochat_x64-setup.exe -Algorithm SHA256   # compare with SHA256SUMS
Get-AuthenticodeSignature .\Riochat_x64-setup.exe        # Status must be Valid
gh attestation verify .\Riochat_x64-setup.exe --repo <owner>/<repo>
```

Binaries are signed only when the release was built with a certificate. An
unsigned build is reported as such in the release notes and should not be
deployed to managed machines.

## Silent install

```bat
msiexec /i Riochat_x64.msi /qn
msiexec /x Riochat_x64.msi /qn
Riochat_x64-setup.exe /S
```

Use your deployment tool's own logging switches (`/l*v install.log` for
`msiexec`) when you need a log.

## Policies

The application reads these values at start from `HKLM\SOFTWARE\Policies\Riochat`
and `HKCU\SOFTWARE\Policies\Riochat`. If both hives set a value, the machine
hive wins, value by value.

| Value | Type | Meaning |
|---|---|---|
| `DefaultServerUrl` | String (up to 2048) | Prefills the server address. An http or https origin without a path. |
| `LockServerUrl` | DWORD `1` | Users can connect only to the default server. Ignored when no default server is set. Also disables the built-in local server; saved tabs for other servers and their stored sign-in are removed. |
| `DisableLocalMode` | DWORD `1` | Disables the bundled local server. |
| `DisableAutoUpdate` | DWORD `1` | No update checks and no update notifications. |
| `ProxyMode` | String | `system`, `direct` or `fixed_servers`. |
| `ProxyServer` | String (up to 2048) | `host:port`, `http://host:port` or `socks5://host:port`, without credentials. Required for `fixed_servers`. |
| `ProxyBypassList` | String (up to 8192) | Host names, wildcard domains, address prefixes and CIDR ranges that skip the proxy. The local machine is always reached directly. |

The hive that sets `ProxyMode` decides all three proxy values.

### Group Policy

1. Copy `Riochat.admx` to `C:\Windows\PolicyDefinitions\` (or the central store
   `\\<domain>\SYSVOL\<domain>\Policies\PolicyDefinitions\`).
2. Copy `en-US\Riochat.adml` into the matching language folder next to it.
3. Open the Group Policy editor. The settings appear under
   Computer Configuration (or User Configuration) > Administrative Templates >
   Riochat.

### Intune

Import `Riochat.admx` and the `.adml` for your language under Devices >
Configuration > Import ADMX, then create a profile that sets the Riochat
settings. Alternatively, deploy the values in `riochat-policy.example.reg`
through a script or a remediation.

`deploy/windows/riochat-policy.example.reg` is an example for `HKLM`: the first
four values are enabled with sample values and the proxy values are commented
out. The file must keep Windows (CRLF) line endings; the repository enforces this
through `.gitattributes`.

## Where data and credentials live

* Installed builds keep their data in the per-user application data folders
  named after the application identifier `com.riochat.desktop`
  (`%APPDATA%` and `%LOCALAPPDATA%`).
* The portable build keeps its data in a `data` folder beside the executable and
  uses a credential-store service name that is unique to that instance, so
  copies do not share sign-ins.
* Refresh tokens and proxy passwords are kept in the Windows credential store.
  They are never written to disk in plain text.
