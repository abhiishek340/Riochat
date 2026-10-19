import assert from "node:assert/strict";
import test from "node:test";
import { planRenames, renameTarget, rewriteManifest, renderManifest, run, versionFromTag, parseArgs } from "../scripts/release-assets.mjs";

const V = "0.4.5";

test("bundler names map to the public scheme", () => {
  const table = {
    "Riochat_0.4.5_aarch64.dmg": "Riochat-0.4.5-macos-arm64.dmg",
    "Riochat_0.4.5_x64.dmg": "Riochat-0.4.5-macos-x64.dmg",
    "Riochat_aarch64.app.tar.gz": "Riochat-0.4.5-macos-arm64-updater.tar.gz",
    "Riochat_aarch64.app.tar.gz.sig": "Riochat-0.4.5-macos-arm64-updater.tar.gz.sig",
    "Riochat_x64.app.tar.gz": "Riochat-0.4.5-macos-x64-updater.tar.gz",
    "Riochat_0.4.5_amd64.AppImage": "Riochat-0.4.5-linux-x64.AppImage",
    "Riochat_0.4.5_amd64.AppImage.sig": "Riochat-0.4.5-linux-x64.AppImage.sig",
    "Riochat_0.4.5_amd64.deb": "Riochat-0.4.5-linux-x64.deb",
    "Riochat_0.4.5_arm64.deb": "Riochat-0.4.5-linux-arm64.deb",
    "Riochat-0.4.5-1.x86_64.rpm": "Riochat-0.4.5-linux-x64.rpm",
    "Riochat-0.4.5-1.aarch64.rpm": "Riochat-0.4.5-linux-arm64.rpm",
    "Riochat_0.4.5_x64-setup.exe": "Riochat-0.4.5-windows-x64-setup.exe",
    "Riochat_0.4.5_x64-setup.exe.sig": "Riochat-0.4.5-windows-x64-setup.exe.sig",
    "Riochat_0.4.5_x64_en-US.msi": "Riochat-0.4.5-windows-x64.msi",
    "Riochat_0.4.5_x64_fr-FR.msi": "Riochat-0.4.5-windows-x64-fr-FR.msi",
    "Riochat_0.4.5_x64_en-US.msi.sig": "Riochat-0.4.5-windows-x64.msi.sig",
  };
  for (const [from, to] of Object.entries(table)) assert.equal(renameTarget(from, V), to, from);
});

test("exempt, renamed, portable and unknown assets are left alone", () => {
  for (const name of [
    "latest.json",
    "SHA256SUMS",
    "Riochat-0.4.5-macos-arm64.dmg",
    "Riochat-0.4.5-windows-x64-setup.exe",
    "Riochat-0.4.5-windows-x64-portable.zip",
    "Riochat-0.4.5-windows-x64-portable-offline.zip",
    "Riochat_0.4.5_riscv64.deb",
    "notes.txt",
  ]) {
    assert.equal(renameTarget(name, V), null, name);
  }
});

test("a plan is validated and resumable", () => {
  const first = planRenames(["Riochat_0.4.5_aarch64.dmg", "Riochat_0.4.5_x64.dmg", "latest.json"], V);
  assert.equal(first.length, 2);
  // after an interruption only what is left is renamed
  const resumed = planRenames(["Riochat-0.4.5-macos-arm64.dmg", "Riochat_0.4.5_x64.dmg", "latest.json"], V);
  assert.deepEqual(resumed, [{ from: "Riochat_0.4.5_x64.dmg", to: "Riochat-0.4.5-macos-x64.dmg" }]);
  assert.deepEqual(planRenames(["Riochat-0.4.5-macos-arm64.dmg"], V), []);
});

test("collisions abort before anything changes", () => {
  assert.throws(
    () => planRenames(["Riochat_0.4.5_x64.dmg", "Riochat_9.9.9_x64.dmg"], V),
    /would both become/,
  );
  assert.throws(
    () => planRenames(["Riochat_0.4.5_aarch64.dmg", "Riochat-0.4.5-macos-arm64.dmg"], V),
    /already exists/,
  );
  // case-only differences count as collisions
  assert.throws(
    () => planRenames(["Riochat_0.4.5_aarch64.dmg", "riochat-0.4.5-MACOS-arm64.dmg"], V),
    /already exists/,
  );
});

test("manifest rewriting is idempotent and touches only the last segment", () => {
  const manifest = {
    version: "0.4.5",
    notes: "x",
    platforms: {
      "darwin-aarch64": { signature: "SIG1", url: "https://github.com/a/b/releases/download/v0.4.5/Riochat_aarch64.app.tar.gz" },
      "windows-x86_64": { signature: "SIG2", url: "https://github.com/a/b/releases/download/v0.4.5/Riochat_0.4.5_x64-setup.exe" },
      "linux-x86_64": { signature: "SIG3", url: "https://github.com/a/b/releases/download/v0.4.5/Riochat_0.4.5_amd64.AppImage" },
      other: { signature: "SIG4", url: "https://example.com/keep/me.zip" },
    },
  };
  const once = rewriteManifest(manifest, V);
  assert.equal(once.platforms["darwin-aarch64"].url, "https://github.com/a/b/releases/download/v0.4.5/Riochat-0.4.5-macos-arm64-updater.tar.gz");
  assert.equal(once.platforms["windows-x86_64"].url.endsWith("/Riochat-0.4.5-windows-x64-setup.exe"), true);
  assert.equal(once.platforms.other.url, "https://example.com/keep/me.zip");
  assert.equal(once.platforms["darwin-aarch64"].signature, "SIG1");
  const twice = rewriteManifest(once, V);
  assert.deepEqual(twice, once);
  assert.equal(renderManifest(once).endsWith("}\n"), true);
  assert.match(renderManifest(once), /^\{\n {2}"version"/);
  // encoded names are decoded before matching
  const encoded = rewriteManifest({ platforms: { p: { url: "https://x/y/Riochat_0.4.5_x64_fr-FR.msi" } } }, V);
  assert.equal(encoded.platforms.p.url, "https://x/y/Riochat-0.4.5-windows-x64-fr-FR.msi");
});

function fakeClient({ draft = true, assets }) {
  const calls = [];
  const state = { assets: assets.map((a, i) => ({ id: i + 1, ...a })) };
  const release = { id: 99, draft, assets: state.assets };
  return {
    calls,
    state,
    async findRelease(tag) {
      return tag === "v0.4.5" ? release : null;
    },
    async renameAsset(asset, name) {
      calls.push(["rename", asset.name, name]);
      asset.name = name;
    },
    async downloadAsset(asset) {
      return Buffer.from(asset.body);
    },
    async uploadAsset(rel, name, data, type, existing) {
      calls.push(["upload", name, existing ? existing.id : null]);
      existing.body = data.toString("utf8");
    },
  };
}

const manifestBody = JSON.stringify({
  version: "0.4.5",
  platforms: { "windows-x86_64": { signature: "s", url: "https://h/dl/Riochat_0.4.5_x64-setup.exe" } },
});

test("run renames draft assets and reconciles the manifest", async () => {
  const client = fakeClient({
    assets: [
      { name: "Riochat_0.4.5_x64-setup.exe", body: "" },
      { name: "latest.json", body: manifestBody },
    ],
  });
  const lines = [];
  assert.equal(await run({ tag: "v0.4.5", dryRun: false, client, log: (l) => lines.push(l) }), 0);
  assert.deepEqual(client.calls, [
    ["rename", "Riochat_0.4.5_x64-setup.exe", "Riochat-0.4.5-windows-x64-setup.exe"],
    ["upload", "latest.json", 2],
  ]);
  // running again changes nothing
  client.calls.length = 0;
  await run({ tag: "v0.4.5", dryRun: false, client, log: () => {} });
  assert.deepEqual(client.calls, []);
});

test("run refuses a published release unless dry-run, and never changes anything in dry-run", async () => {
  const assets = [{ name: "Riochat_0.4.5_x64-setup.exe", body: "" }];
  await assert.rejects(run({ tag: "v0.4.5", dryRun: false, client: fakeClient({ draft: false, assets }), log() {} }), /already published/);
  const client = fakeClient({ draft: false, assets });
  await run({ tag: "v0.4.5", dryRun: true, client, log() {} });
  assert.deepEqual(client.calls, []);
  await assert.rejects(run({ tag: "v9", dryRun: true, client, log() {} }), /not found/);
});

test("run without a manifest succeeds", async () => {
  const lines = [];
  const client = fakeClient({ assets: [{ name: "Riochat_0.4.5_x64-setup.exe", body: "" }] });
  assert.equal(await run({ tag: "v0.4.5", dryRun: false, client, log: (l) => lines.push(l) }), 0);
  assert.ok(lines.some((l) => /no latest\.json/.test(l)));
});

test("tags and arguments", () => {
  assert.equal(versionFromTag("v1.2.3-beta.1"), "1.2.3-beta.1");
  assert.equal(versionFromTag("1.2.3"), "1.2.3");
  assert.deepEqual(parseArgs(["v1.0.0", "--dry-run"]), { tag: "v1.0.0", dryRun: true });
  assert.throws(() => parseArgs([]), /usage/);
  assert.throws(() => parseArgs(["--dry-run"]), /usage/);
});
