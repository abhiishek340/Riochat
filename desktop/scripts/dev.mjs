#!/usr/bin/env node
// Development run: builds the sidecar, then starts the app with the dev
// configuration (separate identifier, keychain service and local database).
// The web dev server is started or reused by dev-server.mjs.

import path from "node:path";
import { isMain, tauriDir } from "./lib/util.mjs";
import { runTauri } from "./lib/tauri-cli.mjs";
import { main as buildSidecar } from "./build-sidecar.mjs";

export function devArgs(extra = []) {
  return ["dev", "--config", path.join(tauriDir, "tauri.dev.conf.json"), ...extra];
}

export function main(argv) {
  const status = buildSidecar([]);
  if (status !== 0) return status;
  return runTauri(devArgs(argv));
}

if (isMain(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(`dev: ${e.message}`);
    process.exit(1);
  }
}
