import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { applyAppleIdentity, exactlyOne, machineArch, parseEnvFile, prepareEnv } from "../scripts/build-signed.mjs";
import { identityFromSubject, parseSubject } from "../scripts/apple-identity.mjs";
import { isAdHoc, plan, releaseName, serviceConfig } from "../scripts/post-dmg.mjs";
import { checkBinary, compareVersions, inspectMachO, main as verify, parseArgs } from "../scripts/verify-macos-bundle.mjs";
import { isReachable } from "../scripts/dev-server.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const scripts = path.join(here, "..", "scripts");

test("env file parsing", () => {
  const vars = parseEnvFile(`
# comment
export A=1
B = "two words"
C='single # kept'
D=plain # trailing comment
E=
not a pair
F=a=b
`);
  assert.deepEqual(vars, { A: "1", B: "two words", C: "single # kept", D: "plain", E: "", F: "a=b" });
});

test("blank variables are unset and the updater key password always exists", () => {
  const env = prepareEnv({ KEEP: "x", BLANK: "" }, { base: { BLANK: "inherited", OTHER: "y" } });
  assert.equal(env.KEEP, "x");
  assert.equal("BLANK" in env, false);
  assert.equal(env.OTHER, "y");
  assert.equal(env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD, "");
  const kept = prepareEnv({ TAURI_SIGNING_PRIVATE_KEY_PASSWORD: "pw" }, { base: {} });
  assert.equal(kept.TAURI_SIGNING_PRIVATE_KEY_PASSWORD, "pw");
});

test("key and certificate content are derived from *_PATH variables", () => {
  const files = { "/k": Buffer.from("PRIVATE KEY TEXT"), "/a.p12": Buffer.from([1, 2, 3]), "/w.pfx": Buffer.from([4, 5]) };
  const opts = { base: {}, resolve: (p) => p, read: (p) => files[p] };
  const env = prepareEnv(
    { TAURI_SIGNING_PRIVATE_KEY_PATH: "/k", APPLE_CERTIFICATE_PATH: "/a.p12", WINDOWS_CERTIFICATE_PATH: "/w.pfx" },
    opts,
  );
  assert.equal(env.TAURI_SIGNING_PRIVATE_KEY, "PRIVATE KEY TEXT");
  assert.equal(env.APPLE_CERTIFICATE, Buffer.from([1, 2, 3]).toString("base64"));
  assert.equal(env.WINDOWS_CERTIFICATE, Buffer.from([4, 5]).toString("base64"));
  // content given directly wins
  const direct = prepareEnv({ APPLE_CERTIFICATE: "ZGlyZWN0", APPLE_CERTIFICATE_PATH: "/a.p12" }, opts);
  assert.equal(direct.APPLE_CERTIFICATE, "ZGlyZWN0");
});

test("Apple identity: certificate, ad-hoc on macOS, nothing elsewhere", () => {
  const warnings = [];
  const withCert = applyAppleIdentity(
    { APPLE_CERTIFICATE: "x" },
    "darwin",
    () => "APPLE_SIGNING_IDENTITY=Developer ID Application: A (TEAM123)\nAPPLE_TEAM_ID=TEAM123\n",
    (w) => warnings.push(w),
  );
  assert.equal(withCert.APPLE_SIGNING_IDENTITY, "Developer ID Application: A (TEAM123)");
  assert.equal(withCert.APPLE_TEAM_ID, "TEAM123");
  assert.equal(warnings.length, 0);
  const adHoc = applyAppleIdentity({}, "darwin", () => "", (w) => warnings.push(w));
  assert.equal(adHoc.APPLE_SIGNING_IDENTITY, "-");
  assert.equal(warnings.length, 1);
  assert.equal(applyAppleIdentity({}, "linux", () => "", () => {}).APPLE_SIGNING_IDENTITY, undefined);
  assert.throws(() => applyAppleIdentity({ APPLE_CERTIFICATE: "x" }, "darwin", () => ""), /did not yield/);
});

test("exactly one artifact is required", () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "bundle-"));
  assert.throws(() => exactlyOne(path.join(dir, "missing"), ".dmg"), /found 0/);
  writeFileSync(path.join(dir, "a.dmg"), "");
  assert.equal(exactlyOne(dir, ".dmg"), path.join(dir, "a.dmg"));
  writeFileSync(path.join(dir, "b.dmg"), "");
  assert.throws(() => exactlyOne(dir, ".dmg"), /found 2/);
  assert.equal(machineArch("arm64"), "arm64");
  assert.equal(machineArch("x64"), "x86_64");
});

test("the signed build needs an environment file", () => {
  const r = spawnSync(process.execPath, [path.join(scripts, "build-signed.mjs")], {
    encoding: "utf8",
    env: { ...process.env, DESKTOP_SIGNING_ENV: path.join(os.tmpdir(), "definitely-missing.env") },
  });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /signing environment file not found/);
});

test("certificate subjects", () => {
  const dn = parseSubject("UID=ABCDE12345,CN=Developer ID Application: Acme\\, Inc. (ABCDE12345),OU=ABCDE12345,O=Acme,C=US");
  assert.equal(dn.CN, "Developer ID Application: Acme, Inc. (ABCDE12345)");
  assert.equal(dn.OU, "ABCDE12345");
  assert.deepEqual(identityFromSubject("subject=CN=Dev ID (T1),OU=T1"), { identity: "Dev ID (T1)", teamId: "T1" });
  assert.throws(() => identityFromSubject("subject=CN=only"), /cannot read the CN and OU/);
  assert.throws(() => identityFromSubject("subject=OU=only"), /cannot read the CN and OU/);
});

test("apple-identity prints nothing without a certificate and fails on garbage", () => {
  const clean = { ...process.env };
  delete clean.APPLE_CERTIFICATE;
  delete clean.APPLE_CERTIFICATE_PATH;
  const none = spawnSync(process.execPath, [path.join(scripts, "apple-identity.mjs")], { encoding: "utf8", env: clean });
  assert.equal(none.status, 0);
  assert.equal(none.stdout, "");
  const bad = spawnSync(process.execPath, [path.join(scripts, "apple-identity.mjs")], {
    encoding: "utf8",
    env: { ...clean, APPLE_CERTIFICATE: Buffer.from("not a certificate").toString("base64"), APPLE_CERTIFICATE_PASSWORD: "x" },
  });
  assert.equal(bad.status, 1);
  assert.equal(bad.stdout, "");
});

test("DMG release names and signing plans", () => {
  assert.equal(releaseName("Riochat_0.4.5_aarch64.dmg"), "Riochat-0.4.5-macos-arm64");
  assert.equal(releaseName("/x/y/Riochat_0.4.5-beta.1_x64.dmg"), "Riochat-0.4.5-beta.1-macos-x64");
  assert.equal(releaseName("Riochat-0.4.5-macos-x64.dmg"), "Riochat-0.4.5-macos-x64");
  assert.throws(() => releaseName("weird.dmg"), /cannot derive/);
  const all = {
    SIGN_AND_NOTARIZE_SCRIPT_URL: "u1",
    SIGN_AND_NOTARIZE_BASE_URL: "u2",
    SIGN_AND_NOTARIZE_API_TOKEN: "t",
    SIGN_AND_NOTARIZE_HELPER_URL: "u3",
  };
  assert.ok(serviceConfig(all));
  assert.equal(serviceConfig({ ...all, SIGN_AND_NOTARIZE_API_TOKEN: "" }), null);
  assert.equal(plan({ ...all, APPLE_TEAM_ID: "T", APPLE_SIGNING_IDENTITY: "ID" }), "service", "the service takes precedence");
  assert.equal(plan({ APPLE_TEAM_ID: "T", APPLE_SIGNING_IDENTITY: "ID" }), "apple");
  assert.equal(plan({ APPLE_TEAM_ID: "T", APPLE_SIGNING_IDENTITY: "-" }), "none");
  assert.equal(plan({}), "none");
  assert.equal(isAdHoc("-"), true);
  assert.equal(isAdHoc("Developer ID"), false);
});

// A minimal 64-bit Mach-O header with one load command.
function macho({ cpu, cmd, minos, platform = 1 }) {
  const body = Buffer.alloc(24);
  let size = 24;
  if (cmd === 0x32) {
    body.writeUInt32LE(0x32, 0);
    body.writeUInt32LE(24, 4);
    body.writeUInt32LE(platform, 8);
    body.writeUInt32LE(minos, 12);
  } else {
    body.writeUInt32LE(0x24, 0);
    body.writeUInt32LE(16, 4);
    body.writeUInt32LE(minos, 8);
    size = 16;
  }
  const head = Buffer.alloc(32);
  head.writeUInt32LE(0xfeedfacf, 0);
  head.writeUInt32LE(cpu, 4);
  head.writeUInt32LE(1, 16);
  head.writeUInt32LE(size, 20);
  return Buffer.concat([head, body.subarray(0, size)]);
}
const v = (a, b, c = 0) => (a << 16) | (b << 8) | c;

test("Mach-O inspection", () => {
  assert.deepEqual(inspectMachO(macho({ cpu: 0x0100000c, cmd: 0x32, minos: v(12, 0) })), { kind: "thin", arch: "arm64", minos: "12.0.0" });
  assert.deepEqual(inspectMachO(macho({ cpu: 0x01000007, cmd: 0x24, minos: v(11, 3, 1) })), { kind: "thin", arch: "x86_64", minos: "11.3.1" });
  // a non-macOS platform's build version is ignored
  assert.equal(inspectMachO(macho({ cpu: 0x0100000c, cmd: 0x32, minos: v(17, 0), platform: 2 })).minos, null);
  const fat = Buffer.alloc(8 + 20 * 2);
  fat.writeUInt32BE(0xcafebabe, 0);
  fat.writeUInt32BE(2, 4);
  fat.writeInt32BE(0x0100000c, 8);
  fat.writeInt32BE(0x01000007, 28);
  assert.deepEqual(inspectMachO(fat), { kind: "fat", archs: ["arm64", "x86_64"] });
  assert.equal(inspectMachO(Buffer.from("#!/bin/sh\n")).kind, "unknown");
  assert.equal(inspectMachO(Buffer.alloc(2)).kind, "unknown");
});

test("binary checks: arch, universal and minimum version", () => {
  const arm = macho({ cpu: 0x0100000c, cmd: 0x32, minos: v(12, 0) });
  assert.equal(checkBinary("x", arm, "arm64", "12.0").arch, "arm64");
  assert.throws(() => checkBinary("x", arm, "x86_64", "12.0"), /built for arm64, expected x86_64/);
  assert.throws(() => checkBinary("x", macho({ cpu: 0x0100000c, cmd: 0x32, minos: v(13, 0) }), "arm64", "12.0"), /newer than the bundle's minimum/);
  assert.throws(() => checkBinary("x", Buffer.from("text"), "arm64", "12.0"), /not a Mach-O/);
  const fat = Buffer.alloc(28);
  fat.writeUInt32BE(0xcafebabe, 0);
  fat.writeUInt32BE(1, 4);
  fat.writeInt32BE(0x0100000c, 8);
  assert.throws(() => checkBinary("x", fat, "arm64", "12.0"), /universal/);
  assert.equal(compareVersions("12.0", "12.0.0"), 0);
  assert.equal(compareVersions("12.1", "12.0.5"), 1);
  assert.equal(compareVersions("11.7", "12"), -1);
});

test("verifier usage", () => {
  assert.equal(parseArgs(["--arch", "arm64", "a.dmg"]).arch, "arm64");
  for (const bad of [[], ["--arch"], ["--arch", "ppc", "a.dmg"], ["--arch", "arm64"], ["a.dmg"], ["--arch", "arm64", "--x"]]) {
    assert.equal(parseArgs(bad), null, JSON.stringify(bad));
  }
  const quiet = console.error;
  console.error = () => {};
  try {
    assert.equal(verify([]), 2);
    assert.equal(verify(["--arch", "arm64", path.join(os.tmpdir(), "nope.txt")]), 1);
  } finally {
    console.error = quiet;
  }
});

test("dev server reachability", async () => {
  const server = http.createServer((req, res) => res.writeHead(404).end());
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  assert.equal(await isReachable(`http://127.0.0.1:${port}/`, 1000), true, "any HTTP answer counts");
  await new Promise((r) => server.close(r));
  assert.equal(await isReachable(`http://127.0.0.1:${port}/`, 500), false);
  // a server that never answers times out
  const silent = http.createServer(() => {});
  await new Promise((r) => silent.listen(0, "127.0.0.1", r));
  const started = Date.now();
  assert.equal(await isReachable(`http://127.0.0.1:${silent.address().port}/`, 200), false);
  assert.ok(Date.now() - started < 2000);
  silent.closeAllConnections();
  await new Promise((r) => silent.close(r));
});
