import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, utimesSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  archLabel,
  collectEntries,
  discoverExe,
  discoverSidecar,
  packagePortable,
  parseArgs,
  readmeText,
  resolveWebview2,
  zipName,
} from "../scripts/package-portable.mjs";
import { buildZip, crc32, dosDateTime, entryDate } from "../scripts/lib/zip.mjs";
import { readZip } from "./helpers/unzip.mjs";

const script = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "scripts", "package-portable.mjs");
const tmp = () => mkdtempSync(path.join(os.tmpdir(), "portable-"));

function fakeBuild() {
  const dir = tmp();
  const exe = path.join(dir, "app.exe");
  const sidecar = path.join(dir, "server.exe");
  writeFileSync(exe, Buffer.from("MZ-app".repeat(500)));
  writeFileSync(sidecar, Buffer.from("MZ-server"));
  return { dir, exe, sidecar };
}

test("archive names", () => {
  assert.equal(zipName({ version: "0.4.5", triple: "x86_64-pc-windows-msvc", offline: false }), "Riochat-0.4.5-windows-x64-portable.zip");
  assert.equal(zipName({ version: "0.4.5", triple: "aarch64-pc-windows-msvc", offline: true }), "Riochat-0.4.5-windows-arm64-portable-offline.zip");
  assert.equal(archLabel("i686-pc-windows-msvc"), "x86");
  assert.throws(() => archLabel("x86_64-apple-darwin"), /unsupported target/);
  assert.throws(() => archLabel("riscv64-pc-windows-msvc"), /unsupported target/);
});

test("crc32 known values", () => {
  assert.equal(crc32(Buffer.from("123456789")), 0xcbf43926);
  assert.equal(crc32(Buffer.alloc(0)), 0);
});

test("zip entries: attributes, flags, compression and CRLF text", () => {
  const big = Buffer.from("abc".repeat(1000));
  const noise = Buffer.from(Array.from({ length: 64 }, (_, i) => (i * 97 + 13) & 0xff));
  const zip = buildZip(
    [
      { name: "Root/", dir: true },
      { name: "Root/big.txt", data: big },
      { name: "Root/noise.bin", data: noise, mode: 0o755 },
      // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
      { name: "Root/\u540d\u524d.txt", data: Buffer.from("x") },
      { name: "Root/empty", data: Buffer.alloc(0) },
      { name: "Root/sub", dir: true },
    ],
    new Date(Date.UTC(2026, 0, 2, 3, 4, 6)),
  );
  const entries = readZip(zip);
  // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
  assert.deepEqual(entries.map((e) => e.name), ["Root/", "Root/big.txt", "Root/noise.bin", "Root/\u540d\u524d.txt", "Root/empty", "Root/sub/"]);
  const by = Object.fromEntries(entries.map((e) => [e.name, e]));
  assert.equal(by["Root/"].dosDir, true);
  assert.equal(by["Root/"].unixMode & 0o170000, 0o040000);
  assert.equal(by["Root/sub/"].dosDir, true);
  assert.equal(by["Root/big.txt"].method, 8, "compressible data is deflated");
  assert.equal(by["Root/noise.bin"].method, 0, "incompressible data is stored");
  assert.equal(by["Root/noise.bin"].unixMode & 0o777, 0o755);
  assert.equal(by["Root/empty"].method, 0);
  assert.equal(by["Root/big.txt"].flags & 0x800, 0, "no UTF-8 flag for ASCII names");
  // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
  assert.equal(by["Root/\u540d\u524d.txt"].flags & 0x800, 0x800, "UTF-8 flag for non-ASCII names");
  assert.deepEqual(by["Root/big.txt"].data, big);
  assert.throws(() => buildZip([{ name: "a\\b", data: Buffer.alloc(1) }]), /forward slashes/);
});

test("timestamps are deterministic and clamped", () => {
  assert.deepEqual(dosDateTime(new Date(Date.UTC(2026, 0, 2, 3, 4, 6))), { time: (3 << 11) | (4 << 5) | 3, date: ((2026 - 1980) << 9) | (1 << 5) | 2 });
  assert.equal(entryDate({ SOURCE_DATE_EPOCH: "0" }).getUTCFullYear(), 1980);
  assert.equal(entryDate({ SOURCE_DATE_EPOCH: "1767225600" }).toISOString(), "2026-01-01T00:00:00.000Z");
  const now = new Date("2030-05-05T00:00:00Z");
  assert.equal(entryDate({}, now), now);
  assert.equal(entryDate({ SOURCE_DATE_EPOCH: "junk" }, now), now);
});

test("a portable package has one root folder and CRLF text files", () => {
  const { dir, exe, sidecar } = fakeBuild();
  const out = path.join(dir, "out");
  const zipPath = packagePortable({ version: "1.2.3", exe, sidecar, outDir: out }, { SOURCE_DATE_EPOCH: "1767225600" });
  assert.equal(path.basename(zipPath), "Riochat-1.2.3-windows-x64-portable.zip");
  const entries = readZip(readFileSync(zipPath));
  const names = entries.map((e) => e.name);
  assert.deepEqual(names, ["Riochat/", "Riochat/Riochat.exe", "Riochat/riochat-server.exe", "Riochat/portable", "Riochat/README.txt"]);
  assert.ok(names.every((n) => n.startsWith("Riochat/")));
  const get = (n) => entries.find((e) => e.name === n);
  assert.deepEqual(get("Riochat/Riochat.exe").data, readFileSync(exe));
  assert.deepEqual(get("Riochat/riochat-server.exe").data, readFileSync(sidecar));
  const marker = get("Riochat/portable").data.toString("utf8");
  assert.ok(marker.endsWith("\r\n") && !/[^\r]\n/.test(marker));
  const readme = get("Riochat/README.txt").data.toString("utf8");
  assert.ok(!/[^\r]\n/.test(readme), "README uses CRLF");
  assert.match(readme, /com\.riochat\.desktop\.portable-/);
  assert.match(readme, /Credential Manager/);
  assert.ok(!readdirSync(out).some((n) => n.endsWith(".partial")));
});

test("builds are reproducible under a fixed timestamp", () => {
  const { dir, exe, sidecar } = fakeBuild();
  const env = { SOURCE_DATE_EPOCH: "1767225600" };
  const a = readFileSync(packagePortable({ version: "1.0.0", exe, sidecar, outDir: path.join(dir, "a") }, env));
  const b = readFileSync(packagePortable({ version: "1.0.0", exe, sidecar, outDir: path.join(dir, "b") }, env));
  assert.ok(a.equals(b));
});

test("the offline variant carries the WebView2 runtime", () => {
  const { dir, exe, sidecar } = fakeBuild();
  // the folder given holds the runtime folder as its only subfolder
  const given = path.join(dir, "download");
  const runtime = path.join(given, "Microsoft.WebView2.FixedVersionRuntime.130.0");
  mkdirSync(path.join(runtime, "Locales"), { recursive: true });
  writeFileSync(path.join(runtime, "msedgewebview2.exe"), "x");
  writeFileSync(path.join(runtime, "Locales", "en-US.pak"), "y");
  const zipPath = packagePortable({ version: "1.0.0", exe, sidecar, webview2: given, outDir: path.join(dir, "o") }, {});
  assert.match(zipPath, /portable-offline\.zip$/);
  const names = readZip(readFileSync(zipPath)).map((e) => e.name);
  assert.ok(names.includes("Riochat/webview2/"));
  assert.ok(names.includes("Riochat/webview2/msedgewebview2.exe"));
  assert.ok(names.includes("Riochat/webview2/Locales/"));
  assert.ok(names.includes("Riochat/webview2/Locales/en-US.pak"));
});

test("WebView2 folder resolution", () => {
  const base = tmp();
  const direct = path.join(base, "direct");
  mkdirSync(direct);
  writeFileSync(path.join(direct, "msedgewebview2.exe"), "x");
  assert.equal(resolveWebview2(direct), direct);

  const wrapper = path.join(base, "wrapper");
  mkdirSync(path.join(wrapper, "inner"), { recursive: true });
  writeFileSync(path.join(wrapper, "inner", "msedgewebview2.exe"), "x");
  assert.equal(resolveWebview2(wrapper), path.join(wrapper, "inner"));

  const two = path.join(base, "two");
  mkdirSync(path.join(two, "a"), { recursive: true });
  mkdirSync(path.join(two, "b"), { recursive: true });
  writeFileSync(path.join(two, "a", "msedgewebview2.exe"), "x");
  assert.throws(() => resolveWebview2(two), /does not contain the WebView2 runtime/);
  assert.throws(() => resolveWebview2(path.join(base, "missing")), /not found/);
  const empty = path.join(base, "empty");
  mkdirSync(empty);
  assert.throws(() => resolveWebview2(empty), /does not contain/);
});

test("binary discovery order", () => {
  const src = tmp();
  const release = path.join(src, "target", "release");
  const triple = path.join(src, "target", "x86_64-pc-windows-msvc", "release");
  mkdirSync(release, { recursive: true });
  mkdirSync(triple, { recursive: true });
  assert.throws(() => discoverExe({ triple: "x86_64-pc-windows-msvc", name: "riochat-desktop", src }), /run the Windows build first/);
  writeFileSync(path.join(release, "riochat-desktop.exe"), "old");
  writeFileSync(path.join(triple, "riochat-desktop.exe"), "new");
  utimesSync(path.join(release, "riochat-desktop.exe"), new Date(2020, 0, 1), new Date(2020, 0, 1));
  assert.equal(discoverExe({ triple: "x86_64-pc-windows-msvc", name: "riochat-desktop", src }), path.join(triple, "riochat-desktop.exe"));
  utimesSync(path.join(release, "riochat-desktop.exe"), new Date(2030, 0, 1), new Date(2030, 0, 1));
  assert.equal(discoverExe({ triple: "x86_64-pc-windows-msvc", name: "riochat-desktop", src }), path.join(release, "riochat-desktop.exe"));

  assert.throws(() => discoverSidecar({ triple: "x86_64-pc-windows-msvc", src }), /sidecar build first/);
  writeFileSync(path.join(release, "riochat-server.exe"), "d");
  assert.equal(discoverSidecar({ triple: "x86_64-pc-windows-msvc", src }), path.join(release, "riochat-server.exe"));
  writeFileSync(path.join(triple, "riochat-server.exe"), "t");
  assert.equal(discoverSidecar({ triple: "x86_64-pc-windows-msvc", src }), path.join(triple, "riochat-server.exe"));
  mkdirSync(path.join(src, "binaries"), { recursive: true });
  writeFileSync(path.join(src, "binaries", "riochat-server-x86_64-pc-windows-msvc.exe"), "signed");
  assert.equal(discoverSidecar({ triple: "x86_64-pc-windows-msvc", src }), path.join(src, "binaries", "riochat-server-x86_64-pc-windows-msvc.exe"));
});

test("strict argument parsing", () => {
  assert.deepEqual(parseArgs(["--version", "1.0.0", "--out-dir", "o", "--target", "aarch64-pc-windows-msvc"]), {
    version: "1.0.0",
    webview2: undefined,
    outDir: "o",
    exe: undefined,
    sidecar: undefined,
    target: "aarch64-pc-windows-msvc",
  });
  assert.throws(() => parseArgs(["--bogus"]), /unknown argument/);
  assert.throws(() => parseArgs(["--version"]), /needs a value/);
  assert.throws(() => parseArgs(["--version", "--exe", "x"]), /needs a value/);
  const r = spawnSync(process.execPath, [script, "--bogus"], { encoding: "utf8" });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /unknown argument/);
});

test("readme mentions the offline runtime only when included", () => {
  assert.match(readmeText({ version: "1", offline: true }), /includes it in the webview2 folder/);
  assert.doesNotMatch(readmeText({ version: "1", offline: false }), /includes it in the webview2 folder/);
});

test("collectEntries lists directories before their files", () => {
  const { exe, sidecar } = fakeBuild();
  const names = collectEntries({ exe, sidecar, version: "1.0.0" }).map((e) => e.name);
  assert.equal(names[0], "Riochat/");
});
