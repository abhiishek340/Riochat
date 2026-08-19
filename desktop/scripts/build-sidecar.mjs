#!/usr/bin/env node
// Builds the Riochat server for bundling as the desktop sidecar.
//
//   node scripts/build-sidecar.mjs [--target <rust-triple>]
//
// Output: src-tauri/binaries/riochat-server-<triple>[.exe]
//
// The default target is the host triple reported by `rustc -vV`. An unknown
// triple, or a missing Rust toolchain, is an error (exit 1).

import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import path from "node:path";
import { capture, isMain, parseFlags, repoRoot, run, tauriConfigPath, tauriDir } from "./lib/util.mjs";

export const BUILD_TAGS = "nopostgres,noredis,nos3,noswagger,nomsgpack,nootlp";
export const BUILD_INFO_PKG = "github.com/riochat/riochat/backend/internal/platform/buildinfo";
export const SQLITE_MODULE = "github.com/mattn/go-sqlite3";

const ARCHES = { aarch64: "arm64", x86_64: "amd64" };
const SYSTEMS = [
  ["apple-darwin", "darwin"],
  ["windows", "windows"],
  ["linux", "linux"],
];

/** Maps a Rust target triple to Go's GOOS/GOARCH. Throws for anything unsupported. */
export function goTarget(triple) {
  const arch = ARCHES[String(triple).split("-")[0]];
  const system = SYSTEMS.find(([marker]) => String(triple).includes(marker));
  if (!arch || !system) {
    throw new Error(`unsupported target triple "${triple}"`);
  }
  return { goos: system[1], goarch: arch, windows: system[1] === "windows", darwin: system[1] === "darwin" };
}

export function outputFileName(triple) {
  return `riochat-server-${triple}${goTarget(triple).windows ? ".exe" : ""}`;
}

export function ldflags({ version, commit, buildTime }) {
  return [
    "-s",
    "-w",
    `-X ${BUILD_INFO_PKG}.Version=${version}`,
    `-X ${BUILD_INFO_PKG}.Commit=${commit}`,
    `-X ${BUILD_INFO_PKG}.BuildTime=${buildTime}`,
  ].join(" ");
}

export function utcStamp(date = new Date()) {
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** Reads `bundle.macOS.minimumSystemVersion` from the app configuration. */
export function macMinimumVersion(configPath = tauriConfigPath) {
  const config = JSON.parse(readFileSync(configPath, "utf8"));
  const v = config?.bundle?.macOS?.minimumSystemVersion;
  if (typeof v !== "string" || v.trim() === "") {
    throw new Error(`bundle.macOS.minimumSystemVersion is missing from ${configPath}`);
  }
  return v.trim();
}

/**
 * Environment for `go build`. CGO flag variables replace the defaults, so the
 * optimization flags are restated.
 */
export function buildEnv({ triple, includeDir, macMin, base = process.env }) {
  const t = goTarget(triple);
  const env = { ...base, CGO_ENABLED: "1", GOOS: t.goos, GOARCH: t.goarch };
  const cflags = ["-O2", "-g", `-I${includeDir}`];
  const ldflagsC = [];
  if (t.darwin) {
    if (!macMin) throw new Error("the macOS minimum system version is required for darwin targets");
    const arch = t.goarch === "arm64" ? "arm64" : "x86_64";
    cflags.push(`-mmacosx-version-min=${macMin}`, "-arch", arch);
    ldflagsC.push(`-mmacosx-version-min=${macMin}`, "-arch", arch);
    env.MACOSX_DEPLOYMENT_TARGET = macMin;
  }
  env.CGO_CFLAGS = cflags.join(" ");
  env.CGO_LDFLAGS = ldflagsC.join(" ");
  if (t.windows && process.platform !== "win32" && !base.CC) {
    // Windows cross-build from macOS/Linux with mingw
    env.CC = t.goarch === "arm64" ? "aarch64-w64-mingw32-gcc" : "x86_64-w64-mingw32-gcc";
  }
  return env;
}

export function goBuildArgs({ output, version, commit, buildTime }) {
  return [
    "build",
    "-trimpath",
    "-tags",
    BUILD_TAGS,
    "-ldflags",
    ldflags({ version, commit, buildTime }),
    "-o",
    output,
    "./cmd/riochat",
  ];
}

export function parseArgs(argv) {
  const { flags, positional } = parseFlags(argv, { target: "value" });
  if (positional.length > 0) throw new Error(`unexpected argument ${positional[0]}`);
  return { target: flags.target ?? "" };
}

export function hostTriple() {
  const out = capture("rustc", ["-vV"]);
  const line = out?.split("\n").find((l) => l.startsWith("host:"));
  if (!line) throw new Error("cannot determine the host triple: is Rust installed? (pass --target)");
  return line.slice("host:".length).trim();
}

function gitCommit() {
  if (process.env.GIT_COMMIT) return process.env.GIT_COMMIT;
  const out = capture("git", ["rev-parse", "--short=12", "HEAD"], { cwd: repoRoot });
  return out?.trim() || "unknown";
}

/** Copies the SQLite binding headers out of the go-sqlite3 module into a fresh include folder. */
function prepareSqliteHeaders(backendDir, includeDir) {
  const out = capture("go", ["mod", "download", "-json", SQLITE_MODULE], { cwd: backendDir });
  if (!out) throw new Error(`cannot download ${SQLITE_MODULE} (is Go installed?)`);
  const dir = JSON.parse(out).Dir;
  if (!dir) throw new Error(`go did not report a folder for ${SQLITE_MODULE}`);
  rmSync(includeDir, { recursive: true, force: true });
  mkdirSync(includeDir, { recursive: true });
  copyFileSync(path.join(dir, "sqlite3-binding.h"), path.join(includeDir, "sqlite3.h"));
  copyFileSync(path.join(dir, "sqlite3ext.h"), path.join(includeDir, "sqlite3ext.h"));
}

export function main(argv) {
  const { target } = parseArgs(argv);
  const triple = target || hostTriple();
  goTarget(triple); // validates
  const backendDir = path.join(repoRoot, "backend");
  const version = readFileSync(path.join(repoRoot, "VERSION"), "utf8").trim();
  const commit = gitCommit();
  const buildTime = process.env.BUILD_TIME || utcStamp();
  const macMin = goTarget(triple).darwin ? macMinimumVersion() : "";

  if (process.env.RIOCHAT_REFRESH_CATALOG_SNAPSHOTS === "1") {
    if (existsSync(path.join(backendDir, "cmd", "catalog-snapshot"))) {
      // runs for the build machine, never cross-compiled; a failure keeps the existing snapshots
      const status = run("go", ["run", "./cmd/catalog-snapshot"], { cwd: backendDir });
      if (status !== 0) console.warn("catalog refresh failed; keeping the existing snapshots");
    } else {
      console.warn("no catalog snapshot tool in this tree; skipping the refresh");
    }
  }

  const includeDir = path.join(tauriDir, "..", ".sidecar-build", "include");
  prepareSqliteHeaders(backendDir, includeDir);

  const outDir = path.join(tauriDir, "binaries");
  mkdirSync(outDir, { recursive: true });
  const output = path.join(outDir, outputFileName(triple));
  console.log(`building riochat-server ${version} (${commit}) for ${triple}`);
  const env = buildEnv({ triple, includeDir, macMin });
  return run("go", goBuildArgs({ output, version, commit, buildTime }), { cwd: backendDir, env });
}

if (isMain(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(`build-sidecar: ${e.message}`);
    process.exit(1);
  }
}
