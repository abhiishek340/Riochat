import assert from "node:assert/strict";
import test from "node:test";
import { compare, isNewer, isPrerelease, parse } from "../scripts/lib/semver.mjs";
import { run as refreshBeta, BETA_TAG, BETA_TITLE } from "../scripts/refresh-beta-manifest.mjs";
import { requireEnv } from "../scripts/lib/github.mjs";

test("semver precedence", () => {
  const order = [
    "1.0.0-alpha",
    "1.0.0-alpha.1",
    "1.0.0-alpha.beta",
    "1.0.0-beta",
    "1.0.0-beta.2",
    "1.0.0-beta.11",
    "1.0.0-rc.1",
    "1.0.0",
    "1.0.1",
    "1.1.0",
    "2.0.0",
  ];
  for (let i = 0; i < order.length - 1; i++) {
    assert.equal(compare(order[i], order[i + 1]), -1, `${order[i]} < ${order[i + 1]}`);
    assert.equal(compare(order[i + 1], order[i]), 1);
  }
  assert.equal(compare("1.0.0+a", "1.0.0+b"), 0);
  assert.equal(compare("0.4.10", "0.4.9"), 1);
  assert.throws(() => compare("1.0", "1.0.0"), /not a semantic version/);
  assert.equal(isPrerelease("1.0.0-beta.1"), true);
  assert.equal(isPrerelease("1.0.0"), false);
  assert.equal(parse("x"), null);
  assert.equal(isNewer("1.0.0", "1.0.0-rc.1"), true);
  assert.equal(isNewer("1.0.0", "1.0.0"), false);
});

function fakeGithub({ tag = { draft: false, assets: [] }, beta = null }) {
  const created = [];
  const uploads = [];
  const releases = { "v1.0.0": tag };
  if (beta) releases[BETA_TAG] = beta;
  return {
    created,
    uploads,
    async findRelease(t) {
      const r = releases[t];
      return r ? { id: t === BETA_TAG ? 2 : 1, ...r, assets: (r.assets ?? []).map((a, i) => ({ id: i + 1, ...a })) } : null;
    },
    async createRelease(fields) {
      created.push(fields);
      releases[BETA_TAG] = { draft: false, assets: [] };
      return { id: 2, assets: [] };
    },
    async downloadAsset(asset) {
      return Buffer.from(asset.body);
    },
    async uploadAsset(release, name, data, type, existing) {
      uploads.push({ release: release.id, name, version: JSON.parse(data.toString()).version, replaced: Boolean(existing) });
    },
  };
}

const manifest = (v) => JSON.stringify({ version: v, platforms: {} });

test("the beta manifest is created and filled on first use", async () => {
  const g = fakeGithub({ tag: { assets: [{ name: "latest.json", body: manifest("1.0.0-beta.1") }] } });
  await refreshBeta({ tag: "v1.0.0", client: g, log() {} });
  assert.equal(g.created.length, 1);
  assert.equal(g.created[0].tag_name, BETA_TAG);
  assert.equal(g.created[0].name, BETA_TITLE);
  assert.equal(g.created[0].prerelease, true);
  assert.deepEqual(g.uploads, [{ release: 2, name: "latest.json", version: "1.0.0-beta.1", replaced: false }]);
});

test("only a strictly newer manifest replaces the current one", async () => {
  const run = async (candidate, current) => {
    const g = fakeGithub({
      tag: { assets: [{ name: "latest.json", body: manifest(candidate) }] },
      beta: { assets: [{ name: "latest.json", body: manifest(current) }] },
    });
    await refreshBeta({ tag: "v1.0.0", client: g, log() {} });
    return g.uploads;
  };
  assert.equal((await run("1.0.0-beta.2", "1.0.0-beta.1")).length, 1);
  assert.equal((await run("1.0.0", "1.0.0-beta.9")).length, 1, "a stable release outranks its betas");
  assert.equal((await run("1.0.0-beta.1", "1.0.0-beta.1")).length, 0);
  assert.equal((await run("1.0.0-beta.1", "1.0.0")).length, 0);
  assert.equal((await run("1.0.0-beta.2", "1.0.0-beta.10")).length, 0);
  const replaced = await run("2.0.0", "1.0.0");
  assert.equal(replaced[0].replaced, true);
});

test("drafts and releases without a manifest are skipped", async () => {
  const draft = fakeGithub({ tag: { draft: true, assets: [{ name: "latest.json", body: manifest("1.0.0") }] } });
  await refreshBeta({ tag: "v1.0.0", client: draft, log() {} });
  assert.deepEqual(draft.uploads, []);
  assert.deepEqual(draft.created, []);
  const none = fakeGithub({ tag: { assets: [] } });
  await refreshBeta({ tag: "v1.0.0", client: none, log() {} });
  assert.deepEqual(none.uploads, []);
  await assert.rejects(refreshBeta({ tag: "v9", client: none, log() {} }), /not found/);
});

test("the environment is required", () => {
  assert.throws(() => requireEnv({}), /GH_TOKEN/);
  assert.throws(() => requireEnv({ GH_TOKEN: "t" }), /GH_REPO/);
  assert.throws(() => requireEnv({ GH_TOKEN: "t", GH_REPO: "bad" }), /GH_REPO/);
  assert.deepEqual(requireEnv({ GH_TOKEN: "t", GH_REPO: "a/b" }), { token: "t", repo: "a/b" });
});
