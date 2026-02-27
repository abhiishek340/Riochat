// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";
import {
  BRIDGE_TTL_MS,
  CHALLENGE_KEY,
  CHALLENGE_TTL_MS,
  bridgeKey,
  clearChallenge,
  normalizeMethods,
  readChallenge,
  saveBridgeRequest,
  saveChallenge,
  takeBridgeRequest,
} from "./handoff-storage";

beforeEach(() => window.sessionStorage.clear());

describe("second-factor challenge", () => {
  it("round-trips and is not consumed by reading", () => {
    expect(saveChallenge("tok", ["email", "two_factor"], 1000)).toBe(true);
    expect(readChallenge(2000)).toEqual({ token: "tok", methods: ["email", "two_factor"], expiresAt: 1000 + CHALLENGE_TTL_MS });
    expect(readChallenge(3000)?.token).toBe("tok");
  });
  it("expires after five minutes", () => {
    saveChallenge("tok", ["two_factor"], 0);
    expect(readChallenge(CHALLENGE_TTL_MS + 1)).toBeNull();
    expect(window.sessionStorage.getItem(CHALLENGE_KEY)).toBeNull();
  });
  it("drops legacy plain-string and malformed entries", () => {
    window.sessionStorage.setItem(CHALLENGE_KEY, "plain-token");
    window.sessionStorage.setItem("riochat:2fa:methods", "email");
    expect(readChallenge()).toBeNull();
    expect(window.sessionStorage.getItem(CHALLENGE_KEY)).toBeNull();
    expect(window.sessionStorage.getItem("riochat:2fa:methods")).toBeNull();
    window.sessionStorage.setItem(CHALLENGE_KEY, "{not json");
    expect(readChallenge()).toBeNull();
  });
  it("filters methods to known values and defaults to an authenticator", () => {
    expect(normalizeMethods(["none", "email", "sms", "email"])).toEqual(["email"]);
    expect(normalizeMethods([])).toEqual(["two_factor"]);
    expect(normalizeMethods(undefined)).toEqual(["two_factor"]);
  });
  it("refuses an empty token and can be cleared", () => {
    expect(saveChallenge("", ["email"])).toBe(false);
    saveChallenge("t", []);
    clearChallenge();
    expect(readChallenge()).toBeNull();
  });
});

describe("provider hand-off", () => {
  const request = { verifier: "v".repeat(64), state: "s".repeat(43), intent: "login" as const, next: "/files" };
  it("is single use: reading removes it", () => {
    saveBridgeRequest("gh", request, 0);
    expect(takeBridgeRequest("gh", 1)).toMatchObject({ ...request, expiresAt: BRIDGE_TTL_MS });
    expect(takeBridgeRequest("gh", 1)).toBeNull();
  });
  it("is removed even when expired or malformed", () => {
    saveBridgeRequest("gh", request, 0);
    expect(takeBridgeRequest("gh", BRIDGE_TTL_MS + 1)).toBeNull();
    expect(window.sessionStorage.getItem(bridgeKey("gh"))).toBeNull();
    window.sessionStorage.setItem(bridgeKey("gh"), JSON.stringify({ verifier: "", state: "x", intent: "login", expiresAt: 9e15 }));
    expect(takeBridgeRequest("gh")).toBeNull();
    expect(window.sessionStorage.getItem(bridgeKey("gh"))).toBeNull();
  });
  it("keys entries per provider and lives 10m + 90s + 30s", () => {
    expect(bridgeKey("a")).toBe("riochat:oauth:a:bridge");
    expect(BRIDGE_TTL_MS).toBe(720_000);
  });
});
