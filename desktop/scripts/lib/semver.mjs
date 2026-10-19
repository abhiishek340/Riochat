// Semantic version parsing and precedence, as the release tooling needs it.

const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

/** Parses `x.y.z[-pre][+build]`; returns null when it is not a semantic version. */
export function parse(text) {
  const m = SEMVER.exec(String(text).trim());
  if (!m) return null;
  return {
    major: Number(m[1]),
    minor: Number(m[2]),
    patch: Number(m[3]),
    pre: m[4] ? m[4].split(".") : [],
    build: m[5] ? m[5].split(".") : [],
  };
}

export function isPrerelease(text) {
  const v = parse(text);
  return v !== null && v.pre.length > 0;
}

const numeric = (s) => /^\d+$/.test(s);

function compareIdentifier(a, b) {
  if (numeric(a) && numeric(b)) return Math.sign(Number(a) - Number(b));
  if (a === b) return 0;
  return a < b ? -1 : 1; // otherwise lexically
}

/**
 * Precedence: the numeric core first; a release outranks a prerelease of the
 * same core; prerelease identifiers compare numerically when both are numeric
 * and lexically otherwise; with equal leading identifiers, fewer sort lower.
 * Returns -1, 0 or 1. Throws for text that is not a semantic version.
 */
export function compare(a, b) {
  const x = parse(a);
  const y = parse(b);
  if (!x) throw new Error(`"${a}" is not a semantic version`);
  if (!y) throw new Error(`"${b}" is not a semantic version`);
  for (const k of ["major", "minor", "patch"]) {
    if (x[k] !== y[k]) return x[k] < y[k] ? -1 : 1;
  }
  if (x.pre.length === 0 && y.pre.length === 0) return 0;
  if (x.pre.length === 0) return 1;
  if (y.pre.length === 0) return -1;
  const n = Math.min(x.pre.length, y.pre.length);
  for (let i = 0; i < n; i++) {
    const c = compareIdentifier(x.pre[i], y.pre[i]);
    if (c !== 0) return c;
  }
  return Math.sign(x.pre.length - y.pre.length);
}

export function isNewer(candidate, current) {
  return compare(candidate, current) > 0;
}
