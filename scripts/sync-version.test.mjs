import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { run } from "./sync-version.mjs";

function fixture(version, manifestVersion = version) {
  const root = mkdtempSync(path.join(tmpdir(), "sync-version-"));
  const put = (rel, text) => {
    mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    writeFileSync(path.join(root, rel), text);
  };
  const json = (name) => `${JSON.stringify({ name, version: manifestVersion, private: true }, null, 2)}\n`;
  put("VERSION", `${version}\n`);
  put("package.json", json("root"));
  put("web/package.json", json("web"));
  put("desktop/package.json", json("desktop"));
  put("backend/package.json", json("backend"));
  put(
    "desktop/src-tauri/Cargo.toml",
    `[package]\nname = "d"\nversion = "${manifestVersion}"\n\n[dependencies]\nserde = { version = "1" }\nurl = "2"\n`,
  );
  put("backend/cmd/riochat/main.go", `// Command riochat.\n//\n// @version ${manifestVersion}\npackage main\n`);
  return { root, put, read: (rel) => readFileSync(path.join(root, rel), "utf8") };
}

function capture(opts) {
  const lines = [];
  const code = run({ ...opts, out: (l) => lines.push(l), err: (l) => lines.push(l) });
  return { code, text: lines.join("\n") };
}

test("matching manifests pass silently", () => {
  const f = fixture("1.2.3");
  const r = capture({ root: f.root, check: true });
  assert.equal(r.code, 0);
  assert.equal(r.text, "");
});

test("one stale manifest is listed, fixed, then passes", () => {
  const f = fixture("1.2.3");
  f.put("web/package.json", `${JSON.stringify({ name: "web", version: "1.2.2" }, null, 2)}\n`);
  let r = capture({ root: f.root, check: true });
  assert.equal(r.code, 1);
  assert.match(r.text, /1 file\(s\)/);
  assert.match(r.text, /web\/package\.json/);
  assert.doesNotMatch(r.text, /desktop\/package\.json/);
  assert.match(r.text, /make version/);
  assert.equal(capture({ root: f.root }).code, 0);
  assert.equal(JSON.parse(f.read("web/package.json")).version, "1.2.3");
  r = capture({ root: f.root, check: true });
  assert.equal(r.code, 0);
});

test("remediation hint names the mode when one is given", () => {
  const f = fixture("1.2.3", "0.0.1");
  const r = capture({ root: f.root, mode: "web", check: true });
  assert.equal(r.code, 1);
  assert.match(r.text, /sync-version\.mjs web/);
});

test("invalid versions abort and name the value", () => {
  for (const bad of ["v1.2.3", "1.2", "1.2.3.4", ""]) {
    const f = fixture("1.2.3");
    f.put("VERSION", `${bad}\n`);
    const r = capture({ root: f.root, check: true });
    assert.equal(r.code, 2, bad);
    assert.match(r.text, /invalid version/);
    assert.ok(r.text.includes(`"${bad}"`));
  }
});

test("unknown mode lists the valid modes", () => {
  const f = fixture("1.2.3");
  const r = capture({ root: f.root, mode: "foo" });
  assert.equal(r.code, 2);
  for (const m of ["all", "workspace", "web", "desktop", "backend"]) assert.ok(r.text.includes(m));
});

test("CRLF manifests are not reported as drift and keep their line endings", () => {
  const f = fixture("1.2.3");
  const crlf = f.read("web/package.json").replace(/\n/g, "\r\n");
  f.put("web/package.json", crlf);
  assert.equal(capture({ root: f.root, check: true }).code, 0);
  f.put("VERSION", "1.2.4\n");
  assert.equal(capture({ root: f.root }).code, 0);
  const after = f.read("web/package.json");
  assert.ok(after.includes("\r\n") && !/[^\r]\n/.test(after));
  assert.equal(JSON.parse(after).version, "1.2.4");
});

test("prerelease versions are accepted and propagated everywhere", () => {
  const f = fixture("1.2.3-beta.1", "1.2.3");
  assert.equal(capture({ root: f.root }).code, 0);
  assert.equal(JSON.parse(f.read("package.json")).version, "1.2.3-beta.1");
  assert.match(f.read("desktop/src-tauri/Cargo.toml"), /^version = "1\.2\.3-beta\.1"$/m);
  assert.match(f.read("backend/cmd/riochat/main.go"), /@version 1\.2\.3-beta\.1/);
});

test("only the package version in Cargo.toml changes", () => {
  const f = fixture("2.0.0", "1.0.0");
  capture({ root: f.root });
  const cargo = f.read("desktop/src-tauri/Cargo.toml");
  assert.match(cargo, /^version = "2\.0\.0"$/m);
  assert.match(cargo, /serde = \{ version = "1" \}/);
});

test("a file lacking the rewritable text fails naming the target", () => {
  const f = fixture("1.2.3");
  f.put("backend/cmd/riochat/main.go", "package main\n");
  let r = capture({ root: f.root, mode: "backend" });
  assert.equal(r.code, 2);
  assert.match(r.text, /backend\/cmd\/riochat\/main\.go/);
  f.put("desktop/src-tauri/Cargo.toml", "[dependencies]\nversion = \"1\"\n");
  r = capture({ root: f.root, mode: "desktop" });
  assert.equal(r.code, 2);
  assert.match(r.text, /Cargo\.toml/);
});

test("optional shell config is rewritten when present and shared packages are covered", () => {
  const f = fixture("1.2.3", "1.0.0");
  f.put("desktop/src-tauri/tauri.conf.json", `${JSON.stringify({ productName: "x", version: "1.0.0" }, null, 2)}\n`);
  f.put("packages/ui/package.json", `${JSON.stringify({ name: "ui", version: "1.0.0" }, null, 2)}\n`);
  assert.equal(capture({ root: f.root }).code, 0);
  assert.equal(JSON.parse(f.read("desktop/src-tauri/tauri.conf.json")).version, "1.2.3");
  assert.equal(JSON.parse(f.read("packages/ui/package.json")).version, "1.2.3");
});
