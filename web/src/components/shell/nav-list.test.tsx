// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ADMIN_NAV } from "@/config/navigation";
import { ALL_ENABLED } from "@/lib/capabilities/features";
import { I18nProvider } from "@/lib/i18n/provider";

const state = vi.hoisted(() => ({ role: "admin", flags: {} as Record<string, boolean>, kb: true, path: "/admin/statistics" }));
vi.mock("next/navigation", () => ({ usePathname: () => state.path }));
vi.mock("@/lib/session/provider", () => ({ useSession: () => ({ user: { role: state.role }, status: "ready" }) }));
vi.mock("@/lib/capabilities/provider", () => ({
  useCapabilities: () => ({ flags: { ...ALL_ENABLED, ...state.flags }, loaded: true }),
  isAvailable: (entry: { feature?: string }, flags: Record<string, boolean>) => !entry.feature || flags[entry.feature],
}));
vi.mock("@/lib/session/feature-policy", () => ({ useFeaturePolicy: () => ({ policy: { knowledgeBaseEnabled: state.kb } }) }));

import { NavList } from "./nav-list";

function links(): string[] {
  return screen.getAllByRole("link").map((a) => a.getAttribute("href") ?? "");
}

function renderNav(defaultActiveId?: string) {
  return render(
    <I18nProvider>
      <TooltipProvider>
        <NavList groups={ADMIN_NAV} collapsed={false} label="Admin console" defaultActiveId={defaultActiveId} renderMark={(entry) => (entry.mark === "update" ? <span data-testid="update-mark" /> : null)} />
      </TooltipProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  state.role = "admin";
  state.flags = {};
  state.kb = true;
  state.path = "/admin/statistics";
});
afterEach(cleanup);

describe("admin sidebar", () => {
  it("hides content moderation from plain administrators and shows it to super administrators", async () => {
    renderNav();
    expect(links()).not.toContain("/admin/content-moderation");
    cleanup();
    state.role = "superadmin";
    renderNav();
    expect(links()).toContain("/admin/content-moderation");
  });

  it("still lists content moderation to nobody while the server feature is off", () => {
    state.role = "superadmin";
    state.flags = { contentModeration: false };
    renderNav();
    expect(links()).not.toContain("/admin/content-moderation");
  });

  it("drops the sections whose feature is off but keeps statistics and billing", () => {
    state.flags = { multiUser: false, identityProviders: false, announcements: false, billingGating: false, usageMetering: false };
    renderNav();
    const hrefs = links();
    for (const gone of ["/admin/users", "/admin/groups", "/admin/user-keys", "/admin/login", "/admin/announcements"]) expect(hrefs).not.toContain(gone);
    for (const kept of ["/admin/statistics", "/admin/billing", "/admin/upstreams", "/admin/models", "/admin/about"]) expect(hrefs).toContain(kept);
  });

  it("puts the update mark on the About entry only", () => {
    renderNav();
    const about = screen.getAllByRole("link").find((a) => a.getAttribute("href") === "/admin/about") as HTMLElement;
    expect(about.querySelector('[data-testid="update-mark"]')).not.toBeNull();
    expect(screen.getAllByTestId("update-mark")).toHaveLength(1);
  });

  it("names the navigation landmark after the console", () => {
    renderNav();
    expect(screen.getByRole("navigation", { name: "Admin console" })).toBeTruthy();
  });

  it("marks the current section", () => {
    renderNav();
    const current = screen.getAllByRole("link").filter((a) => a.getAttribute("aria-current") === "page");
    expect(current.map((a) => a.getAttribute("href"))).toEqual(["/admin/statistics"]);
  });

  it("keeps the knowledge base entry while the feature is switched off, so it can be switched back on", () => {
    state.kb = false;
    renderNav();
    expect(links()).toContain("/admin/knowledge-bases");
  });

  it("scrolls the active entry into view", () => {
    const calls: unknown[] = [];
    const original = HTMLElement.prototype.scrollIntoView;
    HTMLElement.prototype.scrollIntoView = function (arg?: boolean | ScrollIntoViewOptions) {
      calls.push([this.getAttribute("href"), arg]);
    };
    try {
      renderNav();
    } finally {
      HTMLElement.prototype.scrollIntoView = original;
    }
    expect(calls).toEqual([["/admin/statistics", { block: "nearest", inline: "center" }]]);
  });

  it("highlights the section a nested path belongs to, and the default one for an unknown path", () => {
    state.path = "/admin/models/extra";
    renderNav("statistics");
    expect(screen.getByRole("link", { name: "Models" }).getAttribute("aria-current")).toBe("page");
    cleanup();
    state.path = "/admin/nowhere";
    renderNav("statistics");
    const current = screen.getAllByRole("link").filter((a) => a.getAttribute("aria-current") === "page");
    expect(current.map((a) => a.getAttribute("href"))).toEqual(["/admin/statistics"]);
  });
});
