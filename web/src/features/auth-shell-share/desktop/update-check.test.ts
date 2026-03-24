import { describe, expect, it, vi } from "vitest";
import { FALLBACK_DISTRIBUTION, type Distribution } from "./distribution";
import { createUpdateChecker, type UpdateSource } from "./update-check";

const dist = (updateMode: Distribution["updateMode"]): Distribution => ({ ...FALLBACK_DISTRIBUTION, updateMode, releasesUrl: "https://example.com/r" });
const source = (over: Partial<UpdateSource> = {}): UpdateSource => ({
  checkInstallable: vi.fn(async () => ({ rid: 7, version: "1.2.0" })),
  checkNotice: vi.fn(async () => ({ version: "1.3.0" })),
  ...over,
});

describe("createUpdateChecker", () => {
  it("offers an install in auto mode and never the same version twice", async () => {
    const check = createUpdateChecker(source());
    expect(await check(dist("auto"))).toEqual({ kind: "install", version: "1.2.0", rid: 7 });
    expect(await check(dist("auto"))).toBeNull();
  });
  it("points portable builds at the releases page", async () => {
    const check = createUpdateChecker(source());
    expect(await check(dist("notify"))).toEqual({ kind: "download", version: "1.3.0", releasesUrl: "https://example.com/r" });
    expect(await check(dist("notify"))).toBeNull();
  });
  it("says nothing when disabled, up to date or unreachable", async () => {
    const s = source();
    const check = createUpdateChecker(s);
    expect(await check(dist("disabled"))).toBeNull();
    expect(s.checkInstallable).not.toHaveBeenCalled();
    const none = createUpdateChecker(source({ checkInstallable: async () => null }));
    expect(await none(dist("auto"))).toBeNull();
    const broken = createUpdateChecker(
      source({
        checkInstallable: async () => {
          throw new Error("offline");
        },
      }),
    );
    expect(await broken(dist("auto"))).toBeNull();
  });
  it("announces a newer version after an older one", async () => {
    let version = "1.2.0";
    const check = createUpdateChecker(source({ checkInstallable: async () => ({ rid: 1, version }) }));
    expect(await check(dist("auto"))).toMatchObject({ version: "1.2.0" });
    version = "1.2.1";
    expect(await check(dist("auto"))).toMatchObject({ version: "1.2.1" });
  });
});
