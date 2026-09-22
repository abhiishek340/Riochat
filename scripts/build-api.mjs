#!/usr/bin/env node
// Builds the Go server with release metadata embedded.
//
//   node scripts/build-api.mjs [--out <file>] [--sidecar] [--target <rust triple>]
//
// Default output: <repo>/.cache/riochat/riochat (riochat.exe on Windows).
// --sidecar writes the binary where the desktop shell expects its bundled
// server: desktop/src-tauri/binaries/riochat-server-<target triple>[.exe].
// --target cross-selects the platform by Rust target triple (cgo is required
// by the SQLite driver, so a matching C toolchain must be available).
//
// Environment: GIT_COMMIT (default: `git rev-parse`, else "unknown") and
// BUILD_TIME (default: now, UTC, YYYY-MM-DDTHH:MM:SSZ).
// RIOCHAT_REFRESH_CATALOG_SNAPSHOTS=1 refreshes the bundled model catalog from
// its upstream source first; a failed refresh keeps the committed data and
// never fails the build.

import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { readVersion } from "./sync-version.mjs";

const PKG = "github.com/riochat/riochat/backend/internal/platform/buildinfo";

const TRIPLES = {
  "aarch64-apple-darwin": { goos: "darwin", goarch: "arm64" },
  "x86_64-apple-darwin": { goos: "darwin", goarch: "amd64" },
  "x86_64-unknown-linux-gnu": { goos: "linux", goarch: "amd64" },
  "aarch64-unknown-linux-gnu": { goos: "linux", goarch: "arm64" },
  "x86_64-pc-windows-msvc": { goos: "windows", goarch: "amd64" },
  "aarch64-pc-windows-msvc": { goos: "windows", goarch: "arm64" },
};

export function utcStamp(date = new Date()) {
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function ldflags({ version, commit, buildTime }) {
  return `-s -w -X ${PKG}.Version=${version} -X ${PKG}.Commit=${commit} -X ${PKG}.BuildTime=${buildTime}`;
}

export function platformForTriple(triple) {
  const p = TRIPLES[triple];
  if (!p) throw new Error(`unsupported target triple "${triple}" (known: ${Object.keys(TRIPLES).join(", ")})`);
  return p;
}

export function parseArgs(argv) {
  const opts = { sidecar: false, out: "", target: "" };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--sidecar") opts.sidecar = true;
    else if (a === "--out") opts.out = argv[++i] ?? "";
    else if (a === "--target") opts.target = argv[++i] ?? "";
    else throw new Error(`unknown argument ${a}`);
  }
  if (opts.sidecar && !opts.target) opts.target = hostTriple();
  return opts;
}

function hostTriple() {
  const r = spawnSync("rustc", ["-vV"], { encoding: "utf8" });
  const line = r.status === 0 ? r.stdout.split("\n").find((l) => l.startsWith("host:")) : undefined;
  if (!line) throw new Error("--sidecar needs --target <triple> or rustc on PATH to learn the host triple");
  return line.slice(5).trim();
}

export function outputPath(root, { sidecar, out, target }) {
  if (out) return path.resolve(out);
  const windows = target ? platformForTriple(target).goos === "windows" : process.platform === "win32";
  const exe = windows ? ".exe" : "";
  if (sidecar) return path.join(root, "desktop", "src-tauri", "binaries", `riochat-server-${target}${exe}`);
  return path.join(root, ".cache", "riochat", `riochat${exe}`);
}

function gitCommit(root) {
  if (process.env.GIT_COMMIT) return process.env.GIT_COMMIT;
  const r = spawnSync("git", ["rev-parse", "--short=12", "HEAD"], { cwd: root, encoding: "utf8" });
  return r.status === 0 && r.stdout.trim() ? r.stdout.trim() : "unknown";
}

function main(argv) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const opts = parseArgs(argv);
  const version = readVersion(root);
  const commit = gitCommit(root);
  const buildTime = process.env.BUILD_TIME || utcStamp();
  const output = outputPath(root, opts);
  mkdirSync(path.dirname(output), { recursive: true });

  const env = { ...process.env, CGO_ENABLED: "1" };
  if (opts.target) {
    const p = platformForTriple(opts.target);
    env.GOOS = p.goos;
    env.GOARCH = p.goarch;
  }
  if (process.env.RIOCHAT_REFRESH_CATALOG_SNAPSHOTS === "1") {
    // Run for the build machine, never cross-compiled.
    const refresh = spawnSync("go", ["run", "./cmd/catalog-snapshot"], { cwd: path.join(root, "backend"), stdio: "inherit" });
    if (refresh.status !== 0) console.warn("build-api: catalog refresh failed; keeping the committed snapshot");
  }
  const args = ["build", "-trimpath", "-ldflags", ldflags({ version, commit, buildTime }), "-o", output, "./cmd/riochat"];
  console.log(`building riochat ${version} (${commit}) -> ${path.relative(root, output)}`);
  const res = spawnSync("go", args, { cwd: path.join(root, "backend"), env, stdio: "inherit" });
  if (res.error) {
    console.error(`build-api: cannot run go: ${res.error.message}`);
    return 1;
  }
  return res.status ?? 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(`build-api: ${e.message}`);
    process.exit(2);
  }
}
