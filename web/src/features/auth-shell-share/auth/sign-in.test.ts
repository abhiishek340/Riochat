import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import type { AuthPayload } from "@/lib/api/types";

const adopt = vi.fn();
vi.mock("@/lib/api/endpoints/auth", () => ({ adoptSession: (p: unknown) => adopt(p) }));
const store = vi.fn();
vi.mock("../desktop/bridge", () => ({ shell: { storeSession: (t: string) => store(t) } }));
let desktop = false;
vi.mock("@/lib/api/platform", () => ({ isDesktop: () => desktop }));

import { isChallengeExpired, lockMinutes, settleAuth } from "./sign-in";

const payload = (over: Partial<AuthPayload>): AuthPayload =>
  ({ accessToken: "", sessionID: "", expiresAt: "", refreshExpiresAt: "", user: {}, twoFactorRequired: false, ...over }) as AuthPayload;

beforeEach(() => {
  adopt.mockReset();
  store.mockReset();
  desktop = false;
});

describe("settleAuth", () => {
  it("reports a challenge with default and filtered methods, adopting nothing", async () => {
    const a = await settleAuth(payload({ twoFactorRequired: true, twoFactorChallengeToken: "t" }));
    expect(a).toEqual({ kind: "challenge", token: "t", methods: ["two_factor"] });
    const b = await settleAuth(payload({ twoFactorRequired: true, twoFactorChallengeToken: "t", verificationMethods: ["email", "none", "two_factor"] }));
    expect(b).toMatchObject({ methods: ["email", "two_factor"] });
    expect(adopt).not.toHaveBeenCalled();
  });
  it("treats a challenge without a token, or no token at all, as empty", async () => {
    expect(await settleAuth(payload({ twoFactorRequired: true }))).toEqual({ kind: "empty" });
    expect(await settleAuth(payload({}))).toEqual({ kind: "empty" });
  });
  it("adopts the session on success and keeps the refresh token away from the browser", async () => {
    expect(await settleAuth(payload({ accessToken: "a", sessionID: "s", refreshToken: "r" }))).toEqual({ kind: "signed-in" });
    expect(adopt).toHaveBeenCalledWith(expect.objectContaining({ accessToken: "a", sessionID: "s" }));
    expect(store).not.toHaveBeenCalled();
  });
  it("hands the rotating refresh token to the desktop shell", async () => {
    desktop = true;
    await settleAuth(payload({ accessToken: "a", sessionID: "s", refreshToken: "r" }));
    expect(store).toHaveBeenCalledWith("r");
  });
  it("still signs in when the shell cannot store the token", async () => {
    desktop = true;
    store.mockRejectedValue(new Error("keychain locked"));
    expect(await settleAuth(payload({ accessToken: "a", refreshToken: "r" }))).toEqual({ kind: "signed-in" });
  });
});

describe("error helpers", () => {
  it("recognises the expired challenge by message or code", () => {
    expect(isChallengeExpired(new ApiError({ message: "x", status: 401, rawMessage: "two factor challenge expired" }))).toBe(true);
    expect(isChallengeExpired(new ApiError({ message: "x", status: 401, errorCode: "auth.two_factor_expired" }))).toBe(true);
    expect(isChallengeExpired(new ApiError({ message: "x", status: 400, rawMessage: "two factor challenge expired" }))).toBe(false);
    expect(isChallengeExpired(new Error("two factor challenge expired"))).toBe(false);
  });
  it("rounds the lockout up to whole minutes", () => {
    const locked = (s?: number) => new ApiError({ message: "x", status: 423, errorCode: "auth.account_locked", retryAfterSeconds: s });
    expect(lockMinutes(locked(301))).toBe(6);
    expect(lockMinutes(locked(60))).toBe(1);
    expect(lockMinutes(locked(undefined))).toBeNull();
    expect(lockMinutes(new ApiError({ message: "x", status: 423 }))).toBeNull();
  });
});
