import { test } from "node:test";
import assert from "node:assert/strict";
import path from "node:path";
import { ldflags, outputPath, parseArgs, platformForTriple, utcStamp } from "./build-api.mjs";

test("timestamp uses the UTC second-precision form", () => {
  assert.equal(utcStamp(new Date("2026-03-04T05:06:07.891Z")), "2026-03-04T05:06:07Z");
});

test("ldflags embed version, commit and build time and strip symbols", () => {
  const f = ldflags({ version: "1.2.3", commit: "abc", buildTime: "t" });
  assert.match(f, /^-s -w /);
  assert.ok(f.includes("buildinfo.Version=1.2.3"));
  assert.ok(f.includes("buildinfo.Commit=abc"));
  assert.ok(f.includes("buildinfo.BuildTime=t"));
});

test("sidecar output is named for the target triple", () => {
  const p = outputPath("/r", { sidecar: true, out: "", target: "x86_64-pc-windows-msvc" });
  assert.equal(p, path.join("/r", "desktop", "src-tauri", "binaries", "riochat-server-x86_64-pc-windows-msvc.exe"));
});

test("default output lands in .cache/riochat", () => {
  const p = outputPath("/r", { sidecar: false, out: "", target: "x86_64-unknown-linux-gnu" });
  assert.equal(p, path.join("/r", ".cache", "riochat", "riochat"));
});

test("unknown triples and arguments are rejected", () => {
  assert.throws(() => platformForTriple("riscv-nope"), /unsupported target triple/);
  assert.throws(() => parseArgs(["--bogus"]), /unknown argument/);
  assert.equal(platformForTriple("aarch64-apple-darwin").goarch, "arm64");
});
