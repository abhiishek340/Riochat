#!/usr/bin/env node
// Verifies macOS artifacts.
//
//   node scripts/verify-macos-bundle.mjs --arch <arm64|x86_64> <file.dmg | file.app.tar.gz>...
//
// Exit 2 on bad usage, 1 on a failed check. For every artifact (exactly one
// app inside): the app binary and the riochat-server binary must exist, be
// Mach-O for exactly the given architecture and declare a minimum macOS not
// newer than the bundle's own; `codesign --verify --deep --strict` must pass.
// With APPLE_TEAM_ID set the app and the DMG must be signed by that team,
// carry stapled tickets and pass Gatekeeper as "Notarized Developer ID";
// without it the artifact is reported as ad-hoc signed.

import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { isMain } from "./lib/util.mjs";

const CPU = { 0x0100000c: "arm64", 0x01000007: "x86_64" };

/** Dotted numeric comparison: -1, 0 or 1. */
export function compareVersions(a, b) {
  const x = String(a).split(".").map(Number);
  const y = String(b).split(".").map(Number);
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const d = (x[i] || 0) - (y[i] || 0);
    if (d !== 0) return Math.sign(d);
  }
  return 0;
}

const unpack = (v) => `${v >>> 16}.${(v >>> 8) & 0xff}.${v & 0xff}`;

/**
 * Reads a Mach-O image: `{ kind: "thin", arch, minos }`, `{ kind: "fat", archs }`
 * or `{ kind: "unknown" }`. `minos` is the macOS deployment target.
 */
export function inspectMachO(buf) {
  if (buf.length < 8) return { kind: "unknown" };
  const be = buf.readUInt32BE(0);
  if (be === 0xcafebabe || be === 0xcafebabf) {
    const n = buf.readUInt32BE(4);
    const wide = be === 0xcafebabf;
    const stride = wide ? 32 : 20;
    const archs = [];
    for (let i = 0; i < n; i++) {
      const cpu = buf.readInt32BE(8 + i * stride) >>> 0;
      archs.push(CPU[cpu] ?? `cpu-${cpu.toString(16)}`);
    }
    return { kind: "fat", archs };
  }
  if (buf.readUInt32LE(0) !== 0xfeedfacf) return { kind: "unknown" };
  const arch = CPU[buf.readUInt32LE(4)] ?? "other";
  const ncmds = buf.readUInt32LE(16);
  let off = 32;
  let minos = null;
  for (let i = 0; i < ncmds && off + 8 <= buf.length; i++) {
    const cmd = buf.readUInt32LE(off);
    const size = buf.readUInt32LE(off + 4);
    if (size < 8) break;
    if (cmd === 0x32 && buf.readUInt32LE(off + 8) === 1) minos = unpack(buf.readUInt32LE(off + 12)); // LC_BUILD_VERSION, macOS
    if (cmd === 0x24) minos = unpack(buf.readUInt32LE(off + 8)); // LC_VERSION_MIN_MACOSX
    off += size;
  }
  return { kind: "thin", arch, minos };
}

export class Failure extends Error {}

export function checkBinary(label, buf, arch, bundleMin) {
  const info = inspectMachO(buf);
  if (info.kind === "fat") throw new Failure(`${label} is a universal binary (${info.archs.join(", ")}); expected only ${arch}`);
  if (info.kind !== "thin") throw new Failure(`${label} is not a Mach-O executable`);
  if (info.arch !== arch) throw new Failure(`${label} is built for ${info.arch}, expected ${arch}`);
  if (info.minos && bundleMin && compareVersions(info.minos, bundleMin) > 0) {
    throw new Failure(`${label} requires macOS ${info.minos}, newer than the bundle's minimum ${bundleMin}`);
  }
  return info;
}

export function parseArgs(argv) {
  if (argv[0] !== "--arch" || !["arm64", "x86_64"].includes(argv[1] ?? "")) return null;
  const files = argv.slice(2);
  if (files.length === 0 || files.some((f) => f.startsWith("--"))) return null;
  return { arch: argv[1], files };
}

const run = (cmd, args) => spawnSync(cmd, args, { encoding: "utf8" });

function plist(file, key) {
  const r = run("plutil", ["-extract", key, "raw", "-o", "-", file]);
  return r.status === 0 ? r.stdout.trim() : "";
}

function findApp(dir) {
  const apps = readdirSync(dir).filter((n) => n.endsWith(".app"));
  if (apps.length !== 1) throw new Failure(`expected exactly one app, found ${apps.length} in ${dir}`);
  return path.join(dir, apps[0]);
}

function checkSignature(target, label, teamId, warn) {
  const verify = run("codesign", ["--verify", "--deep", "--strict", target]);
  if (verify.status !== 0) throw new Failure(`${label}: codesign verification failed: ${verify.stderr.trim()}`);
  if (!teamId) return;
  const details = run("codesign", ["-dv", "--verbose=4", target]);
  if (!`${details.stdout}${details.stderr}`.includes(`TeamIdentifier=${teamId}`)) {
    throw new Failure(`${label} is not signed by team ${teamId}`);
  }
  const stapled = run("xcrun", ["stapler", "validate", target]);
  if (stapled.status !== 0) throw new Failure(`${label} has no stapled notarization ticket`);
  const assess = label.endsWith(".dmg")
    ? run("spctl", ["--assess", "--type", "open", "--context", "context:primary-signature", "-vv", target])
    : run("spctl", ["--assess", "--type", "execute", "-vv", target]);
  const text = `${assess.stdout}${assess.stderr}`;
  if (/assessments are disabled/i.test(text)) warn(`${label}: Gatekeeper assessments are disabled on this host`);
  else if (!text.includes("Notarized Developer ID")) throw new Failure(`${label} is not accepted as Notarized Developer ID`);
}

function checkApp(app, arch, label, teamId, warn) {
  const info = path.join(app, "Contents", "Info.plist");
  const executable = plist(info, "CFBundleExecutable");
  if (!executable) throw new Failure(`${label}: cannot read CFBundleExecutable`);
  const bundleMin = plist(info, "LSMinimumSystemVersion");
  const main = path.join(app, "Contents", "MacOS", executable);
  const sidecar = path.join(app, "Contents", "MacOS", "riochat-server");
  for (const [name, file] of [
    [executable, main],
    ["riochat-server", sidecar],
  ]) {
    if (!existsSync(file)) throw new Failure(`${label}: ${name} is missing from the bundle`);
    checkBinary(`${label}: ${name}`, readFileSync(file), arch, bundleMin);
  }
  checkSignature(app, `${label} app`, teamId, warn);
}

export function verifyArtifact(file, arch, env = process.env, warn = console.warn) {
  const teamId = env.APPLE_TEAM_ID || "";
  const tmp = mkdtempSync(path.join(os.tmpdir(), "riochat-verify-"));
  let mount = null;
  try {
    let app;
    if (file.endsWith(".dmg")) {
      mount = path.join(tmp, "mnt");
      const attach = run("hdiutil", ["attach", file, "-readonly", "-nobrowse", "-mountpoint", mount]);
      if (attach.status !== 0) throw new Failure(`cannot mount ${file}: ${attach.stderr.trim()}`);
      app = findApp(mount);
      if (teamId) checkSignature(file, `${path.basename(file)}`, teamId, warn);
    } else if (file.endsWith(".app.tar.gz")) {
      execFileSync("tar", ["-xzf", file, "-C", tmp]);
      app = findApp(tmp);
    } else {
      throw new Failure(`unsupported artifact ${file} (expected .dmg or .app.tar.gz)`);
    }
    checkApp(app, arch, path.basename(file), teamId, warn);
    console.log(`${path.basename(file)}: ok (${arch}, ${teamId ? `team ${teamId}` : "ad-hoc signed"})`);
  } finally {
    if (mount) run("hdiutil", ["detach", mount, "-force"]);
    rmSync(tmp, { recursive: true, force: true });
  }
}

export function main(argv, env = process.env) {
  const args = parseArgs(argv);
  if (!args) {
    console.error("usage: verify-macos-bundle.mjs --arch <arm64|x86_64> <file.dmg | file.app.tar.gz>...");
    return 2;
  }
  try {
    for (const f of args.files) verifyArtifact(path.resolve(f), args.arch, env);
    return 0;
  } catch (e) {
    console.error(`verify-macos-bundle: ${e.message}`);
    return 1;
  }
}

if (isMain(import.meta.url)) process.exit(main(process.argv.slice(2)));
