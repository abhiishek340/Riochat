// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { announceConversationChange } from "../lib/conversation-events";
import { DEFAULT_FILTERS, readConversation, type ConversationRow, type Filters } from "./model";
import { clearToasts } from "../test-utils";

const fetchConversationPage = vi.hoisted(() => vi.fn());
vi.mock("./api", () => ({ fetchConversationPage }));

import { renderWithI18n } from "../test-utils";
import { I18nProvider } from "@/lib/i18n/provider";
import { useConversationList } from "./use-conversation-list";

const row = (id: string, over: Record<string, unknown> = {}): ConversationRow =>
  readConversation({ publicID: id, updatedAt: `2025-01-0${id.length}T00:00:00Z`, status: "active", ...over })!;
const pageOf = (rows: ConversationRow[], total = rows.length, received = rows.length) => ({ rows, total, received });
const wrapper = ({ children }: { children: React.ReactNode }) => <I18nProvider>{children}</I18nProvider>;

beforeEach(() => {
  fetchConversationPage.mockReset();
  clearToasts();
});

describe("useConversationList", () => {
  it("loads the first page sorted by recency and is idle afterwards", async () => {
    fetchConversationPage.mockResolvedValue(pageOf([row("a"), row("bbb"), row("cc")]));
    const { result } = renderHook(() => useConversationList(DEFAULT_FILTERS), { wrapper });
    await waitFor(() => expect(result.current.loaded).toBe(true));
    expect(result.current.rows.map((r) => r.publicID)).toEqual(["bbb", "cc", "a"]);
    expect(result.current.hasMore).toBe(false);
    expect(fetchConversationPage).toHaveBeenCalledWith(DEFAULT_FILTERS, 1, expect.anything());
  });

  it("starts over on a filter change and ignores the response of the old filter", async () => {
    let resolveOld: (v: unknown) => void = () => undefined;
    fetchConversationPage.mockImplementationOnce(() => new Promise((resolve) => (resolveOld = resolve)));
    fetchConversationPage.mockResolvedValueOnce(pageOf([row("new")]));
    const { result, rerender } = renderHook(({ filters }: { filters: Filters }) => useConversationList(filters), {
      wrapper,
      initialProps: { filters: DEFAULT_FILTERS },
    });
    rerender({ filters: { ...DEFAULT_FILTERS, status: "archived" } });
    await waitFor(() => expect(result.current.rows.map((r) => r.publicID)).toEqual(["new"]));
    await act(async () => resolveOld(pageOf([row("stale")])));
    expect(result.current.rows.map((r) => r.publicID)).toEqual(["new"]);
  });

  it("pages on demand only when a full page leaves rows to fetch", async () => {
    const full = Array.from({ length: 50 }, (_, i) => row(`id${String(i).padStart(2, "0")}`));
    fetchConversationPage.mockResolvedValueOnce(pageOf(full, 120, 50));
    const { result } = renderHook(() => useConversationList(DEFAULT_FILTERS), { wrapper });
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    fetchConversationPage.mockResolvedValueOnce(pageOf([row("extra"), full[0]!], 120, 50));
    await act(async () => result.current.loadMore());
    await waitFor(() => expect(result.current.rows).toHaveLength(51));
    expect(fetchConversationPage).toHaveBeenLastCalledWith(DEFAULT_FILTERS, 2, expect.anything());
  });

  it("stops auto-loading after a failed page until retried", async () => {
    const full = Array.from({ length: 50 }, (_, i) => row(`id${String(i).padStart(2, "0")}`));
    fetchConversationPage.mockResolvedValueOnce(pageOf(full, 120, 50));
    const { result } = renderHook(() => useConversationList(DEFAULT_FILTERS), { wrapper });
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    fetchConversationPage.mockRejectedValueOnce(new Error("down"));
    await act(async () => result.current.loadMore());
    await waitFor(() => expect(result.current.moreError).toBe(true));
    const calls = fetchConversationPage.mock.calls.length;
    await act(async () => result.current.loadMore());
    expect(fetchConversationPage.mock.calls.length).toBe(calls);
    act(() => result.current.retryMore());
    expect(result.current.moreError).toBe(false);
  });

  it("drops a row that no longer matches the filters after a local change", async () => {
    fetchConversationPage.mockResolvedValue(pageOf([row("aaa"), row("bb")]));
    const { result } = renderHook(() => useConversationList({ ...DEFAULT_FILTERS, status: "active" }), { wrapper });
    await waitFor(() => expect(result.current.rows).toHaveLength(2));
    act(() => result.current.patch("aaa", { status: "archived" }));
    expect(result.current.rows.map((r) => r.publicID)).toEqual(["bb"]);
    act(() => result.current.patch("bb", { title: "renamed" }));
    expect(result.current.rows[0]!.title).toBe("renamed");
  });

  it("follows changes announced elsewhere without refetching", async () => {
    fetchConversationPage.mockResolvedValue(pageOf([row("aaa"), row("bb")]));
    const { result } = renderHook(() => useConversationList({ ...DEFAULT_FILTERS, status: "active" }), { wrapper });
    await waitFor(() => expect(result.current.rows).toHaveLength(2));
    act(() => announceConversationChange({ type: "patch", id: "bb", patch: { status: "archived" } }));
    expect(result.current.rows.map((r) => r.publicID)).toEqual(["aaa"]);
    act(() => announceConversationChange({ type: "remove", ids: ["aaa"] }));
    expect(result.current.rows).toHaveLength(0);
    act(() => announceConversationChange({ type: "upsert", row: row("ccc") }));
    expect(result.current.rows.map((r) => r.publicID)).toEqual(["ccc"]);
    expect(fetchConversationPage).toHaveBeenCalledTimes(1);
  });

  it("only updates listed rows while a text search is active", async () => {
    fetchConversationPage.mockResolvedValue(pageOf([row("aaa")]));
    const { result } = renderHook(() => useConversationList({ ...DEFAULT_FILTERS, query: "x" }), { wrapper });
    await waitFor(() => expect(result.current.rows).toHaveLength(1));
    act(() => announceConversationChange({ type: "upsert", row: row("zzz") }));
    expect(result.current.rows.map((r) => r.publicID)).toEqual(["aaa"]);
    act(() => announceConversationChange({ type: "upsert", row: row("aaa", { title: "changed" }) }));
    expect(result.current.rows[0]!.title).toBe("changed");
  });
});

void renderWithI18n;
