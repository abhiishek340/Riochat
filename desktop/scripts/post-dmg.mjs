#!/usr/bin/env node
// DMG post-processing for macOS builds.
//
//   node scripts/post-dmg.mjs <file.dmg>
//
// 1. Re-encodes the DMG to lzfse (ULFO).
// 2. Signs and notarizes, in one of two ways:
//    - when all four signing-service variables are set (SIGN_AND_NOTARIZE_SCRIPT_URL,
//      SIGN_AND_NOTARIZE_BASE_URL, SIGN_AND_NOTARIZE_API_TOKEN, SIGN_AND_NOTARIZE_HELPER_URL),
//      the service takes precedence: the app inside a writable copy is signed and
//      notarized, the DMG is repackaged, then the DMG itself is signed and
//      notarized; the original is replaced only after everything succeeded;
//    - otherwise, with APPLE_TEAM_ID (and a real identity) the DMG is signed with a
//      timestamp, submitted for notarization with APPLE_ID / APPLE_PASSWORD / team id
//      and waited on, then stapled. Ad-hoc identities skip notarization.

import { execFileSync } from "node:child_process";
import { copyFileSync, mkdtempSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { isMain } from "./lib/util.mjs";

const SERVICE_VARS = [
  "SIGN_AND_NOTARIZE_SCRIPT_URL",
  "SIGN_AND_NOTARIZE_BASE_URL",
  "SIGN_AND_NOTARIZE_API_TOKEN",
  "SIGN_AND_NOTARIZE_HELPER_URL",
];

export function serviceConfig(env = process.env) {
  if (!SERVICE_VARS.every((k) => env[k])) return null;
  return {
    scriptUrl: env.SIGN_AND_NOTARIZE_SCRIPT_URL,
    baseUrl: env.SIGN_AND_NOTARIZE_BASE_URL,
    apiToken: env.SIGN_AND_NOTARIZE_API_TOKEN,
    helperUrl: env.SIGN_AND_NOTARIZE_HELPER_URL,
  };
}

const MAC_ARCH = { aarch64: "arm64", arm64: "arm64", x64: "x64", x86_64: "x64" };

/**
 * Derives the release name from a DMG file name. Accepts the bundler's
 * `<product>_<version>_<arch>.dmg` and the final `Riochat-<version>-macos-<arch>.dmg`.
 */
export function releaseName(fileName) {
  const base = path.basename(fileName);
  let m = /^([A-Za-z0-9.-]+)_(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)_([A-Za-z0-9_]+)\.dmg$/.exec(base);
  if (m && MAC_ARCH[m[3]]) return `${m[1]}-${m[2]}-macos-${MAC_ARCH[m[3]]}`;
  m = /^([A-Za-z0-9.]+)-(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)-macos-(arm64|x64)\.dmg$/.exec(base);
  if (m) return `${m[1]}-${m[2]}-macos-${m[3]}`;
  throw new Error(`cannot derive a release name from "${base}"`);
}

export function isAdHoc(identity) {
  return !identity || identity === "-";
}

/** Which signing path applies. */
export function plan(env = process.env) {
  if (serviceConfig(env)) return "service";
  if (env.APPLE_TEAM_ID && !isAdHoc(env.APPLE_SIGNING_IDENTITY)) return "apple";
  return "none";
}

const sh = (cmd, args, options = {}) => execFileSync(cmd, args, { stdio: "inherit", ...options });
const out = (cmd, args, options = {}) => execFileSync(cmd, args, { encoding: "utf8", ...options });

function reencode(dmg, tmp) {
  const target = path.join(tmp, "lzfse.dmg");
  sh("hdiutil", ["convert", dmg, "-format", "ULFO", "-o", target]);
  return target;
}

function detach(mount) {
  try {
    sh("hdiutil", ["detach", mount, "-force"], { stdio: "ignore" });
  } catch {
    /* already gone */
  }
}

async function viaService(dmg, tmp, cfg) {
  const name = releaseName(dmg);
  const res = await fetch(cfg.scriptUrl);
  if (!res.ok) throw new Error(`cannot download the signing script (HTTP ${res.status})`);
  const script = await res.text();
  if (script.trim() === "") throw new Error("the signing script is empty");
  const scriptPath = path.join(tmp, "sign-service.sh");
  writeFileSync(scriptPath, script, { mode: 0o700 });
  const env = {
    ...process.env,
    SIGN_AND_NOTARIZE_BASE_URL: cfg.baseUrl,
    SIGN_AND_NOTARIZE_API_TOKEN: cfg.apiToken,
    SIGN_AND_NOTARIZE_HELPER_URL: cfg.helperUrl,
  };

  // writable copy: UDRW image, replace the app inside, convert back
  const rw = path.join(tmp, "rw.dmg");
  sh("hdiutil", ["convert", dmg, "-format", "UDRW", "-o", rw]);
  const mount = path.join(tmp, "mnt");
  sh("hdiutil", ["attach", rw, "-nobrowse", "-noverify", "-mountpoint", mount]);
  try {
    const apps = readdirSync(mount).filter((n) => n.endsWith(".app"));
    if (apps.length !== 1) throw new Error(`expected exactly one app in the DMG, found ${apps.length}`);
    sh("bash", [scriptPath, "app", path.join(mount, apps[0]), "--release", name], { env });
  } finally {
    detach(mount);
  }
  const repackaged = path.join(tmp, "repackaged.dmg");
  sh("hdiutil", ["convert", rw, "-format", "ULFO", "-o", repackaged]);
  sh("bash", [scriptPath, "dmg", repackaged, "--release", name], { env });
  return repackaged;
}

function viaApple(dmgPath, env) {
  sh("codesign", ["--force", "--sign", env.APPLE_SIGNING_IDENTITY, "--timestamp", dmgPath]);
  for (const k of ["APPLE_ID", "APPLE_PASSWORD"]) {
    if (!env[k]) throw new Error(`${k} is required to notarize the DMG`);
  }
  sh("xcrun", [
    "notarytool",
    "submit",
    dmgPath,
    "--apple-id",
    env.APPLE_ID,
    "--password",
    env.APPLE_PASSWORD,
    "--team-id",
    env.APPLE_TEAM_ID,
    "--wait",
  ]);
  sh("xcrun", ["stapler", "staple", dmgPath]);
}

export async function main(argv, env = process.env) {
  if (argv.length !== 1 || argv[0].startsWith("--")) {
    console.error("usage: post-dmg.mjs <file.dmg>");
    return 2;
  }
  const dmg = path.resolve(argv[0]);
  const tmp = mkdtempSync(path.join(os.tmpdir(), "riochat-dmg-"));
  try {
    let result;
    switch (plan(env)) {
      case "service":
        result = await viaService(dmg, tmp, serviceConfig(env));
        break;
      case "apple":
        result = reencode(dmg, tmp);
        viaApple(result, env);
        break;
      default:
        result = reencode(dmg, tmp);
        if (!isAdHoc(env.APPLE_SIGNING_IDENTITY)) {
          sh("codesign", ["--force", "--sign", env.APPLE_SIGNING_IDENTITY, "--timestamp", result]);
        } else {
          console.warn("ad-hoc build: the DMG is not signed or notarized");
        }
    }
    // replace the original only after everything succeeded
    const staged = `${dmg}.partial`;
    copyFileSync(result, staged);
    renameSync(staged, dmg);
    console.log(`post-processed ${path.basename(dmg)}`);
    return 0;
  } catch (e) {
    console.error(`post-dmg: ${e.message}`);
    return 1;
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}

if (isMain(import.meta.url)) process.exit(await main(process.argv.slice(2)));
