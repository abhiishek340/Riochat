#!/usr/bin/env node
// The "before dev" helper: makes sure the web dev server answers on port 3000.
//
// An already running server is reused (checked with a 2 second timeout); the
// helper then stays alive until interrupted and exits 0. Otherwise it starts
// the web dev server and exits with that server's status.

import { spawn } from "node:child_process";
import path from "node:path";
import { isMain, repoRoot } from "./lib/util.mjs";

export const DEV_URL = "http://localhost:3000";
export const CHECK_TIMEOUT_MS = 2000;

/** True when something answers HTTP at `url` (any status counts). */
export async function isReachable(url = DEV_URL, timeoutMs = CHECK_TIMEOUT_MS, fetchImpl = globalThis.fetch) {
  try {
    await fetchImpl(url, { signal: AbortSignal.timeout(timeoutMs), redirect: "manual" });
    return true;
  } catch {
    return false;
  }
}

function idleUntilInterrupted() {
  return new Promise((resolve) => {
    const done = () => resolve(0);
    process.once("SIGINT", done);
    process.once("SIGTERM", done);
    setInterval(() => {}, 1 << 30); // keep the event loop alive
  });
}

function startWebServer() {
  const pnpm = process.platform === "win32" ? "pnpm.cmd" : "pnpm";
  const child = spawn(pnpm, ["--filter", "@riochat/web", "dev"], {
    cwd: repoRoot,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  for (const sig of ["SIGINT", "SIGTERM"]) process.once(sig, () => child.kill(sig));
  return new Promise((resolve) => {
    child.on("error", (e) => {
      console.error(`cannot start the web dev server: ${e.message}`);
      resolve(1);
    });
    child.on("exit", (code) => resolve(code ?? 0));
  });
}

export async function main() {
  if (await isReachable()) {
    console.log(`reusing the web dev server already running at ${DEV_URL}`);
    return idleUntilInterrupted();
  }
  console.log(`starting the web dev server (${path.join("web")})`);
  return startWebServer();
}

if (isMain(import.meta.url)) {
  process.exit(await main());
}
