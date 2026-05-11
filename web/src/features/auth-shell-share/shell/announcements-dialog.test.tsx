// @vitest-environment jsdom
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Announcement, AnnouncementSort } from "../lib/announcements-model";
import { renderWithI18n } from "../test-utils";
import { AnnouncementsDialog } from "./announcements-dialog";

afterEach(cleanup);

const item = (id: number, over: Partial<Announcement> = {}): Announcement => ({
  id,
  title: `Item ${id}`,
  contentMarkdown: `Body **${id}**`,
  type: "general",
  pinned: false,
  priority: 0,
  status: "published",
  startsAt: null,
  expiresAt: null,
  createdAt: "2025-01-01T00:00:00Z",
  updatedAt: "2025-01-0" + id + "T00:00:00Z",
  createdByUserID: 1,
  closedAt: null,
  ...over,
});

function Harness({ mode, items, onClose = vi.fn(), onDismiss = vi.fn() }: { mode: "auto" | "manual"; items: Announcement[]; onClose?: () => void; onDismiss?: () => void }) {
  const [sort, setSort] = useState<AnnouncementSort>("default");
  return (
    <AnnouncementsDialog open mode={mode} items={items} loading={false} saving={false} sort={sort} onSortChange={setSort} onHide={vi.fn()} onDismissToday={onDismiss} onClose={onClose} />
  );
}

const titles = () => within(screen.getByRole("list", { name: "Announcement list" })).getAllByRole("button").map((b) => b.textContent ?? "");

describe("AnnouncementsDialog", () => {
  const items = [item(1, { type: "info", closedAt: "2025-02-01T00:00:00Z" }), item(2, { type: "general" }), item(3, { type: "critical" })];

  it("keeps the server order by default and selects the first entry", () => {
    renderWithI18n(<Harness mode="manual" items={items} />);
    expect(titles()[0]).toContain("Item 1");
    expect(screen.getByRole("heading", { name: "Item 1" })).toBeTruthy();
  });

  it("sorting by type puts the unread critical item first and resets the selection", async () => {
    renderWithI18n(<Harness mode="manual" items={items} />);
    await userEvent.click(screen.getByRole("button", { name: "By type" }));
    expect(titles()[0]).toContain("Item 3");
    expect(screen.getByRole("heading", { name: "Item 3" })).toBeTruthy();
  });

  it("exposes the sort choice as pressed buttons, not as unlabelled tabs", async () => {
    renderWithI18n(<Harness mode="manual" items={items} />);
    expect(screen.queryByRole("tab")).toBeNull();
    const byType = screen.getByRole("button", { name: "By type" });
    expect(byType.getAttribute("aria-pressed")).toBe("false");
    await userEvent.click(byType);
    expect(byType.getAttribute("aria-pressed")).toBe("true");
  });

  it("shows the selected body as markdown and lets the reader pick another entry", async () => {
    renderWithI18n(<Harness mode="manual" items={items} />);
    await userEvent.click(within(screen.getByRole("list", { name: "Announcement list" })).getByRole("button", { name: /Item 2/ }));
    expect(screen.getByRole("heading", { name: "Item 2" })).toBeTruthy();
    expect(document.querySelector("article strong")?.textContent).toBe("2");
  });

  it("marks unread entries", () => {
    renderWithI18n(<Harness mode="manual" items={items} />);
    expect(screen.getAllByRole("img", { name: "Unread" })).toHaveLength(2);
  });

  it("offers 'not again today' only in auto mode", () => {
    const { unmount } = renderWithI18n(<Harness mode="auto" items={items} />);
    expect(screen.getByRole("button", { name: "Not again today" })).toBeTruthy();
    unmount();
    renderWithI18n(<Harness mode="manual" items={items} />);
    expect(screen.queryByRole("button", { name: "Not again today" })).toBeNull();
  });

  it("shows an empty message when there is nothing", () => {
    renderWithI18n(<Harness mode="manual" items={[]} />);
    expect(screen.getByText("There are no announcements.")).toBeTruthy();
  });
});
