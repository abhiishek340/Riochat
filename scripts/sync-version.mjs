#!/usr/bin/env node
// Propagates the repository VERSION to every versioned manifest.
//
//   node scripts/sync-version.mjs [mode] [--check] [--root <dir>]
//
// mode is one of: all (default), workspace, web, desktop, backend.
//
// Without --check, files that are out of date are rewritten. With --check
// nothing is written; the exit status is 1 and the drifting files are listed
// when any file differs, 0 (silently) when everything matches. Any other
// failure (bad VERSION, unknown mode, a file missing the text that has to be
// rewritten) aborts with status 2 and a message naming the cause.
//
// To version another application, add one entry to TARGETS below; nothing
// else in this file or elsewhere needs to change.

import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const VERSION_RE = /^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/;

// ---------------------------------------------------------------------------
// Rewriters. Each takes LF-normalised text and the version and returns the
// updated text, or throws when the text lacks the part that must be rewritten.
// ---------------------------------------------------------------------------

function jsonVersion(text, version, label) {
  let doc;
  try {
    doc = JSON.parse(text);
  } catch (e) {
    throw new Error(`${label}: not valid JSON (${e.message})`);
  }
  if (doc === null || typeof doc !== "object" || Array.isArray(doc) || typeof doc.version !== "string") {
    throw new Error(`${label}: no top-level "version" string to update`);
  }
  doc.version = version;
  return `${JSON.stringify(doc, null, 2)}\n`;
}

// Replaces the first `version = "..."` line inside the [package] table only,
// leaving dependency versions untouched.
function cargoPackageVersion(text, version, label) {
  const lines = text.split("\n");
  let inPackage = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*\[/.test(line)) {
      inPackage = /^\s*\[package\]\s*(#.*)?$/.test(line);
      continue;
    }
    if (inPackage) {
      const m = /^(\s*version\s*=\s*)"[^"]*"(.*)$/.exec(line);
      if (m) {
        lines[i] = `${m[1]}"${version}"${m[2]}`;
        return lines.join("\n");
      }
    }
  }
  throw new Error(`${label}: no version entry inside the [package] table`);
}

// Updates the `// @version x.y.z` annotation that documents the API version.
function annotationVersion(text, version, label) {
  const re = /^(\s*\/\/\s*@version\s+)\S+/m;
  if (!re.test(text)) throw new Error(`${label}: no "// @version" annotation found`);
  return text.replace(re, `$1${version}`);
}

// ---------------------------------------------------------------------------
// Targets, grouped by mode. Paths are relative to the repository root. A glob
// of the form "dir/*/file" expands to every immediate subdirectory holding it.
// "optional" files are skipped when absent.
// ---------------------------------------------------------------------------

export const TARGETS = {
  workspace: [
    { file: "package.json", rewrite: jsonVersion },
    { file: "packages/*/package.json", rewrite: jsonVersion, optional: true },
  ],
  web: [{ file: "web/package.json", rewrite: jsonVersion }],
  desktop: [
    { file: "desktop/package.json", rewrite: jsonVersion },
    // The shell configuration only exists once the desktop shell is configured.
    { file: "desktop/src-tauri/tauri.conf.json", rewrite: jsonVersion, optional: true },
    { file: "desktop/src-tauri/Cargo.toml", rewrite: cargoPackageVersion },
  ],
  backend: [
    { file: "backend/package.json", rewrite: jsonVersion },
    { file: "backend/cmd/riochat/main.go", rewrite: annotationVersion },
  ],
};

export const MODES = ["all", ...Object.keys(TARGETS)];

function expand(root, pattern) {
  if (!pattern.includes("*")) return [pattern];
  const [dir, rest] = pattern.split("/*/");
  const base = path.join(root, dir);
  if (!existsSync(base)) return [];
  return readdirSync(base, { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .map((d) => `${dir}/${d.name}/${rest}`)
    .sort();
}

export function readVersion(root) {
  const file = path.join(root, "VERSION");
  if (!existsSync(file)) throw new Error(`VERSION file not found at ${file}`);
  const value = readFileSync(file, "utf8").trim();
  if (!VERSION_RE.test(value)) {
    throw new Error(`invalid version "${value}" in VERSION (expected MAJOR.MINOR.PATCH with an optional -suffix or +suffix)`);
  }
  return value;
}

/**
 * Runs the tool. Returns the process exit status instead of exiting so the
 * behaviour can be tested.
 */
export function run({ root, mode = "all", check = false, out = console.log, err = console.error }) {
  try {
    if (!MODES.includes(mode)) {
      throw new Error(`unknown mode "${mode}"; valid modes: ${MODES.join(", ")}`);
    }
    const version = readVersion(root);
    const groups = mode === "all" ? Object.keys(TARGETS) : [mode];

    const drifted = [];
    for (const group of groups) {
      for (const target of TARGETS[group]) {
        for (const rel of expand(root, target.file)) {
          const abs = path.join(root, rel);
          if (!existsSync(abs)) {
            if (target.optional) continue;
            throw new Error(`${rel}: file not found`);
          }
          const raw = readFileSync(abs, "utf8");
          const crlf = raw.includes("\r\n");
          const current = raw.replace(/\r\n/g, "\n");
          const next = target.rewrite(current, version, rel);
          if (next === current) continue;
          drifted.push(rel);
          if (!check) writeFileSync(abs, crlf ? next.replace(/\n/g, "\r\n") : next);
        }
      }
    }

    if (check && drifted.length > 0) {
      err(`${drifted.length} file(s) are out of sync with VERSION ${version}:`);
      for (const rel of drifted) err(`  ${rel}`);
      err(mode === "all" ? "Regenerate them with: node scripts/sync-version.mjs (or: make version)" : `Fix with: node scripts/sync-version.mjs ${mode}`);
      return 1;
    }
    if (!check && drifted.length > 0) {
      out(`updated ${drifted.length} file(s) to ${version}`);
      for (const rel of drifted) out(`  ${rel}`);
    }
    return 0;
  } catch (e) {
    err(`sync-version: ${e.message}`);
    return 2;
  }
}

function main(argv) {
  let check = false;
  let root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  let mode;
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--check") check = true;
    else if (a === "--root") root = path.resolve(argv[++i] ?? "");
    else if (a.startsWith("--")) {
      console.error(`sync-version: unknown option ${a}`);
      return 2;
    } else if (mode === undefined) mode = a;
    else {
      console.error(`sync-version: unexpected argument ${a}`);
      return 2;
    }
  }
  return run({ root, mode, check });
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
