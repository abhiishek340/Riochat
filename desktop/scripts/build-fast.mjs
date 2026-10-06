#!/usr/bin/env node
// Fast local build: the `app` bundle only, with relaxed optimization (thin LTO,
// 16 codegen units, opt-level 2). Builds the sidecar first and exits with the
// build's status.

import { isMain } from "./lib/util.mjs";
import { runTauri } from "./lib/tauri-cli.mjs";
import { main as buildSidecar } from "./build-sidecar.mjs";

export function fastEnv(base = process.env) {
  return {
    ...base,
    CARGO_PROFILE_RELEASE_LTO: "thin",
    CARGO_PROFILE_RELEASE_CODEGEN_UNITS: "16",
    CARGO_PROFILE_RELEASE_OPT_LEVEL: "2",
  };
}

/**
 * Updater artifacts need the updater private key. A local fast build without
 * one switches them off instead of failing at the very end of the build.
 */
export function updaterOverride(env = process.env) {
  if (env.TAURI_SIGNING_PRIVATE_KEY || env.TAURI_SIGNING_PRIVATE_KEY_PATH) return [];
  return ["--config", JSON.stringify({ bundle: { createUpdaterArtifacts: false } })];
}

export function tauriArgs(extra = [], env = process.env) {
  return ["build", "--bundles", "app", ...updaterOverride(env), ...extra];
}

export function main(argv) {
  const status = buildSidecar([]);
  if (status !== 0) return status;
  return runTauri(tauriArgs(argv), { env: fastEnv() });
}

if (isMain(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(`build-fast: ${e.message}`);
    process.exit(1);
  }
}
