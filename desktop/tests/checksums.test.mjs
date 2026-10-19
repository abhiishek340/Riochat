import assert from "node:assert/strict";
import test from "node:test";
import { formatChecksums, run, sha256Hex } from "../scripts/checksums.mjs";

const EMPTY = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
const ABC = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";

test("known digests", () => {
  assert.equal(sha256Hex(Buffer.alloc(0)), EMPTY);
  assert.equal(sha256Hex(Buffer.from("abc")), ABC);
});

test("GNU coreutils format, byte-order sort, trailing newline", () => {
  const text = formatChecksums([
    { name: "b.zip", digest: ABC },
    { name: "B.zip", digest: EMPTY },
    { name: "a.zip", digest: ABC },
    { name: "SHA256SUMS", digest: ABC },
  ]);
  assert.equal(text, `${EMPTY}  B.zip\n${ABC}  a.zip\n${ABC}  b.zip\n`);
  assert.ok(!text.includes("SHA256SUMS"));
});

test("invalid inputs are errors", () => {
  assert.throws(() => formatChecksums([]), /no assets to hash/);
  assert.throws(() => formatChecksums([{ name: "SHA256SUMS", digest: ABC }]), /no assets to hash/);
  assert.throws(() => formatChecksums([{ name: "a", digest: ABC.toUpperCase() }]), /invalid digest/);
  assert.throws(() => formatChecksums([{ name: "a", digest: "abc" }]), /invalid digest/);
  for (const name of ["a\nb", "a\rb", "a\\b"]) {
    assert.throws(() => formatChecksums([{ name, digest: ABC }]), /cannot be listed safely/);
  }
});

function client({ draft = true, assets, failOn }) {
  const uploads = [];
  return {
    uploads,
    async findRelease() {
      return { id: 1, draft, assets: assets.map((a, i) => ({ id: i + 1, ...a })) };
    },
    async downloadAsset(asset) {
      if (asset.name === failOn) throw new Error("boom");
      return Buffer.from(asset.body);
    },
    async uploadAsset(release, name, data, type, existing) {
      uploads.push({ name, text: data.toString("utf8"), replaced: Boolean(existing) });
    },
  };
}

test("run hashes every asset except SHA256SUMS and uploads the file", async () => {
  const c = client({ assets: [{ name: "b", body: "abc" }, { name: "a", body: "" }, { name: "SHA256SUMS", body: "old" }] });
  await run({ tag: "v1", dryRun: false, client: c, log() {} });
  assert.equal(c.uploads.length, 1);
  assert.equal(c.uploads[0].name, "SHA256SUMS");
  assert.equal(c.uploads[0].text, `${EMPTY}  a\n${ABC}  b\n`);
  assert.equal(c.uploads[0].replaced, true);
});

test("run: published release, failed download, no assets, dry-run", async () => {
  await assert.rejects(run({ tag: "v1", dryRun: false, client: client({ draft: false, assets: [{ name: "a", body: "" }] }), log() {} }), /already published/);
  await assert.rejects(run({ tag: "v1", dryRun: false, client: client({ assets: [{ name: "a", body: "" }], failOn: "a" }), log() {} }), /could not download a/);
  await assert.rejects(run({ tag: "v1", dryRun: false, client: client({ assets: [] }), log() {} }), /no assets to hash/);
  const c = client({ draft: false, assets: [{ name: "a", body: "abc" }] });
  const lines = [];
  await run({ tag: "v1", dryRun: true, client: c, log: (l) => lines.push(l) });
  assert.deepEqual(c.uploads, []);
  assert.equal(lines[0], `${ABC}  a`);
});
