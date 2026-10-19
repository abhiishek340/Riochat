#!/usr/bin/env node
// Prints the per-release Tauri configuration override.
//
//   node scripts/release-config.mjs --channel stable|beta [--repository owner/name]
//        [--version x.y.z] [--thumbprint <40 hex>]
//
// Output: exactly one line, `--config <JSON>`, with no whitespace inside the
// JSON, ready to be passed to `tauri build`.

import { readFileSync } from "node:fs";
import { isMain, parseFlags, tauriConfigPath } from "./lib/util.mjs";
import { parse } from "./lib/semver.mjs";

/** Windows Installer product version: `major.minor.patch[.revision]`. */
export function msiVersion(version) {
  const v = parse(version);
  if (!v) throw new Error(`"${version}" is not a semantic version`);
  if (v.major > 255 || v.minor > 255) {
    throw new Error(`the major and minor version must be at most 255 for an MSI (got ${v.major}.${v.minor})`);
  }
  if (v.patch > 65535) throw new Error(`the patch version must be at most 65535 for an MSI (got ${v.patch})`);
  if (v.pre.length === 0) return `${v.major}.${v.minor}.${v.patch}`;
  const numbers = v.pre.filter((id) => /^\d+$/.test(id));
  const revision = numbers.length > 0 ? Number(numbers[numbers.length - 1]) : 0;
  if (revision > 65535) throw new Error(`the prerelease revision must be at most 65535 for an MSI (got ${revision})`);
  return `${v.major}.${v.minor}.${v.patch}.${revision}`;
}

export function betaManifestUrl(repository) {
  return `https://github.com/${repository}/releases/download/desktop-beta/latest.json`;
}

/** Builds the override object (not yet serialized). */
export function buildConfig({ channel, repository, version, thumbprint }) {
  const windows = { wix: { version: msiVersion(version) } };
  const out = { bundle: { windows } };
  if (channel === "beta") {
    if (!repository || !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) {
      throw new Error('the beta channel needs --repository in the form "owner/name"');
    }
    out.plugins = { updater: { endpoints: [betaManifestUrl(repository)] } };
  } else if (channel !== "stable") {
    throw new Error(`unknown channel "${channel}" (expected stable or beta)`);
  }
  if (thumbprint !== undefined) {
    if (!/^[0-9a-fA-F]{40}$/.test(thumbprint)) throw new Error("the thumbprint must be 40 hexadecimal digits");
    windows.certificateThumbprint = thumbprint.toUpperCase();
  }
  return out;
}

export function configVersion(path = tauriConfigPath) {
  return JSON.parse(readFileSync(path, "utf8")).version;
}

export function render(options) {
  return `--config ${JSON.stringify(buildConfig(options))}`;
}

export function main(argv, out = (line) => console.log(line)) {
  const { flags, positional } = parseFlags(argv, {
    channel: "value",
    repository: "value",
    version: "value",
    thumbprint: "value",
  });
  if (positional.length > 0) throw new Error(`unexpected argument ${positional[0]}`);
  if (!flags.channel) throw new Error("--channel stable|beta is required");
  out(
    render({
      channel: flags.channel,
      repository: flags.repository,
      version: flags.version ?? configVersion(),
      thumbprint: flags.thumbprint,
    }),
  );
  return 0;
}

if (isMain(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(e.message);
    process.exit(1);
  }
}
