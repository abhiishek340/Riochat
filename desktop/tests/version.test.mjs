import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { main, readVersion, reconcile } from "../scripts/version.mjs";

function fixture(version = "1.2.3", carried = "1.2.3") {
  const root = mkdtempSync(path.join(os.tmpdir(), "ver-"));
  const desktop = path.join(root, "desktop");
  mkdirSync(path.join(desktop, "src-tauri"), { recursive: true });
  writeFileSync(path.join(root, "VERSION"), `${version}\n`);
  writeFileSync(path.join(desktop, "package.json"), JSON.stringify({ name: "x", version: carried }, null, 2) + "\n");
  writeFileSync(path.join(desktop, "src-tauri", "tauri.conf.json"), JSON.stringify({ version: carried }, null, 2) + "\n");
  writeFileSync(
    path.join(desktop, "src-tauri", "Cargo.toml"),
    `[package]\nname = "riochat-desktop"\nversion = "${carried}"\n\n[dependencies]\nserde = { version = "1" }\ntauri = "2"\n`,
  );
  writeFileSync(
    path.join(desktop, "src-tauri", "Cargo.lock"),
    `[[package]]\nname = "other"\nversion = "9.9.9"\n\n[[package]]\nname = "riochat-desktop"\nversion = "${carried}"\n`,
  );
  return { root, desktop };
}

test("matching files pass the check", () => {
  const { root, desktop } = fixture();
  assert.equal(main(["--check"], { root, desktop }), 0);
});

test("drift is reported and then synced", () => {
  const { root, desktop } = fixture("1.3.0", "1.2.3");
  const quiet = console.error;
  console.error = () => {};
  try {
    assert.equal(main(["--check"], { root, desktop }), 1);
  } finally {
    console.error = quiet;
  }
  const log = console.log;
  console.log = () => {};
  try {
    assert.equal(main(["--sync"], { root, desktop }), 0);
  } finally {
    console.log = log;
  }
  assert.equal(main(["--check"], { root, desktop }), 0);
  assert.equal(JSON.parse(readFileSync(path.join(desktop, "package.json"), "utf8")).version, "1.3.0");
  const cargo = readFileSync(path.join(desktop, "src-tauri", "Cargo.toml"), "utf8");
  assert.match(cargo, /^version = "1\.3\.0"$/m);
  assert.match(cargo, /serde = \{ version = "1" \}/, "dependency versions are untouched");
  const lock = readFileSync(path.join(desktop, "src-tauri", "Cargo.lock"), "utf8");
  assert.match(lock, /name = "other"\nversion = "9\.9\.9"/);
  assert.match(lock, /name = "riochat-desktop"\nversion = "1\.3\.0"/);
});

test("usage errors and bad VERSION", () => {
  const { root, desktop } = fixture();
  const quiet = console.error;
  console.error = () => {};
  try {
    assert.equal(main([], { root, desktop }), 2);
    assert.equal(main(["--check", "--sync"], { root, desktop }), 2);
  } finally {
    console.error = quiet;
  }
  writeFileSync(path.join(root, "VERSION"), "not-a-version\n");
  assert.throws(() => readVersion(root), /invalid version/);
});

test("CRLF files keep their line endings", () => {
  const { root, desktop } = fixture("2.0.0", "1.0.0");
  const toml = path.join(desktop, "src-tauri", "Cargo.toml");
  writeFileSync(toml, readFileSync(toml, "utf8").replace(/\n/g, "\r\n"));
  reconcile({ root, desktop, write: true });
  const text = readFileSync(toml, "utf8");
  assert.match(text, /version = "2\.0\.0"\r\n/);
  assert.ok(!/[^\r]\n/.test(text));
});

test("the real project is in sync with VERSION", () => {
  assert.equal(reconcile().drifted.length, 0);
});
