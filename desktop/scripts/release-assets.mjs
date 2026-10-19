#!/usr/bin/env node
// Renames the assets of a DRAFT release to the public naming scheme and keeps
// the updater manifest (latest.json) pointing at the new names.
//
//   GH_TOKEN=... GH_REPO=owner/name node scripts/release-assets.mjs <tag> [--dry-run]
//
// Names: Riochat-<version>-<os>-<arch>[-setup|-updater].<ext>, where version
// is the tag without a leading "v". A ".sig" suffix is preserved.

import { createClient, requireEnv } from "./lib/github.mjs";
import { isMain } from "./lib/util.mjs";

const EXEMPT = new Set(["latest.json", "SHA256SUMS"]);

/** aarch64/arm64 -> arm64; x64/x86_64/amd64 -> x64; anything else -> null. */
export function archLabel(arch) {
  switch (arch) {
    case "aarch64":
    case "arm64":
      return "arm64";
    case "x64":
    case "x86_64":
    case "amd64":
      return "x64";
    default:
      return null;
  }
}

/**
 * The public name for a bundler-shaped asset name, or null when the asset is
 * left alone (exempt files, already renamed, portable zips, unknown shapes).
 */
export function renameTarget(name, version) {
  if (EXEMPT.has(name)) return null;
  const prefix = `Riochat-${version}-`;
  // already renamed (the public scheme always names an os) or a portable zip
  if (/^Riochat-.+-(macos|linux|windows)-/.test(name) || /portable/i.test(name)) return null;
  const sig = name.endsWith(".sig") ? ".sig" : "";
  const base = sig ? name.slice(0, -4) : name;
  const out = (tail) => `${prefix}${tail}${sig}`;
  let m;
  if ((m = /^Riochat_(.+)_([A-Za-z0-9]+)\.dmg$/.exec(base))) {
    const arch = archLabel(m[2]);
    return arch ? out(`macos-${arch}.dmg`) : null;
  }
  if ((m = /^Riochat_([A-Za-z0-9]+)\.app\.tar\.gz$/.exec(base))) {
    const arch = archLabel(m[1]);
    return arch ? out(`macos-${arch}-updater.tar.gz`) : null;
  }
  if ((m = /^Riochat_(.+)_([A-Za-z0-9]+)\.(AppImage|deb)$/.exec(base))) {
    const arch = archLabel(m[2]);
    return arch ? out(`linux-${arch}.${m[3]}`) : null;
  }
  if ((m = /^Riochat-(.+)-\d+\.([A-Za-z0-9_]+)\.rpm$/.exec(base))) {
    const arch = archLabel(m[2]);
    return arch ? out(`linux-${arch}.rpm`) : null;
  }
  if ((m = /^Riochat_(.+)_([A-Za-z0-9]+)-setup\.exe$/.exec(base))) {
    const arch = archLabel(m[2]);
    return arch ? out(`windows-${arch}-setup.exe`) : null;
  }
  if ((m = /^Riochat_(.+)_([A-Za-z0-9]+)_([A-Za-z]{2,3}-[A-Za-z0-9]+)\.msi$/.exec(base))) {
    const arch = archLabel(m[2]);
    if (!arch) return null;
    return m[3] === "en-US" ? out(`windows-${arch}.msi`) : out(`windows-${arch}-${m[3]}.msi`);
  }
  return null;
}

export function versionFromTag(tag) {
  return tag.replace(/^v/, "");
}

/**
 * Plans the renames for a list of asset names. Validates before anything
 * changes: two assets mapping to the same name, or a target colliding with an
 * untouched asset (case-insensitively), is an error.
 */
export function planRenames(names, version) {
  const renames = [];
  const untouched = [];
  for (const name of names) {
    const to = renameTarget(name, version);
    if (to && to !== name) renames.push({ from: name, to });
    else untouched.push(name);
  }
  const claimed = new Map(); // lower-case target -> source
  for (const r of renames) {
    const key = r.to.toLowerCase();
    if (claimed.has(key)) {
      throw new Error(`"${claimed.get(key)}" and "${r.from}" would both become "${r.to}"`);
    }
    claimed.set(key, r.from);
  }
  for (const name of untouched) {
    const key = name.toLowerCase();
    if (claimed.has(key)) {
      throw new Error(`cannot rename "${claimed.get(key)}" to "${name}": that name already exists`);
    }
  }
  return renames;
}

/** Rewrites the bundler-shaped file name in each platform URL. Idempotent. */
export function rewriteManifest(manifest, version) {
  const next = JSON.parse(JSON.stringify(manifest));
  for (const platform of Object.values(next.platforms ?? {})) {
    if (!platform || typeof platform.url !== "string") continue;
    const cut = platform.url.lastIndexOf("/");
    const last = platform.url.slice(cut + 1);
    let decoded;
    try {
      decoded = decodeURIComponent(last);
    } catch {
      continue;
    }
    const to = renameTarget(decoded, version);
    if (to) platform.url = `${platform.url.slice(0, cut + 1)}${encodeURIComponent(to)}`;
  }
  return next;
}

export function renderManifest(manifest) {
  return `${JSON.stringify(manifest, null, 2)}\n`;
}

export async function run({ tag, dryRun, client, log = console.log }) {
  const version = versionFromTag(tag);
  const release = await client.findRelease(tag);
  if (!release) throw new Error(`release ${tag} not found`);
  if (!release.draft && !dryRun) throw new Error(`release ${tag} is already published; refusing to rename its assets`);
  const assets = release.assets ?? [];
  const renames = planRenames(assets.map((a) => a.name), version);
  for (const r of renames) {
    log(`${dryRun ? "would rename" : "rename"} ${r.from} -> ${r.to}`);
    if (!dryRun) await client.renameAsset(assets.find((a) => a.name === r.from), r.to);
  }
  if (renames.length === 0) log("no assets to rename");

  const manifestAsset = assets.find((a) => a.name === "latest.json");
  if (!manifestAsset) {
    log("no latest.json in this release; nothing to reconcile");
    return 0;
  }
  const original = (await client.downloadAsset(manifestAsset)).toString("utf8");
  const updated = renderManifest(rewriteManifest(JSON.parse(original), version));
  if (updated === original) {
    log("latest.json is already up to date");
    return 0;
  }
  log(`${dryRun ? "would update" : "update"} latest.json`);
  if (!dryRun) await client.uploadAsset(release, "latest.json", Buffer.from(updated), "application/json", manifestAsset);
  return 0;
}

export function parseArgs(argv) {
  const dryRun = argv.includes("--dry-run");
  const rest = argv.filter((a) => a !== "--dry-run");
  if (rest.length !== 1 || rest[0].startsWith("--")) throw new Error("usage: release-assets.mjs <tag> [--dry-run]");
  return { tag: rest[0], dryRun };
}

if (isMain(import.meta.url)) {
  try {
    const { tag, dryRun } = parseArgs(process.argv.slice(2));
    const client = createClient(requireEnv());
    process.exit(await run({ tag, dryRun, client }));
  } catch (e) {
    console.error(`release-assets: ${e.message}`);
    process.exit(1);
  }
}
