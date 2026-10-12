#!/usr/bin/env node
// Signed local build.
//
//   node scripts/build-signed.mjs [tauri build arguments]
//
// Needs a local, untracked signing environment file (default
// deploy/secrets/desktop-signing.env at the repository root; override with
// DESKTOP_SIGNING_ENV). The file holds KEY=VALUE lines, see the example next to it.

import { existsSync, readFileSync, readdirSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { desktopRoot, isMain, repoRoot, run, tauriDir } from "./lib/util.mjs";
import { runTauri } from "./lib/tauri-cli.mjs";
import { main as buildSidecar } from "./build-sidecar.mjs";

export const DEFAULT_ENV_FILE = path.join(repoRoot, "deploy", "secrets", "desktop-signing.env");

/** Parses KEY=VALUE lines (optional `export`, quotes, `#` comments). */
export function parseEnvFile(text) {
  const vars = {};
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const m = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
    if (!m) continue;
    let value = m[2].trim();
    const quote = value[0];
    if ((quote === '"' || quote === "'") && value.endsWith(quote) && value.length >= 2) {
      value = value.slice(1, -1);
    } else {
      value = value.replace(/\s+#.*$/, "");
    }
    vars[m[1]] = value;
  }
  return vars;
}

/**
 * Builds the child environment from the file: blank variables are unset, the
 * updater key password always exists, and key/certificate content is derived
 * from the *_PATH variables when not given directly.
 */
export function prepareEnv(fileVars, { base = process.env, resolve = (p) => path.resolve(repoRoot, p), read = readFileSync } = {}) {
  const env = { ...base };
  for (const [k, v] of Object.entries(fileVars)) {
    if (v === "") delete env[k];
    else env[k] = v;
  }
  if (env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD === undefined) env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD = "";
  const derive = (target, pathVar, encode) => {
    if (env[target] || !env[pathVar]) return;
    const content = read(resolve(env[pathVar]));
    env[target] = encode(content);
  };
  derive("TAURI_SIGNING_PRIVATE_KEY", "TAURI_SIGNING_PRIVATE_KEY_PATH", (c) => c.toString("utf8"));
  derive("APPLE_CERTIFICATE", "APPLE_CERTIFICATE_PATH", (c) => c.toString("base64"));
  derive("WINDOWS_CERTIFICATE", "WINDOWS_CERTIFICATE_PATH", (c) => c.toString("base64"));
  return env;
}

/** Ad-hoc identity `-` when there is no Apple certificate on macOS. */
export function applyAppleIdentity(env, platform, helper, warn = console.warn) {
  if (env.APPLE_CERTIFICATE) {
    const derived = helper(env);
    for (const line of derived.split("\n")) {
      const m = /^(APPLE_SIGNING_IDENTITY|APPLE_TEAM_ID)=(.*)$/.exec(line.trim());
      if (m) env[m[1]] = m[2];
    }
    if (!env.APPLE_SIGNING_IDENTITY) throw new Error("the Apple certificate did not yield a signing identity");
  } else if (platform === "darwin") {
    warn("no Apple certificate configured: using an ad-hoc signature (macOS reports unsigned apps as damaged on Apple silicon otherwise)");
    env.APPLE_SIGNING_IDENTITY = "-";
  }
  return env;
}

export function bundleDir() {
  return path.join(tauriDir, "target", "release", "bundle");
}

/** Exactly one file with `suffix` in `dir`; throws otherwise. */
export function exactlyOne(dir, suffix) {
  const found = existsSync(dir) ? readdirSync(dir).filter((n) => n.endsWith(suffix)) : [];
  if (found.length !== 1) {
    throw new Error(`expected exactly one ${suffix} in ${dir}, found ${found.length}`);
  }
  return path.join(dir, found[0]);
}

export function machineArch(arch = os.arch()) {
  return arch === "arm64" ? "arm64" : "x86_64";
}

function appleHelper(env) {
  const r = spawnSync(process.execPath, [path.join(desktopRoot, "scripts", "apple-identity.mjs")], {
    env,
    encoding: "utf8",
  });
  if (r.status !== 0) throw new Error(`apple-identity failed: ${r.stderr.trim()}`);
  return r.stdout;
}

export async function main(argv) {
  const file = process.env.DESKTOP_SIGNING_ENV
    ? path.resolve(repoRoot, process.env.DESKTOP_SIGNING_ENV)
    : DEFAULT_ENV_FILE;
  if (!existsSync(file)) {
    console.error(
      `build-signed: signing environment file not found: ${file}\n` +
        "Create it from the example in deploy/secrets (it is untracked), or use `bundle` for an unsigned build.",
    );
    return 1;
  }
  const env = prepareEnv(parseEnvFile(readFileSync(file, "utf8")));
  applyAppleIdentity(env, process.platform, appleHelper);

  const mac = process.platform === "darwin";
  const bundle = bundleDir();
  if (mac) {
    for (const sub of ["dmg", "macos"]) rmSync(path.join(bundle, sub), { recursive: true, force: true });
  }
  let status = buildSidecar([]);
  if (status !== 0) return status;
  status = runTauri(["build", ...argv], { env });
  if (status !== 0) return status;
  if (!mac) return 0;

  const dmg = exactlyOne(path.join(bundle, "dmg"), ".dmg");
  const updater = exactlyOne(path.join(bundle, "macos"), ".app.tar.gz");
  const { main: postDmg } = await import("./post-dmg.mjs");
  status = await postDmg([dmg], env);
  if (status !== 0) return status;
  return run(process.execPath, [path.join(desktopRoot, "scripts", "verify-macos-bundle.mjs"), "--arch", machineArch(), dmg, updater], { env });
}

if (isMain(import.meta.url)) {
  try {
    process.exit(await main(process.argv.slice(2)));
  } catch (e) {
    console.error(`build-signed: ${e.message}`);
    process.exit(1);
  }
}
