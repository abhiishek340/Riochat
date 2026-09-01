// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";

const mocks = vi.hoisted(() => ({
  access: { access: { enabled: true, maxPerUser: 5, protocols: [], modelProtocols: [] } as unknown, loaded: true },
  listProviders: vi.fn(),
  createProvider: vi.fn(),
  probeProvider: vi.fn(),
  replace: vi.fn(),
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace: mocks.replace, push: vi.fn() }) }));
vi.mock("@/lib/session/auth-guard", () => ({ AuthGuard: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock("../models/use-provider-access", () => ({ useProviderAccess: () => mocks.access }));
vi.mock("../models/api", async (orig) => ({
  ...(await orig<object>()),
  listProviders: mocks.listProviders,
  createProvider: mocks.createProvider,
  probeProvider: mocks.probeProvider,
}));

import { clearStash, readStash, saveStash, STASH_KEY } from "../models/link-stash";
import { ProviderImportPage } from "./import-page";

const LINK = "v=1&url=https://api.example.com/v1&key=sk-abcdefghijklmnop&name=Mine";

beforeEach(() => {
  mocks.access = { access: { enabled: true, maxPerUser: 5, protocols: [], modelProtocols: [] }, loaded: true };
  mocks.listProviders.mockReset().mockResolvedValue([]);
  mocks.createProvider.mockReset();
  mocks.probeProvider.mockReset();
  mocks.replace.mockReset();
  window.history.replaceState(null, "", "/import");
  clearStash();
});
afterEach(cleanup);

describe("ProviderImportPage", () => {
  it("takes the fragment out of the address bar at once and shows the link without sending anything", async () => {
    window.history.replaceState(null, "", `/import?x=1#${LINK}`);
    renderWithApp(<ProviderImportPage />);
    // The address has no fragment by the time anything is on screen.
    expect(window.location.hash).toBe("");
    expect(window.location.search).toBe("?x=1");
    expect(await screen.findByText("Mine")).toBeTruthy();
    expect(screen.getByText("https://api.example.com/v1")).toBeTruthy();
    expect(screen.getByText("sk-••••••••mnop")).toBeTruthy();
    expect(screen.queryByText(/sk-abcdefghijklmnop/)).toBeNull();
    expect(screen.getByText(/Your key will be sent to api.example.com/)).toBeTruthy();
    expect(mocks.probeProvider).not.toHaveBeenCalled();
    expect(mocks.createProvider).not.toHaveBeenCalled();
    // Once the screen is up the stash has done its job.
    await waitFor(() => expect(window.sessionStorage.getItem(STASH_KEY)).toBeNull());
  });

  it("restores a stashed link when the address carries none", async () => {
    saveStash(LINK);
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByText("Mine")).toBeTruthy();
  });

  it("ignores a stash older than ten minutes", async () => {
    saveStash(LINK, Date.now() - 11 * 60 * 1000);
    expect(readStash()).toBe("");
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByText("This link cannot be used")).toBeTruthy();
    expect(screen.getByText("The link is empty.")).toBeTruthy();
  });

  it("explains why a link is rejected", async () => {
    window.history.replaceState(null, "", "/import#v=1&url=http://api.example.com&key=k1");
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByText("The address must use https.")).toBeTruthy();
  });

  it("names the entry in a multi-entry failure", async () => {
    window.history.replaceState(null, "", "/import#v=2&url=https://a.test&key=k1&url=https://b.test&key=k2&url=https://b.test&key=k2");
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByText("Entry 3: The same address and key appear twice.")).toBeTruthy();
  });

  it("says imports are unavailable when personal providers are off", async () => {
    mocks.access = { access: { enabled: false, maxPerUser: 0, protocols: [], modelProtocols: [] }, loaded: true };
    window.history.replaceState(null, "", `/import#${LINK}`);
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByText("Imports are not available")).toBeTruthy();
  });

  it("warns about a host that already has a provider and leaves via Cancel", async () => {
    mocks.listProviders.mockResolvedValue([{ id: "p1", name: "Old", host: "api.example.com" }]);
    window.history.replaceState(null, "", `/import#${LINK}`);
    const user = userEvent.setup();
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByText(/already have a provider on api.example.com/)).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(mocks.replace).toHaveBeenCalledWith("/chat");
  });

  it("offers Manage instead of Continue at the provider limit", async () => {
    mocks.listProviders.mockResolvedValue(Array.from({ length: 5 }, (_, i) => ({ id: `p${i}`, name: `P${i}`, host: `h${i}.test` })));
    window.history.replaceState(null, "", `/import#${LINK}`);
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByRole("button", { name: "Manage providers" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
  });

  it("keeps the stashed link while the provider list is still loading", async () => {
    let release!: (v: unknown[]) => void;
    mocks.listProviders.mockReturnValue(new Promise((resolve) => (release = resolve)));
    saveStash(LINK);
    renderWithApp(<ProviderImportPage />);
    expect(await screen.findByText("Preparing your import…")).toBeTruthy();
    // A reload at this point (for example a sign-in redirect) must still find the link.
    expect(readStash()).toBe(LINK);
    release([]);
    expect(await screen.findByText("Mine")).toBeTruthy();
    await waitFor(() => expect(readStash()).toBe(""));
  });

  it("does not guess about limits when the provider list cannot be read, and retries", async () => {
    mocks.listProviders.mockRejectedValueOnce(new Error("offline")).mockResolvedValue([]);
    window.history.replaceState(null, "", `/import#${LINK}`);
    const user = userEvent.setup();
    renderWithApp(<ProviderImportPage />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Could not check your providers");
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Mine")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Continue" })).toBeTruthy();
  });
});
