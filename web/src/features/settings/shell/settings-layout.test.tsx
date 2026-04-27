// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ALL_ENABLED, type Capabilities } from "@/lib/capabilities/features";
import { renderWithApp } from "../test-utils";

const mocks = vi.hoisted(() => ({
  pathname: "/settings/general",
  replace: vi.fn(),
  caps: { flags: {} as unknown, loaded: true },
  access: { access: null as unknown, loaded: false },
}));
vi.mock("next/navigation", () => ({ usePathname: () => mocks.pathname, useRouter: () => ({ replace: mocks.replace }) }));
vi.mock("next/link", () => ({
  default: ({ href, children, prefetch: _prefetch, ...rest }: { href: string; children: React.ReactNode; prefetch?: boolean }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/lib/capabilities/provider", () => ({ useCapabilities: () => mocks.caps }));
vi.mock("../models/use-provider-access", () => ({ useProviderAccess: () => mocks.access }));

import { SettingsLayout } from "./settings-layout";

const flags = (over: Partial<Capabilities> = {}) => ({ ...ALL_ENABLED, ...over });
const links = () => screen.getAllByRole("link").map((a) => a.getAttribute("href"));

beforeEach(() => {
  mocks.pathname = "/settings/general";
  mocks.replace.mockReset();
  mocks.caps = { flags: flags(), loaded: true };
  mocks.access = { access: null, loaded: false };
});
afterEach(cleanup);

describe("SettingsLayout", () => {
  it("lists the sections in order and marks the current page", async () => {
    mocks.access = { access: { enabled: true, maxPerUser: 3, protocols: [], modelProtocols: [] }, loaded: true };
    mocks.pathname = "/settings/chat";
    renderWithApp(
      <SettingsLayout>
        <p>page body</p>
      </SettingsLayout>,
    );
    await screen.findByText("page body");
    expect(links()).toEqual([
      "/settings/general",
      "/settings/chat",
      "/settings/memory",
      "/settings/skills",
      "/settings/tools",
      "/settings/models",
      "/settings/subscription",
      "/settings/account",
      "/settings/about",
    ]);
    const current = screen.getAllByRole("link").filter((a) => a.getAttribute("aria-current") === "page");
    expect(current.map((a) => a.getAttribute("href"))).toEqual(["/settings/chat"]);
    expect(screen.getByRole("navigation", { name: "Settings navigation" })).toBeTruthy();
  });

  it("redirects to General (replace) and renders nothing when billing is off", async () => {
    mocks.caps = { flags: flags({ billingGating: false }), loaded: true };
    mocks.pathname = "/settings/subscription";
    renderWithApp(
      <SettingsLayout>
        <p>page body</p>
      </SettingsLayout>,
    );
    await waitFor(() => expect(mocks.replace).toHaveBeenCalledWith("/settings/general"));
    expect(screen.queryByText("page body")).toBeNull();
    expect(links()).not.toContain("/settings/subscription");
  });

  it("waits for the capabilities before guarding", async () => {
    mocks.caps = { flags: flags({ billingGating: false }), loaded: false };
    mocks.pathname = "/settings/subscription";
    renderWithApp(
      <SettingsLayout>
        <p>page body</p>
      </SettingsLayout>,
    );
    await screen.findByText("page body");
    expect(mocks.replace).not.toHaveBeenCalled();
  });

  it("hides Models until the personal-provider answer says yes", async () => {
    renderWithApp(<SettingsLayout>x</SettingsLayout>);
    await screen.findByRole("navigation");
    expect(links()).not.toContain("/settings/models");
    cleanup();
    mocks.access = { access: { enabled: false, maxPerUser: 0, protocols: [], modelProtocols: [] }, loaded: true };
    renderWithApp(<SettingsLayout>x</SettingsLayout>);
    await screen.findByRole("navigation");
    expect(links()).not.toContain("/settings/models");
  });

  it("sends a browser away from the desktop-only network page after hydration", async () => {
    mocks.pathname = "/settings/network";
    renderWithApp(<SettingsLayout>body</SettingsLayout>);
    await waitFor(() => expect(mocks.replace).toHaveBeenCalledWith("/settings/general"));
  });

  it("keeps the Network link in the desktop shell", async () => {
    window.__RIOCHAT_DESKTOP__ = true;
    mocks.pathname = "/settings/network";
    renderWithApp(<SettingsLayout>body</SettingsLayout>);
    await screen.findByText("body");
    expect(links()).toContain("/settings/network");
    expect(mocks.replace).not.toHaveBeenCalled();
    delete window.__RIOCHAT_DESKTOP__;
  });
});
