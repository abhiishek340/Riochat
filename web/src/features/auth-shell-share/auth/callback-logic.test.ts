// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import type { AuthPayload } from "@/lib/api/types";
import { CHALLENGE_KEY, bridgeKey, readChallenge, saveBridgeRequest } from "../lib/handoff-storage";
import { processCallback, type CallbackDeps } from "./callback-logic";
import type { SettledAuth } from "./sign-in";

const STATE = "s".repeat(43);
const VERIFIER = "v".repeat(64);

function deps(over: Partial<CallbackDeps> = {}): CallbackDeps & { login: ReturnType<typeof vi.fn>; bind: ReturnType<typeof vi.fn> } {
  const login = vi.fn(async () => ({ accessToken: "a" }) as AuthPayload);
  const bind = vi.fn(async () => ({}));
  return {
    desktop: false,
    exchangeLogin: login,
    exchangeBind: bind,
    hasAccessToken: async () => true,
    settle: async (): Promise<SettledAuth> => ({ kind: "signed-in" }),
    ...over,
    login,
    bind,
  } as never;
}

const query = (q: string) => new URLSearchParams(q);

beforeEach(() => window.sessionStorage.clear());

describe("processCallback", () => {
  it("reports a provider error without touching storage", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/files" });
    const d = deps();
    expect(await processCallback(query("error=access_denied"), d)).toEqual({ kind: "provider-error", detail: "access_denied" });
    expect(d.login).not.toHaveBeenCalled();
    expect(window.sessionStorage.getItem(bridgeKey("gh"))).not.toBeNull();
  });
  it("drops the hand-off of the named provider when the provider reports an error", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/files" });
    saveBridgeRequest("other", { verifier: VERIFIER, state: STATE, intent: "login", next: "/files" });
    expect(await processCallback(query("provider=gh&error=access_denied"), deps())).toEqual({ kind: "provider-error", detail: "access_denied" });
    expect(window.sessionStorage.getItem(bridgeKey("gh"))).toBeNull();
    expect(window.sessionStorage.getItem(bridgeKey("other"))).not.toBeNull();
  });
  it("needs both provider and grant", async () => {
    expect(await processCallback(query("provider=gh"), deps())).toEqual({ kind: "missing" });
    expect(await processCallback(query("grant=g"), deps())).toEqual({ kind: "missing" });
  });
  it("exchanges once with the stored verifier, then forgets the hand-off", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/files" });
    const d = deps();
    const result = await processCallback(query(`provider=gh&grant=g&state=${STATE}`), d);
    expect(result).toEqual({ kind: "signed-in", next: "/files" });
    expect(d.login).toHaveBeenCalledTimes(1);
    expect(d.login).toHaveBeenCalledWith("gh", { clientID: "riochat-web", grant: "g", codeVerifier: VERIFIER });
    expect(window.sessionStorage.getItem(bridgeKey("gh"))).toBeNull();
    // a second run of the same callback finds nothing to exchange
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), d)).toEqual({ kind: "expired" });
    expect(d.login).toHaveBeenCalledTimes(1);
  });
  it("uses the desktop client id on desktop", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/chat" });
    const d = deps({ desktop: true });
    await processCallback(query(`provider=gh&grant=g&state=${STATE}`), d);
    expect(d.login).toHaveBeenCalledWith("gh", expect.objectContaining({ clientID: "com.riochat.desktop" }));
  });
  it("rejects a state mismatch and still clears the hand-off", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/chat" });
    const d = deps();
    expect(await processCallback(query("provider=gh&grant=g&state=wrong"), d)).toEqual({ kind: "expired" });
    expect(d.login).not.toHaveBeenCalled();
    expect(window.sessionStorage.getItem(bridgeKey("gh"))).toBeNull();
  });
  it("reports an expired session when nothing was stored, even with a valid grant", async () => {
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), deps())).toEqual({ kind: "expired" });
  });
  it("sanitises the stored next path", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "//evil.example" });
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), deps())).toEqual({ kind: "signed-in", next: "/chat" });
  });
  it("stores a second-factor challenge and asks to go to the login page", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/files" });
    const d = deps({ settle: async () => ({ kind: "challenge", token: "tok", methods: ["email"] }) });
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), d)).toEqual({ kind: "challenge-stored", next: "/files" });
    expect(readChallenge()).toMatchObject({ token: "tok", methods: ["email"] });
    expect(window.sessionStorage.getItem(CHALLENGE_KEY)).not.toBeNull();
  });
  it("shows the conflict panel for an existing account with the same email", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/chat" });
    const error = new ApiError({
      message: "conflict",
      status: 409,
      errorCode: "auth.provider_email_conflict",
      details: { action: "sign_in_then_bind", providerSlug: "gh", email: "a@b.c" },
    });
    const d = deps({ exchangeLogin: async () => Promise.reject(error) });
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), d)).toEqual({ kind: "conflict", providerSlug: "gh", email: "a@b.c" });
  });
  it("passes other exchange failures through", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "login", next: "/chat" });
    const boom = new ApiError({ message: "no", status: 400, errorCode: "auth.oauth_state_expired" });
    const d = deps({ exchangeLogin: async () => Promise.reject(boom) });
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), d)).toEqual({ kind: "failed", error: boom });
  });
  it("links an account only while a session exists", async () => {
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "bind", next: "/settings/account" });
    const noToken = deps({ hasAccessToken: async () => false });
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), noToken)).toEqual({ kind: "bind-session" });
    saveBridgeRequest("gh", { verifier: VERIFIER, state: STATE, intent: "bind", next: "/settings/account" });
    const ok = deps();
    expect(await processCallback(query(`provider=gh&grant=g&state=${STATE}`), ok)).toEqual({ kind: "bound", next: "/settings/account" });
    expect(ok.bind).toHaveBeenCalledTimes(1);
    expect(ok.login).not.toHaveBeenCalled();
  });
});
