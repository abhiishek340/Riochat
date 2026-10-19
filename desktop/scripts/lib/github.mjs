// A minimal GitHub release client (REST), injectable for tests.

const API = "https://api.github.com";
const UPLOADS = "https://uploads.github.com";

export function requireEnv(env = process.env) {
  const token = env.GH_TOKEN;
  const repo = env.GH_REPO;
  if (!token) throw new Error("GH_TOKEN is required");
  if (!repo || !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repo)) throw new Error("GH_REPO=owner/name is required");
  return { token, repo };
}

export function createClient({ token, repo, fetchImpl = globalThis.fetch }) {
  const headers = (extra = {}) => ({
    Authorization: `Bearer ${token}`,
    Accept: "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
    "User-Agent": "riochat-desktop-release",
    ...extra,
  });

  async function call(method, url, { body, headers: extra, raw } = {}) {
    const res = await fetchImpl(url, { method, headers: headers(extra), body, redirect: "follow" });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(`${method} ${url} failed: ${res.status} ${text.slice(0, 200)}`);
    }
    if (raw) return res;
    if (res.status === 204) return null;
    return res.json();
  }

  return {
    repo,
    /** Finds a release (draft or published) by tag; null when absent. */
    async findRelease(tag) {
      for (let page = 1; page < 50; page++) {
        const list = await call("GET", `${API}/repos/${repo}/releases?per_page=100&page=${page}`);
        const hit = list.find((r) => r.tag_name === tag);
        if (hit) return hit;
        if (list.length < 100) return null;
      }
      return null;
    },
    async createRelease(fields) {
      return call("POST", `${API}/repos/${repo}/releases`, {
        body: JSON.stringify(fields),
        headers: { "Content-Type": "application/json" },
      });
    },
    async renameAsset(asset, name) {
      return call("PATCH", `${API}/repos/${repo}/releases/assets/${asset.id}`, {
        body: JSON.stringify({ name }),
        headers: { "Content-Type": "application/json" },
      });
    },
    async deleteAsset(asset) {
      return call("DELETE", `${API}/repos/${repo}/releases/assets/${asset.id}`);
    },
    async downloadAsset(asset) {
      const res = await call("GET", `${API}/repos/${repo}/releases/assets/${asset.id}`, {
        headers: { Accept: "application/octet-stream" },
        raw: true,
      });
      return Buffer.from(await res.arrayBuffer());
    },
    /** Uploads an asset, replacing one with the same name. */
    async uploadAsset(release, name, data, contentType = "application/octet-stream", existing) {
      if (existing) await this.deleteAsset(existing);
      const url = `${UPLOADS}/repos/${repo}/releases/${release.id}/assets?name=${encodeURIComponent(name)}`;
      return call("POST", url, { body: data, headers: { "Content-Type": contentType } });
    },
  };
}
