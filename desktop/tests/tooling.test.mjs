import assert from "node:assert/strict";
import { mkdtempSync, readFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fastEnv, tauriArgs, updaterOverride } from "../scripts/build-fast.mjs";
import { devArgs } from "../scripts/dev.mjs";
import { EXCLUDED_LIBRARIES, cacheDir, renderWrapper, tools } from "../scripts/prepare-appimage.mjs";
import { BACKGROUND, INK, buildIcns, buildIco, crc32, encodePng, generate, renderApp, renderGlyph } from "../scripts/gen-icons.mjs";
import { parseFlags } from "../scripts/lib/util.mjs";

test("fast builds relax optimization and bundle only the app", () => {
  const env = fastEnv({ PATH: "/bin" });
  assert.equal(env.CARGO_PROFILE_RELEASE_LTO, "thin");
  assert.equal(env.CARGO_PROFILE_RELEASE_CODEGEN_UNITS, "16");
  assert.equal(env.CARGO_PROFILE_RELEASE_OPT_LEVEL, "2");
  assert.equal(env.PATH, "/bin");
  assert.deepEqual(tauriArgs([], { TAURI_SIGNING_PRIVATE_KEY: "k" }), ["build", "--bundles", "app"]);
});

test("a fast build without an updater key switches updater artifacts off", () => {
  assert.deepEqual(updaterOverride({ TAURI_SIGNING_PRIVATE_KEY_PATH: "/k" }), []);
  const args = tauriArgs([], {});
  assert.deepEqual(args.slice(0, 3), ["build", "--bundles", "app"]);
  assert.equal(args[3], "--config");
  assert.deepEqual(JSON.parse(args[4]), { bundle: { createUpdaterArtifacts: false } });
});

test("dev uses the dev configuration", () => {
  const args = devArgs();
  assert.equal(args[0], "dev");
  assert.equal(args[1], "--config");
  assert.ok(args[2].endsWith("tauri.dev.conf.json"));
});

test("dev configuration separates identity and data", () => {
  const dev = JSON.parse(readFileSync(new URL("../src-tauri/tauri.dev.conf.json", import.meta.url), "utf8"));
  const base = JSON.parse(readFileSync(new URL("../src-tauri/tauri.conf.json", import.meta.url), "utf8"));
  assert.equal(dev.identifier, "com.riochat.desktop.dev");
  assert.equal(dev.productName, "Riochat Dev");
  assert.notEqual(dev.identifier, base.identifier);
  assert.equal(dev.build.beforeDevCommand, "node scripts/dev-server.mjs");
});

test("AppImage wrapper excludes the Wayland libraries", () => {
  assert.deepEqual(EXCLUDED_LIBRARIES, ["libwayland-client.so.0", "libwayland-cursor.so.0", "libwayland-egl.so.1", "libwayland-server.so.0"]);
  const script = renderWrapper({ realPath: "/c/linuxdeploy-x86_64.real.AppImage" });
  assert.match(script, /^#!\/bin\/sh/);
  for (const lib of EXCLUDED_LIBRARIES) assert.ok(script.includes(`--exclude-library "${lib}"`), lib);
  assert.match(script, /LINUXDEPLOY_DEBUG_LOG/);
  assert.match(script, /export LINUXDEPLOY=/);
  assert.match(script, /exec "\$REAL"/);
  assert.ok(renderWrapper({ realPath: "/it's/x" }).includes("REAL='/it'\\''s/x'"));
  assert.ok(tools("aarch64").some((t) => t.file === "linuxdeploy-aarch64.AppImage"));
  assert.equal(cacheDir({ XDG_CACHE_HOME: "/xdg" }), path.join("/xdg", "tauri"));
  assert.equal(cacheDir({}, "/home/u"), path.join("/home/u", ".cache", "tauri"));
});

test("flag parsing", () => {
  assert.deepEqual(parseFlags(["--a", "1", "--b", "pos"], { a: "value", b: "value" }), { flags: { a: "1", b: "pos" }, positional: [] });
  assert.deepEqual(parseFlags(["x", "--f"], { f: "boolean" }), { flags: { f: true }, positional: ["x"] });
  assert.throws(() => parseFlags(["--zzz"], {}), /unknown argument/);
});

test("icons are valid containers", () => {
  const png = encodePng(2, 2, Buffer.alloc(16, 255));
  assert.deepEqual([...png.subarray(0, 8)], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  assert.equal(crc32(Buffer.from("123456789")), 0xcbf43926);
  const app = renderApp(64);
  assert.equal(app.readUInt32BE(16), 64);
  assert.equal(renderGlyph(44).readUInt32BE(16), 44);
  const ico = buildIco([{ size: 16, png: renderApp(16) }, { size: 256, png: renderApp(32) }]);
  assert.equal(ico.readUInt16LE(2), 1);
  assert.equal(ico.readUInt16LE(4), 2);
  assert.equal(ico[6 + 16], 0, "256 is stored as 0");
  const icns = buildIcns([{ type: "ic07", png: renderApp(16) }]);
  assert.equal(icns.toString("ascii", 0, 4), "icns");
  assert.equal(icns.readUInt32BE(4), icns.length);
  assert.deepEqual(BACKGROUND, [0xf8, 0xf8, 0xf6]);
  assert.deepEqual(INK, [0x3d, 0x39, 0x29]);
});

test("the full icon set is generated", () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "icons-"));
  const names = generate(dir);
  assert.deepEqual(names.sort(), ["128x128.png", "128x128@2x.png", "32x32.png", "icon.icns", "icon.ico", "icon.png", "source.png", "tray.png"]);
  assert.equal(readFileSync(path.join(dir, "source.png")).readUInt32BE(16), 1024);
  assert.equal(readFileSync(path.join(dir, "tray.png")).readUInt32BE(16), 44);
});
