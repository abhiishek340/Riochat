#!/usr/bin/env node
// Refreshes the rolling `desktop-beta` manifest after a release is built.
//
//   GH_TOKEN=... GH_REPO=owner/name node scripts/refresh-beta-manifest.mjs <tag>
//
// Skips drafts and releases without latest.json. Creates the rolling
// prerelease on first use. Uploads only when the candidate manifest is
// strictly newer (semantic-version precedence) than the one on desktop-beta,
// so beta installs also receive newer stable releases.

import { createClient, requireEnv } from "./lib/github.mjs";
import { isNewer } from "./lib/semver.mjs";
import { isMain } from "./lib/util.mjs";

export const BETA_TAG = "desktop-beta";
export const BETA_TITLE = "Desktop beta channel";
const MANIFEST = "latest.json";

export async function run({ tag, client, log = console.log }) {
  const release = await client.findRelease(tag);
  if (!release) throw new Error(`release ${tag} not found`);
  if (release.draft) {
    log(`${tag} is a draft; skipping`);
    return 0;
  }
  const candidateAsset = (release.assets ?? []).find((a) => a.name === MANIFEST);
  if (!candidateAsset) {
    log(`${tag} has no ${MANIFEST}; skipping`);
    return 0;
  }
  const candidateBytes = await client.downloadAsset(candidateAsset);
  const candidate = JSON.parse(candidateBytes.toString("utf8"));
  if (typeof candidate.version !== "string") throw new Error(`${MANIFEST} of ${tag} has no version`);

  let beta = await client.findRelease(BETA_TAG);
  if (!beta) {
    beta = await client.createRelease({
      tag_name: BETA_TAG,
      name: BETA_TITLE,
      body: "Rolling manifest for the desktop beta channel. Do not delete.",
      prerelease: true,
      draft: false,
    });
    log(`created the ${BETA_TAG} release`);
  }
  const current = (beta.assets ?? []).find((a) => a.name === MANIFEST);
  if (current) {
    const currentManifest = JSON.parse((await client.downloadAsset(current)).toString("utf8"));
    if (typeof currentManifest.version === "string" && !isNewer(candidate.version, currentManifest.version)) {
      log(`${BETA_TAG} already has ${currentManifest.version}; ${candidate.version} is not newer`);
      return 0;
    }
  }
  await client.uploadAsset(beta, MANIFEST, candidateBytes, "application/json", current);
  log(`${BETA_TAG} now serves ${candidate.version}`);
  return 0;
}

if (isMain(import.meta.url)) {
  try {
    const argv = process.argv.slice(2);
    if (argv.length !== 1 || argv[0].startsWith("--")) throw new Error("usage: refresh-beta-manifest.mjs <tag>");
    process.exit(await run({ tag: argv[0], client: createClient(requireEnv()) }));
  } catch (e) {
    console.error(`refresh-beta-manifest: ${e.message}`);
    process.exit(1);
  }
}
