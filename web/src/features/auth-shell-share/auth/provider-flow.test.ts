// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { bridgeKey } from "../lib/handoff-storage";

const api = vi.hoisted(() => ({ authorizeProvider: vi.fn() }));
vi.mock("./api", async (orig) => ({ ...(await orig<object>()), authorizeProvider: api.authorizeProvider }));

import { callbackPathFromLoopback, startProviderFlow, webRedirectUri } from "./provider-flow";

beforeEach(() => {
  api.authorizeProvider.mockReset();
  window.sessionStorage.clear();
});

describe("redirect helpers", () => {
  it("builds the web redirect and translates a loopback callback", () => {
    expect(webRedirectUri("https://app.example", "git hub")).toBe("https://app.example/auth/callback?provider=git%20hub");
    expect(callbackPathFromLoopback("http://127.0.0.1:5123/oauth/callback?provider=gh&grant=g&state=s")).toBe("/auth/callback?provider=gh&grant=g&state=s");
    expect(callbackPathFromLoopback("https://evil.example/oauth/callback?x=1")).toBeNull();
    expect(callbackPathFromLoopback("garbage")).toBeNull();
  });
});

describe("startProviderFlow (browser)", () => {
  it("stores the hand-off, authorises with PKCE and leaves for the provider", async () => {
    api.authorizeProvider.mockResolvedValue("https://provider.example/authorize?x=1");
    const assign = vi.fn();
    Object.defineProperty(window, "location", { value: { ...window.location, origin: "http://localhost:3000", assign }, writable: true });
    await startProviderFlow({ slug: "gh", intent: "login", next: "/files", navigate: vi.fn() });
    const [slug, body, bind] = api.authorizeProvider.mock.calls[0]!;
    expect(slug).toBe("gh");
    expect(bind).toBe(false);
    expect(body).toMatchObject({ clientID: "riochat-web", redirectURI: "http://localhost:3000/auth/callback?provider=gh", intent: "login", next: "/files" });
    expect(body.codeChallenge).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(body.clientState).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(assign).toHaveBeenCalledWith("https://provider.example/authorize?x=1");
    const stored = JSON.parse(window.sessionStorage.getItem(bridgeKey("gh"))!);
    expect(stored).toMatchObject({ state: body.clientState, intent: "login", next: "/files" });
    expect(stored.verifier).toHaveLength(64);
  });

  it("uses the bind endpoint for linking and removes the hand-off when starting fails", async () => {
    api.authorizeProvider.mockRejectedValue(new Error("nope"));
    await expect(startProviderFlow({ slug: "gh", intent: "bind", next: "/settings/account", navigate: vi.fn() })).rejects.toThrow("nope");
    expect(api.authorizeProvider.mock.calls[0]![2]).toBe(true);
    expect(window.sessionStorage.getItem(bridgeKey("gh"))).toBeNull();
  });
});
