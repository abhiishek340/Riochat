import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import {
  BUILD_TAGS,
  buildEnv,
  goBuildArgs,
  goTarget,
  ldflags,
  macMinimumVersion,
  outputFileName,
  parseArgs,
  utcStamp,
} from "../scripts/build-sidecar.mjs";

test("target mapping", () => {
  assert.deepEqual(goTarget("aarch64-apple-darwin"), { goos: "darwin", goarch: "arm64", windows: false, darwin: true });
  assert.equal(goTarget("x86_64-apple-darwin").goarch, "amd64");
  assert.equal(goTarget("x86_64-pc-windows-msvc").goos, "windows");
  assert.equal(goTarget("aarch64-unknown-linux-gnu").goarch, "arm64");
  assert.equal(goTarget("x86_64-unknown-linux-musl").goos, "linux");
  for (const bad of ["riscv64-unknown-linux-gnu", "x86_64-unknown-freebsd", "nope", "", "i686-pc-windows-msvc"]) {
    assert.throws(() => goTarget(bad), /unsupported target triple/, bad);
  }
});

test("output names", () => {
  assert.equal(outputFileName("aarch64-apple-darwin"), "riochat-server-aarch64-apple-darwin");
  assert.equal(outputFileName("x86_64-pc-windows-msvc"), "riochat-server-x86_64-pc-windows-msvc.exe");
});

test("link flags inject version, commit and build time", () => {
  const flags = ldflags({ version: "1.2.3", commit: "abc", buildTime: "2026-01-01T00:00:00Z" });
  assert.match(flags, /^-s -w /);
  assert.match(flags, /buildinfo\.Version=1\.2\.3/);
  assert.match(flags, /buildinfo\.Commit=abc/);
  assert.match(flags, /buildinfo\.BuildTime=2026-01-01T00:00:00Z/);
  assert.match(utcStamp(new Date("2026-03-04T05:06:07.890Z")), /^2026-03-04T05:06:07Z$/);
});

test("go build arguments", () => {
  const args = goBuildArgs({ output: "/o/x", version: "1", commit: "c", buildTime: "t" });
  assert.deepEqual(args.slice(0, 4), ["build", "-trimpath", "-tags", BUILD_TAGS]);
  assert.equal(BUILD_TAGS, "nopostgres,noredis,nos3,noswagger,nomsgpack,nootlp");
  assert.equal(args.at(-1), "./cmd/riochat");
  assert.equal(args[args.indexOf("-o") + 1], "/o/x");
});

test("environment for darwin targets pins the deployment target", () => {
  const env = buildEnv({ triple: "aarch64-apple-darwin", includeDir: "/inc", macMin: "12.0", base: { PATH: "/bin" } });
  assert.equal(env.CGO_ENABLED, "1");
  assert.equal(env.GOOS, "darwin");
  assert.equal(env.GOARCH, "arm64");
  assert.equal(env.MACOSX_DEPLOYMENT_TARGET, "12.0");
  assert.match(env.CGO_CFLAGS, /-O2 -g -I\/inc/);
  assert.match(env.CGO_CFLAGS, /-mmacosx-version-min=12\.0/);
  assert.match(env.CGO_LDFLAGS, /-mmacosx-version-min=12\.0/);
  assert.throws(() => buildEnv({ triple: "aarch64-apple-darwin", includeDir: "/inc", macMin: "" }), /minimum system version/);
});

test("environment for other targets restates optimization flags", () => {
  const env = buildEnv({ triple: "x86_64-unknown-linux-gnu", includeDir: "/inc", macMin: "", base: {} });
  assert.equal(env.CGO_CFLAGS, "-O2 -g -I/inc");
  assert.equal(env.MACOSX_DEPLOYMENT_TARGET, undefined);
  const win = buildEnv({ triple: "x86_64-pc-windows-msvc", includeDir: "/inc", macMin: "", base: {} });
  assert.equal(win.GOOS, "windows");
  if (process.platform !== "win32") assert.equal(win.CC, "x86_64-w64-mingw32-gcc");
  const custom = buildEnv({ triple: "x86_64-pc-windows-msvc", includeDir: "/inc", macMin: "", base: { CC: "my-cc" } });
  assert.equal(custom.CC, "my-cc");
});

test("minimum macOS version comes from the app configuration", () => {
  assert.equal(macMinimumVersion(), "12.0");
  const dir = mkdtempSync(path.join(os.tmpdir(), "sc-"));
  const file = path.join(dir, "tauri.conf.json");
  writeFileSync(file, JSON.stringify({ bundle: {} }));
  assert.throws(() => macMinimumVersion(file), /minimumSystemVersion is missing/);
});

test("argument parsing", () => {
  assert.deepEqual(parseArgs([]), { target: "" });
  assert.deepEqual(parseArgs(["--target", "x86_64-apple-darwin"]), { target: "x86_64-apple-darwin" });
  assert.throws(() => parseArgs(["--target"]), /needs a value/);
  assert.throws(() => parseArgs(["--bogus"]), /unknown argument/);
  assert.throws(() => parseArgs(["stray"]), /unexpected argument/);
});
