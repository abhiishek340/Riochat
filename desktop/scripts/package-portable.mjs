#!/usr/bin/env node
// Packages the Windows portable distribution (a zip with a single Riochat/ folder).
//
//   node scripts/package-portable.mjs [--version v] [--webview2 dir] [--out-dir dir]
//        [--exe path] [--sidecar path] [--target triple]
//
// Prints the path of the zip. Giving --webview2 produces the offline variant
// that carries a Fixed Version WebView2 runtime.

import { existsSync, mkdirSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { entryDate, writeZip } from "./lib/zip.mjs";
import { desktopRoot, isMain, parseFlags, tauriConfigPath, tauriDir } from "./lib/util.mjs";

export const DEFAULT_TRIPLE = "x86_64-pc-windows-msvc";
export const ROOT_FOLDER = "Riochat";
const ARCHES = { x86_64: "x64", aarch64: "arm64", i686: "x86" };

export function archLabel(triple) {
  const arch = ARCHES[String(triple).split("-")[0]];
  if (!arch || !String(triple).includes("-pc-windows-")) {
    throw new Error(`unsupported target "${triple}" for a Windows portable package`);
  }
  return arch;
}

export function zipName({ version, triple, offline }) {
  return `Riochat-${version}-windows-${archLabel(triple)}-portable${offline ? "-offline" : ""}.zip`;
}

const crlf = (text) => text.replace(/\r?\n/g, "\r\n");

export const MARKER_TEXT = crlf(
  "This file marks a portable copy of Riochat. Keep it next to Riochat.exe; delete it to stop using portable mode.\n",
);

export function readmeText({ version, offline }) {
  const en = `Riochat ${version} (portable)

Requirements
  - Windows 10 or 11.
  - The Microsoft Edge WebView2 runtime. ${
    offline
      ? "This package includes it in the webview2 folder, so nothing needs to be installed."
      : "Windows 11 and current Windows 10 already have it; otherwise install it from https://developer.microsoft.com/microsoft-edge/webview2/"
  }

Getting started
  1. Extract the whole zip to a folder you can write to (not a read-only disk or Program Files).
  2. Run Riochat.exe.
  3. Choose a server to connect to, or use the built-in local server.

Where your data is
  Everything is stored in the "data" folder next to Riochat.exe: settings, the local server's database and files, and the web engine profile. Nothing is written anywhere else.
  Sign-in tokens and the proxy password are kept in the Windows Credential Manager, in entries whose names start with com.riochat.desktop.portable-

Updating
  Riochat tells you when a new version exists. Download the new zip and extract it over the existing folder; your "data" folder is kept.

Removing
  Delete the Riochat folder. To also remove saved sign-ins, open Credential Manager (Windows Credentials) and delete the entries whose names start with com.riochat.desktop.portable-
`;
  return crlf(en);
}

/** The built executable name comes from the package name in Cargo.toml. */
export function packageName(cargoToml) {
  const text = readFileSync(cargoToml, "utf8");
  let inPackage = false;
  for (const line of text.split(/\r?\n/)) {
    if (/^\s*\[/.test(line)) inPackage = /^\s*\[package\]\s*(#.*)?$/.test(line);
    const m = inPackage && /^\s*name\s*=\s*"([^"]+)"/.exec(line);
    if (m) return m[1];
  }
  throw new Error(`no package name found in ${cargoToml}`);
}

function newest(paths) {
  const found = paths
    .filter((p) => existsSync(p))
    .map((p) => ({ p, t: statSync(p).mtimeMs }))
    .sort((a, b) => b.t - a.t);
  return found.length > 0 ? found[0].p : null;
}

/** The newest of the per-triple and default release folders. */
export function discoverExe({ triple, name, src = tauriDir }) {
  const found = newest([
    path.join(src, "target", triple, "release", `${name}.exe`),
    path.join(src, "target", "release", `${name}.exe`),
  ]);
  if (!found) throw new Error(`no ${name}.exe found; run the Windows build first (pnpm --filter @riochat/desktop bundle)`);
  return found;
}

/** Bundler-signed binaries/ copy first, then per-triple, then default release folders. */
export function discoverSidecar({ triple, src = tauriDir }) {
  const candidates = [
    path.join(src, "binaries", `riochat-server-${triple}.exe`),
    path.join(src, "target", triple, "release", "riochat-server.exe"),
    path.join(src, "target", "release", "riochat-server.exe"),
  ];
  const found = candidates.find((p) => existsSync(p));
  if (!found) throw new Error("no riochat-server.exe found; run the sidecar build first (pnpm --filter @riochat/desktop sidecar)");
  return found;
}

/** Accepts the runtime folder itself, or a folder whose only subfolder is the runtime. */
export function resolveWebview2(dir) {
  const hasRuntime = (d) => existsSync(path.join(d, "msedgewebview2.exe"));
  if (!existsSync(dir) || !statSync(dir).isDirectory()) throw new Error(`WebView2 folder not found: ${dir}`);
  if (hasRuntime(dir)) return dir;
  const subdirs = readdirSync(dir, { withFileTypes: true }).filter((e) => e.isDirectory());
  if (subdirs.length === 1 && hasRuntime(path.join(dir, subdirs[0].name))) return path.join(dir, subdirs[0].name);
  throw new Error(`${dir} does not contain the WebView2 runtime (msedgewebview2.exe)`);
}

function walk(dir, base, out) {
  const entries = readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  for (const e of entries) {
    const full = path.join(dir, e.name);
    const rel = `${base}/${e.name}`;
    if (e.isDirectory()) {
      out.push({ name: rel, dir: true });
      walk(full, rel, out);
    } else if (e.isFile()) {
      out.push({ name: rel, data: readFileSync(full), mode: 0o644 });
    }
  }
}

/** The archive contents, in a stable order, under the single root folder. */
export function collectEntries({ exe, sidecar, webview2, version }) {
  const offline = Boolean(webview2);
  const entries = [
    { name: `${ROOT_FOLDER}/`, dir: true },
    { name: `${ROOT_FOLDER}/Riochat.exe`, data: readFileSync(exe), mode: 0o755 },
    { name: `${ROOT_FOLDER}/riochat-server.exe`, data: readFileSync(sidecar), mode: 0o755 },
    { name: `${ROOT_FOLDER}/portable`, data: Buffer.from(MARKER_TEXT, "utf8") },
    { name: `${ROOT_FOLDER}/README.txt`, data: Buffer.from(readmeText({ version, offline }), "utf8") },
  ];
  if (offline) {
    entries.push({ name: `${ROOT_FOLDER}/webview2/`, dir: true });
    walk(webview2, `${ROOT_FOLDER}/webview2`, entries);
  }
  return entries;
}

export function parseArgs(argv) {
  const { flags, positional } = parseFlags(argv, {
    version: "value",
    webview2: "value",
    "out-dir": "value",
    exe: "value",
    sidecar: "value",
    target: "value",
  });
  if (positional.length > 0) throw new Error(`unexpected argument ${positional[0]}`);
  return {
    version: flags.version,
    webview2: flags.webview2,
    outDir: flags["out-dir"],
    exe: flags.exe,
    sidecar: flags.sidecar,
    target: flags.target,
  };
}

export function packagePortable(options, env = process.env) {
  const triple = options.target ?? DEFAULT_TRIPLE;
  archLabel(triple); // validates
  const version = options.version ?? JSON.parse(readFileSync(tauriConfigPath, "utf8")).version;
  const exe = options.exe ?? discoverExe({ triple, name: packageName(path.join(tauriDir, "Cargo.toml")) });
  const sidecar = options.sidecar ?? discoverSidecar({ triple });
  const webview2 = options.webview2 ? resolveWebview2(options.webview2) : undefined;
  const outDir = path.resolve(options.outDir ?? path.join(desktopRoot, "dist"));
  mkdirSync(outDir, { recursive: true });
  const file = path.join(outDir, zipName({ version, triple, offline: Boolean(webview2) }));
  writeZip(file, collectEntries({ exe, sidecar, webview2, version }), entryDate(env));
  return file;
}

if (isMain(import.meta.url)) {
  try {
    console.log(packagePortable(parseArgs(process.argv.slice(2))));
  } catch (e) {
    console.error(`package-portable: ${e.message}`);
    process.exit(1);
  }
}
