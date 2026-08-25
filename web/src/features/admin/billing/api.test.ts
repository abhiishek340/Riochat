import { beforeEach, describe, expect, it, vi } from "vitest";

// The server wraps a redemption code in `{ code: {...} }` for both the
// plaintext reveal and the edit response.
const calls = vi.hoisted(() => ({ list: [] as { method: string; path: string; body?: unknown }[], reply: undefined as unknown }));
vi.mock("@/lib/api/client", () => {
  const record = (method: string) => async (path: string, body?: unknown) => {
    calls.list.push({ method, path, body });
    return calls.reply;
  };
  return { api: { get: record("GET"), post: record("POST"), put: record("PUT"), patch: record("PATCH"), delete: record("DELETE") } };
});

import { patchCode, revealCode } from "./api";

const code = { id: 7, code: "ABCD-EFGH", codeHint: "ABCD***EFGH", mode: "usage", status: "active", description: "x" };

describe("redemption code responses", () => {
  beforeEach(() => {
    calls.list = [];
    calls.reply = undefined;
  });

  it("reads the plaintext from data.code.code", async () => {
    calls.reply = { code };
    await expect(revealCode(7)).resolves.toBe("ABCD-EFGH");
    expect(calls.list[0]).toMatchObject({ method: "GET", path: "/admin/billing/redemption-codes/7/code" });
  });

  it("gives an empty plaintext when the reply has none", async () => {
    calls.reply = {};
    await expect(revealCode(7)).resolves.toBe("");
  });

  it("returns the updated code from data.code", async () => {
    calls.reply = { code: { ...code, status: "inactive" } };
    const out = await patchCode(7, { status: "inactive" });
    expect(out.status).toBe("inactive");
    expect(out.id).toBe(7);
    expect(calls.list[0]).toMatchObject({ method: "PATCH", path: "/admin/billing/redemption-codes/7", body: { status: "inactive" } });
  });
});
