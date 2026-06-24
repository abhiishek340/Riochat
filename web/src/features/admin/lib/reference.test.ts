import { beforeEach, describe, expect, it, vi } from "vitest";
import { cachedLoad, invalidateModels, invalidateReference } from "./reference";

beforeEach(() => invalidateReference());

describe("cachedLoad", () => {
  it("shares one request between callers and serves repeats from the cache", async () => {
    const loader = vi.fn(async () => 42);
    const [a, b] = await Promise.all([cachedLoad("k", loader), cachedLoad("k", loader)]);
    expect([a, b]).toEqual([42, 42]);
    await cachedLoad("k", loader);
    expect(loader).toHaveBeenCalledTimes(1);
  });
  it("reloads after the lifetime has passed", async () => {
    let clock = 0;
    const loader = vi.fn(async () => clock);
    await cachedLoad("t", loader, 1000, () => clock);
    clock = 999;
    await cachedLoad("t", loader, 1000, () => clock);
    expect(loader).toHaveBeenCalledTimes(1);
    clock = 1000;
    await cachedLoad("t", loader, 1000, () => clock);
    expect(loader).toHaveBeenCalledTimes(2);
  });
  it("does not remember a failure", async () => {
    const loader = vi.fn().mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce("ok");
    await expect(cachedLoad("f", loader)).rejects.toThrow("boom");
    await expect(cachedLoad("f", loader)).resolves.toBe("ok");
  });
  it("drops model related entries on a model change", async () => {
    const models = vi.fn(async () => 1);
    const other = vi.fn(async () => 2);
    await cachedLoad("models-all", models);
    await cachedLoad("billing-config", other);
    invalidateModels();
    await cachedLoad("models-all", models);
    await cachedLoad("billing-config", other);
    expect(models).toHaveBeenCalledTimes(2);
    expect(other).toHaveBeenCalledTimes(1);
  });
});
