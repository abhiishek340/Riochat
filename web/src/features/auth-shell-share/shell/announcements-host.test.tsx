// @vitest-environment jsdom
import { act, cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Announcement } from "../lib/announcements-model";
import { currentUnread, OPEN_ANNOUNCEMENTS_EVENT } from "../lib/announcements-model";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";

const route = vi.hoisted(() => ({ path: "/chat" }));
vi.mock("next/navigation", () => ({ usePathname: () => route.path }));
const session = vi.hoisted(() => ({ value: { accessToken: "tok", status: "ready", user: { initialSecurityRequired: false } } as unknown }));
vi.mock("@/lib/session/provider", () => ({ useSession: () => session.value }));
vi.mock("@/lib/capabilities/provider", () => ({ useCapabilities: () => ({ flags: { announcements: true } }) }));
const api = vi.hoisted(() => ({ loadAnnouncements: vi.fn(), dismissForToday: vi.fn(), closeAnnouncement: vi.fn() }));
vi.mock("./announcements-api", async (orig) => ({ ...(await orig<object>()), ...api }));

import { AnnouncementsHost } from "./announcements-host";

const item = (id: number, over: Partial<Announcement> = {}): Announcement => ({
  id,
  title: `Item ${id}`,
  contentMarkdown: "body",
  type: "general",
  pinned: false,
  priority: 0,
  status: "published",
  startsAt: null,
  expiresAt: null,
  createdAt: "2025-01-01T00:00:00Z",
  updatedAt: `2025-01-0${id}T00:00:00Z`,
  createdByUserID: 1,
  closedAt: null,
  ...over,
});

/** The dialog has a corner X and a footer button, both named "Close"; the footer one comes first in the DOM. */
const footerClose = () => screen.getAllByRole("button", { name: "Close" }).at(0)!;

beforeEach(() => {
  route.path = "/chat";
  session.value = { accessToken: "tok", status: "ready", user: { initialSecurityRequired: false } };
  Object.values(api).forEach((fn) => fn.mockReset());
  clearToasts();
});
afterEach(cleanup);

describe("AnnouncementsHost", () => {
  it("opens by itself when something is unread and sends each item's own updatedAt", async () => {
    api.loadAnnouncements.mockResolvedValue([item(1), item(2), item(3, { closedAt: "2025-02-01T00:00:00Z" })]);
    api.dismissForToday.mockResolvedValue(undefined);
    renderWithI18n(<AnnouncementsHost />);
    await userEvent.click(await screen.findByRole("button", { name: "Not again today" }));
    await waitFor(() => expect(api.dismissForToday).toHaveBeenCalledTimes(2));
    const sent = api.dismissForToday.mock.calls.map((c) => (c[0] as Announcement).updatedAt).sort();
    expect(sent).toEqual(["2025-01-01T00:00:00Z", "2025-01-02T00:00:00Z"]);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(currentUnread()).toBe(false);
  });

  it("stays closed and does not load on a share page", async () => {
    route.path = "/share";
    api.loadAnnouncements.mockResolvedValue([item(1)]);
    renderWithI18n(<AnnouncementsHost />);
    await act(async () => undefined);
    expect(api.loadAnnouncements).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("keeps the dialog open after a failed close, and only retries what failed", async () => {
    api.loadAnnouncements.mockResolvedValue([item(1), item(2)]);
    api.closeAnnouncement.mockImplementation(async (it: Announcement) => {
      if (it.id === 2) throw new Error("boom");
    });
    renderWithI18n(<AnnouncementsHost />);
    await screen.findByRole("dialog");
    await userEvent.click(footerClose());
    await waitFor(() => expect(toastTitles().length).toBe(1));
    expect(screen.getByRole("dialog")).toBeTruthy();
    // Item 1 went through and now shows as read; only item 2 still has the unread marker.
    expect(screen.getAllByRole("img", { name: "Unread" })).toHaveLength(1);
    api.closeAnnouncement.mockReset();
    api.closeAnnouncement.mockResolvedValue(undefined);
    await userEvent.click(footerClose());
    await waitFor(() => expect(api.closeAnnouncement).toHaveBeenCalledTimes(1));
    expect((api.closeAnnouncement.mock.calls[0]?.[0] as Announcement).id).toBe(2);
  });

  it("loads the full history when opened by hand", async () => {
    api.loadAnnouncements.mockResolvedValue([]);
    renderWithI18n(<AnnouncementsHost />);
    await waitFor(() => expect(api.loadAnnouncements).toHaveBeenCalledWith(false));
    api.loadAnnouncements.mockResolvedValue([item(1, { closedAt: "2025-02-01T00:00:00Z" })]);
    act(() => {
      window.dispatchEvent(new Event(OPEN_ANNOUNCEMENTS_EVENT));
    });
    await waitFor(() => expect(api.loadAnnouncements).toHaveBeenCalledWith(true));
    expect(await screen.findByRole("heading", { name: "Item 1" })).toBeTruthy();
  });
});
