// Locates the Tauri CLI: the pinned npm package first, then `cargo tauri`.

import { existsSync } from "node:fs";
import path from "node:path";
import { desktopRoot, repoRoot, run } from "./util.mjs";

export function findLocalCli() {
  const bin = process.platform === "win32" ? "tauri.cmd" : "tauri";
  for (const base of [desktopRoot, repoRoot]) {
    const candidate = path.join(base, "node_modules", ".bin", bin);
    if (existsSync(candidate)) return candidate;
  }
  return null;
}

/** Runs the Tauri CLI with `args`; returns its exit status. */
export function runTauri(args, options = {}) {
  const local = findLocalCli();
  if (local) {
    return run(local, args, { cwd: desktopRoot, shell: process.platform === "win32", ...options });
  }
  console.warn("the pinned Tauri CLI is not installed (run pnpm install); trying `cargo tauri`");
  return run("cargo", ["tauri", ...args], { cwd: desktopRoot, ...options });
}
