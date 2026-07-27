// Shared helpers for the desktop tooling scripts.

import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

/** The desktop project folder (contains package.json and src-tauri). */
export const desktopRoot = path.resolve(here, "..", "..");
/** The repository root (contains VERSION, backend, web, desktop). */
export const repoRoot = path.resolve(desktopRoot, "..");
export const tauriDir = path.join(desktopRoot, "src-tauri");
export const tauriConfigPath = path.join(tauriDir, "tauri.conf.json");

/** True when the module is the process entry point. */
export function isMain(metaUrl) {
  return Boolean(process.argv[1]) && pathToFileURL(path.resolve(process.argv[1])).href === metaUrl;
}

/** Runs a command with inherited stdio; returns its exit status (1 on spawn failure). */
export function run(cmd, args, options = {}) {
  const res = spawnSync(cmd, args, { stdio: "inherit", shell: false, ...options });
  if (res.error) {
    console.error(`cannot run ${cmd}: ${res.error.message}`);
    return 1;
  }
  return res.status ?? 1;
}

/** Runs a command and captures stdout; returns null on any failure. */
export function capture(cmd, args, options = {}) {
  const res = spawnSync(cmd, args, { encoding: "utf8", ...options });
  if (res.error || res.status !== 0) return null;
  return res.stdout;
}

/** Name of the pnpm-installed Tauri CLI entry, run through the package manager. */
export function tauriCommand() {
  return process.platform === "win32" ? "tauri.cmd" : "tauri";
}

/** Splits `--flag value` style arguments, rejecting unknown flags and missing values. */
export function parseFlags(argv, spec) {
  const out = {};
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith("--")) {
      positional.push(a);
      continue;
    }
    const kind = spec[a.slice(2)];
    if (kind === undefined) throw new Error(`unknown argument ${a}`);
    const key = a.slice(2);
    if (kind === "boolean") {
      out[key] = true;
    } else {
      const value = argv[i + 1];
      if (value === undefined || value.startsWith("--")) throw new Error(`${a} needs a value`);
      out[key] = value;
      i++;
    }
  }
  return { flags: out, positional };
}
