import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { buildConfig, main, msiVersion, render } from "../scripts/release-config.mjs";

const script = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "scripts", "release-config.mjs");

test("MSI product versions", () => {
  const table = {
    "1.2.3": "1.2.3",
    "0.4.4-beta.2": "0.4.4.2",
    "1.2.3-rc.10": "1.2.3.10",
    "1.2.3-rc": "1.2.3.0",
    "1.2.3-beta.2.hotfix": "1.2.3.2",
    "1.2.3+build.5": "1.2.3",
    "1.2.3-rc.1+meta": "1.2.3.1",
    "255.255.65535": "255.255.65535",
  };
  for (const [input, want] of Object.entries(table)) assert.equal(msiVersion(input), want, input);
});

test("MSI version errors", () => {
  assert.throws(() => msiVersion("1.2"), /semantic version/);
  assert.throws(() => msiVersion("v1.2.3"), /semantic version/);
  assert.throws(() => msiVersion("256.0.0"), /at most 255/);
  assert.throws(() => msiVersion("0.256.0"), /at most 255/);
  assert.throws(() => msiVersion("1.0.65536"), /patch/);
  assert.throws(() => msiVersion("1.0.0-beta.65536"), /revision/);
});

test("stable keeps the configured endpoint", () => {
  const cfg = buildConfig({ channel: "stable", version: "1.2.3" });
  assert.deepEqual(cfg, { bundle: { windows: { wix: { version: "1.2.3" } } } });
});

test("beta overrides the updater endpoint and needs a repository", () => {
  const cfg = buildConfig({ channel: "beta", repository: "acme/chat", version: "1.2.3-beta.4" });
  assert.deepEqual(cfg.plugins.updater.endpoints, ["https://github.com/acme/chat/releases/download/desktop-beta/latest.json"]);
  assert.equal(cfg.bundle.windows.wix.version, "1.2.3.4");
  assert.throws(() => buildConfig({ channel: "beta", version: "1.2.3-beta.1" }), /owner\/name/);
  assert.throws(() => buildConfig({ channel: "beta", repository: "bad", version: "1.2.3-beta.1" }), /owner\/name/);
});

test("unknown channel and thumbprints", () => {
  assert.throws(() => buildConfig({ channel: "nightly", version: "1.2.3" }), /unknown channel/);
  const cfg = buildConfig({ channel: "stable", version: "1.2.3", thumbprint: "ab".repeat(20) });
  assert.equal(cfg.bundle.windows.certificateThumbprint, "AB".repeat(20));
  for (const bad of ["", "abc", "zz".repeat(20), "ab".repeat(21)]) {
    assert.throws(() => buildConfig({ channel: "stable", version: "1.2.3", thumbprint: bad }), /40 hexadecimal/, bad);
  }
});

test("output is one --config argument followed by whitespace-free JSON", () => {
  const line = render({ channel: "beta", repository: "a/b", version: "1.0.0-beta.1", thumbprint: "cd".repeat(20) });
  assert.match(line, /^--config \{/);
  const json = line.slice("--config ".length);
  assert.ok(!/\s/.test(json));
  JSON.parse(json);
  const lines = [];
  main(["--channel", "stable", "--version", "2.0.0"], (l) => lines.push(l));
  assert.equal(lines.length, 1);
});

test("the CLI exits 1 with a message on errors", () => {
  const bad = spawnSync(process.execPath, [script, "--channel", "weird", "--version", "1.0.0"], { encoding: "utf8" });
  assert.equal(bad.status, 1);
  assert.match(bad.stderr, /unknown channel/);
  const ok = spawnSync(process.execPath, [script, "--channel", "stable"], { encoding: "utf8" });
  assert.equal(ok.status, 0);
  assert.equal(ok.stdout.trim().split("\n").length, 1);
  const missing = spawnSync(process.execPath, [script], { encoding: "utf8" });
  assert.equal(missing.status, 1);
});
