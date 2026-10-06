import { beforeEach, describe, expect, it, vi } from "vitest";

// Records every call the settings API wrappers make, so the paths and bodies
// can be compared with what the backend's handlers bind.
const calls = vi.hoisted(() => ({ list: [] as { method: string; path: string; body?: unknown; options?: unknown }[], reply: undefined as unknown }));
vi.mock("@/lib/api/client", () => {
  const record = (method: string) => async (path: string, a?: unknown, b?: unknown) => {
    const hasBody = method === "POST" || method === "PUT" || method === "PATCH";
    calls.list.push({ method, path, body: hasBody ? a : undefined, options: hasBody ? b : a });
    return calls.reply;
  };
  return { api: { get: record("GET"), post: record("POST"), put: record("PUT"), patch: record("PATCH"), delete: record("DELETE") } };
});

import * as account from "./account/api";
import * as memory from "./memory/api";
import * as models from "./models/api";
import * as billing from "./subscription/api";
import { patchProfile, patchUsername, uploadAvatar } from "./api";

const last = () => calls.list[calls.list.length - 1] as (typeof calls.list)[number];

beforeEach(() => {
  calls.list.length = 0;
  calls.reply = {};
});

describe("profile wire format", () => {
  it("patches /me and /me/username with the field names the server binds", async () => {
    calls.reply = { user: { username: "ada" } };
    await patchProfile({ avatarURL: "", displayName: "Ada", timezone: "Etc/UTC", locale: "en-US", profilePreferences: "" });
    expect(last()).toMatchObject({ method: "PATCH", path: "/me", body: { displayName: "Ada", profilePreferences: "" } });
    await patchUsername("ada");
    expect(last()).toMatchObject({ method: "PATCH", path: "/me/username", body: { username: "ada" } });
  });

  it("uploads an avatar as multipart with purpose avatar and reads the file id", async () => {
    calls.reply = { file: { fileID: "file_9" }, reused: false };
    const id = await uploadAvatar(new File(["x"], "me.png", { type: "image/png" }));
    expect(id).toBe("file_9");
    const form = last().body as FormData;
    expect(last()).toMatchObject({ method: "POST", path: "/files" });
    expect(form.get("purpose")).toBe("avatar");
    expect((form.get("file") as File).name).toBe("me.png");
  });
});

describe("account wire format", () => {
  it("deletes the account with a JSON body carrying method and code", async () => {
    await account.deleteAccount("two_factor", "123456");
    expect(last()).toMatchObject({ method: "DELETE", path: "/me", options: { body: { verificationMethod: "two_factor", code: "123456" } } });
  });

  it("sends the password change fields without an empty current password", async () => {
    await account.passwordChangeStart("email");
    expect(last()).toMatchObject({ path: "/auth/password/change/start", body: { verificationMethod: "email" } });
    await account.passwordChangeComplete({ newPassword: "abcd1234x", verificationMethod: "none", code: "" });
    expect(last()).toMatchObject({ path: "/auth/password/change/complete", body: { newPassword: "abcd1234x", verificationMethod: "none", code: "" } });
  });

  it("starts the current-identity check with or without a method", async () => {
    await account.emailChangeStartCurrent();
    expect(last().body).toBeUndefined();
    await account.emailChangeStartCurrent("email");
    expect(last()).toMatchObject({ path: "/me/email/change/start-current", body: { verificationMethod: "email" } });
  });

  it("completes an email change with both codes", async () => {
    calls.reply = { user: { email: "a@b.test" } };
    await account.emailChangeComplete({ email: "a@b.test", currentVerificationMethod: "email", currentCode: "111111", newCode: "222222" });
    expect(last()).toMatchObject({
      path: "/me/email/change/complete",
      body: { email: "a@b.test", currentVerificationMethod: "email", currentCode: "111111", newCode: "222222" },
    });
  });

  it("reads the send result, two-factor setup and recovery codes", async () => {
    calls.reply = { sent: true, debugCode: "123456", expiresAt: "x" };
    expect(await account.deleteStart("email")).toEqual({ sent: true, debugCode: "123456" });
    expect(last()).toMatchObject({ path: "/me/delete/start", body: { verificationMethod: "email" } });
    calls.reply = { secret: "S", otpauthURL: "otpauth://x", expiresAt: "2030-01-01T00:00:00Z" };
    expect(await account.mfaSetupStart()).toEqual({ secret: "S", otpauthURL: "otpauth://x", expiresAt: "2030-01-01T00:00:00Z" });
    calls.reply = { recoveryCodes: ["a", 3, "b"], status: {} };
    expect(await account.mfaSetupConfirm("123456")).toEqual(["a", "b"]);
    expect(last()).toMatchObject({ path: "/me/2fa/setup/confirm", body: { code: "123456" } });
    await account.mfaSetupCancel();
    expect(last()).toMatchObject({ method: "DELETE", path: "/me/2fa/setup" });
  });

  it("revokes a session by its public id and unlinks an identity by number", async () => {
    await account.revokeSession("ses_1/../x");
    expect(last().path).toBe("/auth/sessions/ses_1%2F..%2Fx/logout");
    await account.unlinkIdentity(7);
    expect(last()).toMatchObject({ method: "DELETE", path: "/me/identities/7" });
  });

  it("reads session and identity rows from results[]", async () => {
    calls.reply = { results: [{ sessionID: "s", current: true, browserName: "Firefox" }, null], total: 1 };
    const rows = await account.fetchSessions();
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ sessionID: "s", current: true, browserName: "Firefox", cityName: "" });
    calls.reply = { results: [{ id: 3, providerSlug: "gh", providerDisplayName: "ada", linkedAt: "t" }] };
    expect((await account.fetchIdentities())[0]).toMatchObject({ id: 3, providerSlug: "gh", providerDisplayName: "ada" });
  });
});

describe("memory wire format", () => {
  it("url-encodes the key on delete and sends memoryKey/value/scope on save", async () => {
    await memory.deleteMemory("a b/c");
    expect(last()).toMatchObject({ method: "DELETE", path: "/memories/profile/a%20b%2Fc" });
    await memory.saveMemory({ memoryKey: "tone", value: "dry", scope: "preference" });
    expect(last()).toMatchObject({ method: "PUT", path: "/memories/profile", body: { memoryKey: "tone", value: "dry", scope: "preference" } });
  });
});

describe("provider wire format", () => {
  it("creates with bare names or per-model protocols as given, and never sends an empty name", async () => {
    calls.reply = { provider: { id: "p", name: "n", protocol: "openai_chat_completions", models: ["m"] } };
    const created = await models.createProvider({
      protocol: "openai_chat_completions",
      baseURL: "https://api.x.test/v1",
      apiKey: "k",
      models: ["m"],
      source: "link",
    });
    expect(last()).toMatchObject({ method: "POST", path: "/me/model-providers", body: { models: ["m"], source: "link" } });
    expect(created.models).toEqual([{ name: "m", protocols: ["openai_chat_completions"] }]);
  });

  it("probes with protocol, address and key and normalises suggestions", async () => {
    calls.reply = { models: [{ name: "a", suggestedProtocols: ["x"] }, "b"] };
    const found = await models.probeProvider({ protocol: "p", baseURL: "https://h.test", apiKey: "k" });
    expect(last()).toMatchObject({ path: "/me/model-providers/probe", body: { protocol: "p", baseURL: "https://h.test", apiKey: "k" } });
    expect(found).toEqual([
      { name: "a", suggestedProtocols: ["x"] },
      { name: "b", suggestedProtocols: ["p"] },
    ]);
  });

  it("toggles with enabled only", async () => {
    calls.reply = { provider: { id: "p" } };
    await models.updateProvider("p", { enabled: false });
    expect(last()).toMatchObject({ method: "PATCH", path: "/me/model-providers/p", body: { enabled: false } });
  });
});

describe("billing wire format", () => {
  it("lists the usage log with page, page_size, query, status and sort", async () => {
    calls.reply = { results: [], total: 0 };
    await billing.fetchUsage({ page: 2, pageSize: 25, query: " gpt ", status: "free", sort: "oldest" });
    expect(last()).toMatchObject({ path: "/billing/usage", options: { query: { page: 2, page_size: 25, query: "gpt", status: "free", sort: "oldest" } } });
  });

  it("asks for 12 months and 365 days", async () => {
    calls.reply = [];
    await billing.fetchMonthly();
    expect(last()).toMatchObject({ path: "/billing/usage/monthly", options: { query: { months: 12 } } });
    await billing.fetchActivity();
    expect(last()).toMatchObject({ path: "/user/stats/activity", options: { query: { days: 365 } } });
  });

  it("subscribes to a free price for one cycle and redeems a trimmed code's overview", async () => {
    await billing.subscribeFree(5);
    expect(last()).toMatchObject({ path: "/billing/subscriptions", body: { priceID: 5, cycles: 1 } });
    calls.reply = { redemption: {}, overview: { account: { balanceUSD: 3 } } };
    const overview = await billing.redeemCode("CODE1");
    expect(last()).toMatchObject({ path: "/billing/redemptions", body: { code: "CODE1" } });
    expect(overview.balanceUSD).toBe(3);
  });

  it("reads the checkout url from the nested checkout object", async () => {
    calls.reply = { checkout: { checkoutURL: " https://pay.test/c " } };
    const url = await billing.createCheckout({
      orderType: "topup",
      amountMinorUnits: 2000,
      cycles: 1,
      paymentProvider: "epay",
      epayType: "alipay",
      successURL: "https://x.test/settings/subscription?payment=success",
      cancelURL: "https://x.test/settings/subscription?payment=cancel",
    });
    expect(url).toBe("https://pay.test/c");
    expect(last()).toMatchObject({ path: "/billing/payments/checkout", body: { orderType: "topup", amountMinorUnits: 2000, epayType: "alipay" } });
    calls.reply = { checkout: {} };
    expect(await billing.createCheckout({ orderType: "topup", amountMinorUnits: 1, cycles: 1, paymentProvider: "stripe", successURL: "", cancelURL: "" })).toBe("");
  });
});
