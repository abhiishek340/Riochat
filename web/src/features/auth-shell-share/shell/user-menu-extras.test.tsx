// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { ALL_ENABLED } from "@/lib/capabilities/features";
import { DEFAULT_FEATURE_POLICY } from "@/lib/session/feature-policy";
import type { FeaturePolicy } from "@/lib/api/types";
import { renderWithI18n } from "../test-utils";
import { broadcastUnread, OPEN_ANNOUNCEMENTS_EVENT } from "../lib/announcements-model";

const caps = vi.hoisted(() => ({ flags: {} as Record<string, boolean> }));
vi.mock("@/lib/capabilities/provider", () => ({ useCapabilities: () => ({ flags: caps.flags, loaded: true, reload: () => undefined }) }));
const policy = vi.hoisted(() => ({ value: { policy: {} as unknown, loaded: true } }));
vi.mock("@/lib/session/feature-policy", async (orig) => ({ ...(await orig<object>()), useFeaturePolicy: () => policy.value }));
const platform = vi.hoisted(() => ({ desktop: false }));
vi.mock("@/lib/api/platform", async (orig) => ({ ...(await orig<object>()), isDesktop: () => platform.desktop }));
vi.mock("@/lib/session/provider", () => ({ useSession: () => ({ accessToken: "" }), publishUserUpdate: vi.fn() }));

import { AnnouncementsMenuItem, DownloadDesktopMenuItem, UpgradePlanMenuItem } from "./user-menu-extras";

function inMenu(node: React.ReactNode) {
  return renderWithI18n(
    <DropdownMenu open>
      <DropdownMenuTrigger>open</DropdownMenuTrigger>
      <DropdownMenuContent>{node}</DropdownMenuContent>
    </DropdownMenu>,
  );
}

const setPolicy = (over: Partial<FeaturePolicy>, loaded = true) => {
  policy.value = { policy: { ...DEFAULT_FEATURE_POLICY, ...over }, loaded };
};

beforeEach(() => {
  caps.flags = { ...ALL_ENABLED };
  platform.desktop = false;
  setPolicy({ desktopDownloadURL: "https://example.com/get" });
  broadcastUnread(false);
});
afterEach(cleanup);

describe("DownloadDesktopMenuItem", () => {
  it("links to a valid address in a new tab without an opener", async () => {
    inMenu(<DownloadDesktopMenuItem />);
    const link = await screen.findByRole("menuitem", { name: /desktop app/i });
    expect(link.getAttribute("href")).toBe("https://example.com/get");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toContain("noopener");
    expect(link.getAttribute("rel")).toContain("noreferrer");
  });

  it.each([
    ["an ftp address", { desktopDownloadURL: "ftp://x" }],
    ["credentials in the address", { desktopDownloadURL: "https://u:p@host" }],
    ["an over-long address", { desktopDownloadURL: `https://example.com/${"a".repeat(200)}` }],
    ["no address", { desktopDownloadURL: "" }],
    ["the admin switch off", { desktopDownloadEnabled: false }],
  ])("is hidden for %s", async (_name, over) => {
    setPolicy(over as Partial<FeaturePolicy>);
    inMenu(<DownloadDesktopMenuItem />);
    await screen.findByRole("menu");
    expect(screen.queryByRole("menuitem", { name: /desktop app/i })).toBeNull();
  });

  it("is hidden until the policy has loaded, inside the desktop shell and when the server capability is off", async () => {
    setPolicy({ desktopDownloadURL: "https://example.com/get" }, false);
    const first = inMenu(<DownloadDesktopMenuItem />);
    await screen.findByRole("menu");
    expect(screen.queryByRole("menuitem", { name: /desktop app/i })).toBeNull();
    first.unmount();

    setPolicy({ desktopDownloadURL: "https://example.com/get" });
    platform.desktop = true;
    const second = inMenu(<DownloadDesktopMenuItem />);
    await screen.findByRole("menu");
    expect(screen.queryByRole("menuitem", { name: /desktop app/i })).toBeNull();
    second.unmount();

    platform.desktop = false;
    caps.flags = { ...ALL_ENABLED, desktopDownload: false };
    inMenu(<DownloadDesktopMenuItem />);
    await screen.findByRole("menu");
    expect(screen.queryByRole("menuitem", { name: /desktop app/i })).toBeNull();
  });
});

describe("AnnouncementsMenuItem", () => {
  it("requests the dialog and shows a dot for unread items", async () => {
    broadcastUnread(true);
    inMenu(<AnnouncementsMenuItem />);
    const item = await screen.findByRole("menuitem", { name: /announcements/i });
    expect(screen.getByLabelText("Unread announcements")).toBeTruthy();
    const heard = vi.fn();
    window.addEventListener(OPEN_ANNOUNCEMENTS_EVENT, heard);
    item.click();
    expect(heard).toHaveBeenCalled();
    window.removeEventListener(OPEN_ANNOUNCEMENTS_EVENT, heard);
  });

  it("is absent when the server has announcements off", async () => {
    caps.flags = { ...ALL_ENABLED, announcements: false };
    inMenu(<AnnouncementsMenuItem />);
    await screen.findByRole("menu");
    expect(screen.queryByRole("menuitem", { name: /announcements/i })).toBeNull();
  });
});

describe("UpgradePlanMenuItem", () => {
  it("shows only when billing gating is on", async () => {
    caps.flags = { ...ALL_ENABLED, billingGating: false };
    const off = inMenu(<UpgradePlanMenuItem />);
    await screen.findByRole("menu");
    expect(screen.queryByRole("menuitem", { name: /upgrade/i })).toBeNull();
    off.unmount();
    caps.flags = { ...ALL_ENABLED };
    inMenu(<UpgradePlanMenuItem />);
    expect(await screen.findByRole("menuitem", { name: /upgrade/i })).toBeTruthy();
  });
});
