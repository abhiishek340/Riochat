// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SearchItem } from "../lib/search-model";
import { renderWithI18n } from "../test-utils";

const router = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => router, usePathname: () => "/chat" }));
const searchConversations = vi.hoisted(() => vi.fn());
const fetchPreview = vi.hoisted(() => vi.fn());
vi.mock("./search-api", () => ({ searchConversations, fetchPreview }));
const media = vi.hoisted(() => ({ wide: false }));
vi.mock("@/hooks/use-media-query", () => ({ useMediaQuery: () => media.wide, useIsMobile: () => false }));

import { ConversationSearchDialog } from "./search-dialog";
import { invalidateDefaultSearch } from "./use-conversation-search";

const today = new Date();
const iso = (d: Date) => d.toISOString();
const item = (id: string, over: Partial<SearchItem> = {}): SearchItem => ({ publicID: id, title: `Title ${id}`, projectName: "", status: "active", updatedAt: iso(today), ...over });

beforeEach(() => {
  searchConversations.mockReset();
  fetchPreview.mockReset();
  router.push.mockReset();
  media.wide = false;
  invalidateDefaultSearch();
  window.localStorage.clear();
});
afterEach(cleanup);

describe("ConversationSearchDialog", () => {
  it("lists results grouped under Today, with untitled and archived markers", async () => {
    searchConversations.mockResolvedValue({
      hasMore: false,
      results: [item("a"), item("b", { title: "", status: "archived", projectName: "Work" }), item("old", { updatedAt: "2020-02-03T10:00:00Z" })],
    });
    renderWithI18n(<ConversationSearchDialog open onOpenChange={vi.fn()} />);
    await screen.findByRole("option", { name: /Title a/ });
    expect(screen.getByText("Today")).toBeTruthy();
    expect(screen.getByRole("option", { name: /New chat/ })).toBeTruthy();
    expect(screen.getByText("Archived")).toBeTruthy();
    expect(screen.getByText("Work")).toBeTruthy();
    expect(screen.getByText(/February/)).toBeTruthy();
  });

  it("moves with the arrow keys (looping) and opens the highlighted conversation with Enter", async () => {
    searchConversations.mockResolvedValue({ hasMore: false, results: [item("a"), item("b")] });
    const onOpenChange = vi.fn();
    renderWithI18n(<ConversationSearchDialog open onOpenChange={onOpenChange} />);
    await screen.findByRole("option", { name: /Title a/ });
    const input = screen.getByRole("combobox");
    await userEvent.type(input, "{ArrowDown}");
    expect(screen.getByRole("option", { name: /Title a/ }).getAttribute("aria-selected")).toBe("true");
    await userEvent.type(input, "{ArrowDown}{ArrowDown}");
    expect(screen.getByRole("option", { name: /Title a/ }).getAttribute("aria-selected")).toBe("true");
    await userEvent.type(input, "{ArrowUp}{Enter}");
    expect(router.push).toHaveBeenCalledWith("/chat?conversation_id=b");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("ignores Enter while an IME is composing", async () => {
    searchConversations.mockResolvedValue({ hasMore: false, results: [item("a")] });
    renderWithI18n(<ConversationSearchDialog open onOpenChange={vi.fn()} />);
    await screen.findByRole("option", { name: /Title a/ });
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "ArrowDown" });
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter", isComposing: true });
    expect(router.push).not.toHaveBeenCalled();
  });

  it("clicking a result navigates and closes", async () => {
    searchConversations.mockResolvedValue({ hasMore: false, results: [item("a")] });
    const onOpenChange = vi.fn();
    renderWithI18n(<ConversationSearchDialog open onOpenChange={onOpenChange} />);
    await userEvent.click(await screen.findByRole("option", { name: /Title a/ }));
    expect(router.push).toHaveBeenCalledWith("/chat?conversation_id=a");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("offers a retry when the first load fails", async () => {
    searchConversations.mockRejectedValueOnce(new Error("down"));
    renderWithI18n(<ConversationSearchDialog open onOpenChange={vi.fn()} />);
    await screen.findByText("Search is unavailable right now.");
    searchConversations.mockResolvedValue({ hasMore: false, results: [item("a")] });
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByRole("option", { name: /Title a/ });
  });

  it("says so when nothing matches", async () => {
    searchConversations.mockResolvedValue({ hasMore: false, results: [] });
    renderWithI18n(<ConversationSearchDialog open onOpenChange={vi.fn()} />);
    await screen.findByText("No conversations match.");
  });

  it("previews the hovered conversation on wide screens and renders its messages", async () => {
    media.wide = true;
    searchConversations.mockResolvedValue({ hasMore: false, results: [item("a")] });
    fetchPreview.mockResolvedValue([
      { publicID: "m1", role: "user", content: "Hello **there**", errorMessage: "" },
      { publicID: "m2", role: "assistant", content: "Hi!", errorMessage: "" },
    ]);
    renderWithI18n(<ConversationSearchDialog open onOpenChange={vi.fn()} />);
    const row = await screen.findByRole("option", { name: /Title a/ });
    expect(screen.getByText("Point at a conversation to peek inside.")).toBeTruthy();
    fireEvent.mouseMove(row);
    await waitFor(() => expect(fetchPreview).toHaveBeenCalledWith("a", expect.anything()));
    const preview = await screen.findByTestId("search-preview");
    expect(within(preview).getByText("there").tagName).toBe("STRONG");
    expect(within(preview).getByText("Hi!")).toBeTruthy();
  });

  it("moves the preview along with the keyboard after the pointer has rested on a row", async () => {
    media.wide = true;
    searchConversations.mockResolvedValue({ hasMore: false, results: [item("k1"), item("k2")] });
    fetchPreview.mockResolvedValue([{ publicID: "m1", role: "user", content: "Hello", errorMessage: "" }]);
    renderWithI18n(<ConversationSearchDialog open onOpenChange={vi.fn()} />);
    fireEvent.mouseMove(await screen.findByRole("option", { name: /Title k1/ }));
    await waitFor(() => expect(fetchPreview).toHaveBeenCalledWith("k1", expect.anything()));
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "ArrowDown" });
    await waitFor(() => expect(fetchPreview).toHaveBeenCalledWith("k2", expect.anything()));
  });

  it("remembers the preview toggle", async () => {
    media.wide = true;
    searchConversations.mockResolvedValue({ hasMore: false, results: [item("a")] });
    renderWithI18n(<ConversationSearchDialog open onOpenChange={vi.fn()} />);
    await userEvent.click(await screen.findByRole("button", { name: "Hide preview" }));
    expect(window.localStorage.getItem("riochat.navigation-search.preview.open")).toBe("false");
    expect(screen.queryByText("Point at a conversation to peek inside.")).toBeNull();
  });
});
