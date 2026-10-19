#!/usr/bin/env node
// Uploads SHA256SUMS for a DRAFT release.
//
//   GH_TOKEN=... GH_REPO=owner/name node scripts/checksums.mjs <tag> [--dry-run]
//
// Format: GNU coreutils lines `<64 lowercase hex>  <name>`, sorted by name in
// byte order, newline-terminated, never listing itself.

import { createHash } from "node:crypto";
import { createClient, requireEnv } from "./lib/github.mjs";
import { isMain } from "./lib/util.mjs";

export const FILE_NAME = "SHA256SUMS";

export function sha256Hex(data) {
  return createHash("sha256").update(data).digest("hex");
}

/** Formats `[{ name, digest }]`; validates digests and names. */
export function formatChecksums(entries) {
  const rows = entries.filter((e) => e.name !== FILE_NAME);
  if (rows.length === 0) throw new Error("no assets to hash");
  for (const { name, digest } of rows) {
    if (!/^[0-9a-f]{64}$/.test(digest)) throw new Error(`invalid digest for ${name}`);
    if (/[\r\n\\]/.test(name)) throw new Error(`asset name cannot be listed safely: ${JSON.stringify(name)}`);
  }
  rows.sort((a, b) => Buffer.compare(Buffer.from(a.name, "utf8"), Buffer.from(b.name, "utf8")));
  return rows.map((r) => `${r.digest}  ${r.name}\n`).join("");
}

export async function run({ tag, dryRun, client, log = console.log }) {
  const release = await client.findRelease(tag);
  if (!release) throw new Error(`release ${tag} not found`);
  if (!release.draft && !dryRun) throw new Error(`release ${tag} is already published; refusing to change its assets`);
  const assets = (release.assets ?? []).filter((a) => a.name !== FILE_NAME);
  if (assets.length === 0) throw new Error("no assets to hash");
  const entries = [];
  for (const asset of assets) {
    let data;
    try {
      data = await client.downloadAsset(asset);
    } catch (e) {
      throw new Error(`could not download ${asset.name}: ${e.message}`);
    }
    entries.push({ name: asset.name, digest: sha256Hex(data) });
  }
  const text = formatChecksums(entries);
  if (dryRun) {
    log(text.trimEnd());
    return 0;
  }
  const existing = (release.assets ?? []).find((a) => a.name === FILE_NAME);
  await client.uploadAsset(release, FILE_NAME, Buffer.from(text), "text/plain", existing);
  log(`uploaded ${FILE_NAME} with ${entries.length} entries`);
  return 0;
}

if (isMain(import.meta.url)) {
  try {
    const argv = process.argv.slice(2);
    const dryRun = argv.includes("--dry-run");
    const rest = argv.filter((a) => a !== "--dry-run");
    if (rest.length !== 1 || rest[0].startsWith("--")) throw new Error("usage: checksums.mjs <tag> [--dry-run]");
    process.exit(await run({ tag: rest[0], dryRun, client: createClient(requireEnv()) }));
  } catch (e) {
    console.error(`checksums: ${e.message}`);
    process.exit(1);
  }
}
