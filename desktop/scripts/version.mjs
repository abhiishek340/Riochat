#!/usr/bin/env node
// Keeps every version carried by the desktop project equal to the repository
// VERSION file.
//
//   node scripts/version.mjs --sync     rewrite out-of-date files
//   node scripts/version.mjs --check    exit 1 and list files that differ
//
// Files: desktop/package.json, src-tauri/tauri.conf.json, src-tauri/Cargo.toml
// ([package] table) and the riochat-desktop entry of src-tauri/Cargo.lock.

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { desktopRoot, isMain, repoRoot } from "./lib/util.mjs";
import { parse } from "./lib/semver.mjs";

const PACKAGE_NAME = "riochat-desktop";

export function readVersion(root) {
  const file = path.join(root, "VERSION");
  if (!existsSync(file)) throw new Error(`VERSION file not found at ${file}`);
  const v = readFileSync(file, "utf8").trim();
  if (!parse(v)) throw new Error(`invalid version "${v}" in ${file}`);
  return v;
}

function jsonVersion(text, version) {
  const doc = JSON.parse(text);
  if (typeof doc.version !== "string") throw new Error('no top-level "version" string');
  const before = doc.version;
  doc.version = version;
  return { text: `${JSON.stringify(doc, null, 2)}\n`, before };
}

function cargoPackageVersion(text, version) {
  const lines = text.split("\n");
  let inPackage = false;
  for (let i = 0; i < lines.length; i++) {
    if (/^\s*\[/.test(lines[i])) {
      inPackage = /^\s*\[package\]\s*(#.*)?$/.test(lines[i]);
      continue;
    }
    const m = inPackage && /^(\s*version\s*=\s*)"([^"]*)"(.*)$/.exec(lines[i]);
    if (m) {
      lines[i] = `${m[1]}"${version}"${m[3]}`;
      return { text: lines.join("\n"), before: m[2] };
    }
  }
  throw new Error("no version entry inside the [package] table");
}

function cargoLockVersion(text, version) {
  const re = new RegExp(`(\\[\\[package\\]\\]\\r?\\nname = "${PACKAGE_NAME}"\\r?\\nversion = ")([^"]*)(")`);
  const m = re.exec(text);
  if (!m) throw new Error(`no ${PACKAGE_NAME} entry`);
  return { text: text.replace(re, `$1${version}$3`), before: m[2] };
}

/** One entry per versioned file; `optional` files are skipped when absent. */
export function targets(desktop = desktopRoot) {
  return [
    { file: path.join(desktop, "package.json"), rewrite: jsonVersion },
    { file: path.join(desktop, "src-tauri", "tauri.conf.json"), rewrite: jsonVersion },
    { file: path.join(desktop, "src-tauri", "Cargo.toml"), rewrite: cargoPackageVersion },
    { file: path.join(desktop, "src-tauri", "Cargo.lock"), rewrite: cargoLockVersion, optional: true },
  ];
}

/**
 * Compares (and with `write` rewrites) every target. Returns the list of files
 * that differed, each with the version it carried.
 */
export function reconcile({ root = repoRoot, desktop = desktopRoot, write = false } = {}) {
  const version = readVersion(root);
  const drifted = [];
  for (const t of targets(desktop)) {
    if (!existsSync(t.file)) {
      if (t.optional) continue;
      throw new Error(`${t.file}: file not found`);
    }
    const raw = readFileSync(t.file, "utf8");
    const crlf = raw.includes("\r\n");
    let result;
    try {
      result = t.rewrite(raw.replace(/\r\n/g, "\n"), version);
    } catch (e) {
      throw new Error(`${t.file}: ${e.message}`);
    }
    const next = crlf ? result.text.replace(/\n/g, "\r\n") : result.text;
    if (result.before !== version || next !== raw) {
      drifted.push({ file: t.file, found: result.before });
      if (write && next !== raw) writeFileSync(t.file, next);
    }
  }
  return { version, drifted };
}

export function main(argv, { root = repoRoot, desktop = desktopRoot } = {}) {
  const check = argv.includes("--check");
  const sync = argv.includes("--sync");
  if (check === sync) {
    console.error("usage: version.mjs --sync | --check");
    return 2;
  }
  const { version, drifted } = reconcile({ root, desktop, write: sync });
  if (check) {
    if (drifted.length === 0) return 0;
    console.error(`${drifted.length} file(s) differ from VERSION ${version}:`);
    for (const d of drifted) console.error(`  ${path.relative(desktop, d.file)} has ${d.found}`);
    console.error("Fix with: pnpm --filter @riochat/desktop version:sync");
    return 1;
  }
  if (drifted.length > 0) {
    console.log(`updated ${drifted.length} file(s) to ${version}`);
    for (const d of drifted) console.log(`  ${path.relative(desktop, d.file)}`);
  }
  return 0;
}

if (isMain(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(`version: ${e.message}`);
    process.exit(2);
  }
}
