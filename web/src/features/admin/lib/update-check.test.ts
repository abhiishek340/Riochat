import { describe, expect, it } from "vitest";
import { checkLatestRelease, compareVersions, isUpdateAvailable, parseVersion, readRelease, releasePageUrl } from "./update-check";

function reply(body: unknown, init: { ok?: boolean } = {}): typeof fetch {
  return (async () => ({ ok: init.ok ?? true, json: async () => body })) as unknown as typeof fetch;
}

describe("version comparison", () => {
  it("reads the first numeric run and pads missing parts", () => {
    expect(parseVersion("v1.2.3-beta")).toEqual([1, 2, 3]);
    expect(parseVersion("2")).toEqual([2, 0, 0]);
    expect(parseVersion("release 4.5")).toEqual([4, 5, 0]);
    expect(parseVersion("nightly")).toBeNull();
  });
  it("compares numerically, not as text", () => {
    expect(compareVersions("0.4.5", "v0.5.0")).toBe(-1);
    expect(compareVersions("0.10.0", "0.9.9")).toBe(1);
    expect(compareVersions("v1", "1.0.0")).toBe(0);
    expect(compareVersions("dev", "1.0.0")).toBeNull();
  });
  it("treats an unknown comparison as an available update", () => {
    expect(isUpdateAvailable("dev", "1.0.0")).toBe(true);
    expect(isUpdateAvailable("1.0.0", "1.0.0")).toBe(false);
    expect(isUpdateAvailable("1.1.0", "1.0.0")).toBe(false);
  });
  it("builds the release page address", () => {
    expect(releasePageUrl("0.5.0")).toBe("https://github.com/riochat/riochat/releases/tag/v0.5.0");
    expect(releasePageUrl("v0.5.0+x")).toBe("https://github.com/riochat/riochat/releases/tag/v0.5.0%2Bx");
  });
});

describe("readRelease", () => {
  it("rejects malformed payloads", () => {
    expect(readRelease(null)).toBeNull();
    expect(readRelease({ tag_name: 3, html_url: "x" })).toBeNull();
    expect(readRelease({ tag_name: " ", html_url: "x" })).toBeNull();
    expect(readRelease({ tag_name: "v1", html_url: "https://x" })).toEqual({ version: "v1", url: "https://x" });
  });
});

describe("checkLatestRelease", () => {
  it("reports an available update", async () => {
    const out = await checkLatestRelease("0.4.5", reply({ tag_name: "v0.5.0", html_url: "https://example.test/r" }));
    expect(out).toEqual({ state: "available", latest: { version: "v0.5.0", url: "https://example.test/r" } });
  });
  it("reports the current release", async () => {
    const out = await checkLatestRelease("0.5.0", reply({ tag_name: "v0.5.0", html_url: "https://example.test/r" }));
    expect(out.state).toBe("current");
  });
  it("fails on bad status, bad body and network errors", async () => {
    expect((await checkLatestRelease("1", reply({}, { ok: false }))).state).toBe("failed");
    expect((await checkLatestRelease("1", reply({ tag_name: "v2" }))).state).toBe("failed");
    const boom = (async () => {
      throw new Error("offline");
    }) as unknown as typeof fetch;
    expect((await checkLatestRelease("1", boom)).state).toBe("failed");
  });
});
