#!/usr/bin/env node
// Prints the signing identity of an Apple .p12 certificate.
//
//   APPLE_CERTIFICATE=<base64> | APPLE_CERTIFICATE_PATH=<file>
//   APPLE_CERTIFICATE_PASSWORD=<password>   node scripts/apple-identity.mjs
//
// Output: APPLE_SIGNING_IDENTITY=<CN> and APPLE_TEAM_ID=<OU>. Exits 0 printing
// nothing when there is no certificate; exits 1 when CN or OU cannot be read.

import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { isMain } from "./lib/util.mjs";

/** Splits an RFC 2253 distinguished name on unescaped commas. */
export function parseSubject(subject) {
  const parts = [];
  let cur = "";
  for (let i = 0; i < subject.length; i++) {
    const c = subject[i];
    if (c === "\\" && i + 1 < subject.length) {
      cur += subject[i + 1];
      i++;
    } else if (c === ",") {
      parts.push(cur);
      cur = "";
    } else {
      cur += c;
    }
  }
  parts.push(cur);
  const out = {};
  for (const p of parts) {
    const eq = p.indexOf("=");
    if (eq < 0) continue;
    const key = p.slice(0, eq).trim().toUpperCase();
    if (!(key in out)) out[key] = p.slice(eq + 1).trim();
  }
  return out;
}

/** Extracts `{ identity, teamId }` from an openssl subject line. */
export function identityFromSubject(line) {
  const subject = line.replace(/^subject\s*=\s*/i, "").trim();
  const dn = parseSubject(subject);
  if (!dn.CN || !dn.OU) throw new Error("cannot read the CN and OU from the certificate subject");
  return { identity: dn.CN, teamId: dn.OU };
}

function openssl(args, input) {
  return spawnSync("openssl", args, { encoding: "utf8", input });
}

function readSubject(p12Path, password) {
  const base = ["pkcs12", "-in", p12Path, "-nokeys", "-clcerts", "-passin", "env:APPLE_P12_PASSWORD"];
  const env = { ...process.env, APPLE_P12_PASSWORD: password };
  for (const extra of [[], ["-legacy"]]) {
    const pem = spawnSync("openssl", [...base, ...extra], { encoding: "utf8", env });
    if (pem.status !== 0 || !pem.stdout.includes("BEGIN CERTIFICATE")) continue;
    const subject = openssl(["x509", "-noout", "-subject", "-nameopt", "RFC2253"], pem.stdout);
    if (subject.status === 0) return subject.stdout.trim();
  }
  throw new Error("cannot read the certificate (wrong password or unsupported format)");
}

export function main(env = process.env, out = (s) => console.log(s)) {
  let dir;
  try {
    let file = env.APPLE_CERTIFICATE_PATH;
    if (env.APPLE_CERTIFICATE) {
      dir = mkdtempSync(path.join(os.tmpdir(), "riochat-cert-"));
      file = path.join(dir, "certificate.p12");
      writeFileSync(file, Buffer.from(env.APPLE_CERTIFICATE, "base64"), { mode: 0o600 });
    }
    if (!file) return 0; // no certificate: print nothing
    readFileSync(file); // fail early with a clear error when the path is wrong
    const { identity, teamId } = identityFromSubject(readSubject(file, env.APPLE_CERTIFICATE_PASSWORD ?? ""));
    out(`APPLE_SIGNING_IDENTITY=${identity}`);
    out(`APPLE_TEAM_ID=${teamId}`);
    return 0;
  } catch (e) {
    console.error(`apple-identity: ${e.message}`);
    return 1;
  } finally {
    if (dir) rmSync(dir, { recursive: true, force: true });
  }
}

if (isMain(import.meta.url)) process.exit(main());
