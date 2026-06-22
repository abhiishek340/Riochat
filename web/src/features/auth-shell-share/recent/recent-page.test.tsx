// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getToasts } from "@/components/ui/toast-store";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";
import { readConversation, type ConversationRow } from "./model";

const router = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
let search = "";
vi.mock("next/navigation", () => ({ useRouter: () => router, useSearchParams: () => new URLSearchParams(search), usePathname: () => "/recent" }));
vi.mock("@/components/shell/header-context", () => ({ HeaderActions: ({ children }: { children: React.ReactNode }) => <div data-testid="header-actions">{children}</div> }));
const api = vi.hoisted(() => ({
  fetchConversationPage: vi.fn(),
  fetchProjects: vi.fn(),
  fetchDeleteFilesDefault: vi.fn(),
  setArchived: vi.fn(),
  setStarred: vi.fn(),
  revokeShares: vi.fn(),
  exportAllJsonl: vi.fn(),
  exportConversationJson: vi.fn(),
  deleteConversation: vi.fn(),
  moveManyToProject: vi.fn(),
  moveToProject: vi.fn(),
  renameConversation: vi.fn(),
  regenerateTitle: vi.fn(),
  setLabels: vi.fn(),
  fetchShare: vi.fn(),
  createShare: vi.fn(),
  revokeShare: vi.fn(),
}));
vi.mock("./api", () => api);
const download = vi.hoisted(() => ({ downloadBlob: vi.fn() }));
vi.mock("../lib/download", async (orig) => ({ ...(await orig<object>()), downloadBlob: download.downloadBlob }));

import { RecentPage } from "./recent-page";

const row = (n: number, over: Record<string, unknown> = {}): ConversationRow =>
  readConversation({ publicID: `c${n}`, title: `Chat ${n}`, status: "active", updatedAt: new Date(Date.UTC(2025, 0, 1, 0, 0, 100 - n)).toISOString(), ...over })!;
const page = (rows: ConversationRow[], total = rows.length) => ({ rows, total, received: rows.length });

const lastFilters = () => api.fetchConversationPage.mock.calls.at(-1)![0];

beforeEach(() => {
  search = "";
  Object.values(api).forEach((fn) => fn.mockReset());
  download.downloadBlob.mockReset();
  router.push.mockReset();
  api.fetchProjects.mockResolvedValue([{ publicID: "P1", name: "Alpha" }]);
  api.fetchDeleteFilesDefault.mockResolvedValue(false);
  api.fetchConversationPage.mockResolvedValue(page([row(1), row(2)]));
  clearToasts();
});
afterEach(cleanup);

const rowFor = (title: string) => screen.getByText(title).closest("li") as HTMLElement;
const pick = (title: string) => userEvent.click(within(rowFor(title)).getByRole("checkbox"));

describe("RecentPage list", () => {
  it("starts from the project in the URL, 50 per page", async () => {
    search = "project=P1";
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    expect(api.fetchConversationPage.mock.calls[0]![0]).toMatchObject({ project: "P1", query: "" });
    expect(api.fetchConversationPage.mock.calls[0]![1]).toBe(1);
  });

  it("debounces the search and starts again from page one", async () => {
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    const calls = api.fetchConversationPage.mock.calls.length;
    await userEvent.type(screen.getByRole("searchbox"), "X");
    expect(api.fetchConversationPage.mock.calls.length).toBe(calls);
    await waitFor(() => expect(lastFilters().query).toBe("x"));
    expect(api.fetchConversationPage.mock.calls.at(-1)![1]).toBe(1);
  });

  it("names the active filters when nothing matches", async () => {
    api.fetchConversationPage.mockResolvedValue(page([]));
    renderWithI18n(<RecentPage />);
    await screen.findByText("No conversations yet");
    await userEvent.click(screen.getByRole("radio", { name: "Archived" }));
    await userEvent.click(screen.getByRole("radio", { name: "Shared" }));
    await screen.findByText("No archived and shared conversations");
  });

  it("distinguishes a search with no results", async () => {
    api.fetchConversationPage.mockResolvedValue(page([]));
    renderWithI18n(<RecentPage />);
    await screen.findByText("No conversations yet");
    await userEvent.type(screen.getByRole("searchbox"), "zzz");
    await screen.findByText("No matching conversations");
  });

  it("groups by project in the user's order with the default group last", async () => {
    api.fetchConversationPage.mockResolvedValue(page([row(1), row(2, { projectID: "P1", projectName: "Alpha" })]));
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings[0]).toContain("Alpha");
    expect(headings[1]).toContain("Default");
  });

  it("shows a retry when the list cannot load", async () => {
    api.fetchConversationPage.mockRejectedValueOnce(new Error("down"));
    renderWithI18n(<RecentPage />);
    await screen.findByText("Conversations could not be loaded");
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("Chat 1");
  });
});

describe("RecentPage row actions", () => {
  it("removes a row that no longer matches the star filter", async () => {
    api.setStarred.mockResolvedValue({});
    api.fetchConversationPage.mockResolvedValue(page([row(1, { isStarred: true }), row(2, { isStarred: true })]));
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(screen.getByRole("radio", { name: "Starred" }));
    await screen.findByText("Chat 1");
    await userEvent.click(within(rowFor("Chat 1")).getByRole("button", { name: "Conversation actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Unstar" }));
    await waitFor(() => expect(screen.queryByText("Chat 1")).toBeNull());
    expect(api.setStarred).toHaveBeenCalledWith("c1", false);
    expect(screen.getByText("Chat 2")).toBeTruthy();
  });

  it("deletes with the user's default for removing files", async () => {
    api.fetchDeleteFilesDefault.mockResolvedValue(true);
    api.deleteConversation.mockResolvedValue({});
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(within(rowFor("Chat 1")).getByRole("button", { name: "Conversation actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    expect((within(dialog).getByRole("checkbox") as HTMLInputElement).checked).toBe(true);
    expect(within(dialog).getByText(/“Chat 1”/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(api.deleteConversation).toHaveBeenCalledWith("c1", true));
    await waitFor(() => expect(screen.queryByText("Chat 1")).toBeNull());
  });
});

describe("RecentPage bulk actions", () => {
  it("archives a selection in chunks of ten and drops the rows from an Active view", async () => {
    const rows = Array.from({ length: 12 }, (_, i) => row(i + 1));
    api.fetchConversationPage.mockResolvedValue(page(rows));
    api.setArchived.mockResolvedValue({});
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(screen.getByRole("radio", { name: "Active" }));
    await screen.findByText("Chat 1");
    for (const r of rows) await pick(r.title);
    expect(screen.getByText("12 conversations selected")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Archive" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Archive the selected 12 conversations?")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(api.setArchived).toHaveBeenCalledTimes(12));
    await waitFor(() => expect(screen.queryByText("Chat 1")).toBeNull());
    expect(api.setArchived).toHaveBeenCalledWith("c1", true);
  });

  it("offers to unarchive only when every selected conversation is archived", async () => {
    api.fetchConversationPage.mockResolvedValue(page([row(1, { status: "archived" }), row(2, { status: "archived" }), row(3)]));
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await pick("Chat 1");
    await pick("Chat 2");
    expect(screen.getByRole("button", { name: "Unarchive" })).toBeTruthy();
    await pick("Chat 3");
    expect(screen.getByRole("button", { name: "Archive" })).toBeTruthy();
  });

  it("stops sharing only for the shared conversations in the selection", async () => {
    const shared = { shareStatus: "active", shareID: "s" };
    api.fetchConversationPage.mockResolvedValue(page([row(1, shared), row(2, shared), row(3)]));
    api.revokeShares.mockResolvedValue({});
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    for (const t of ["Chat 1", "Chat 2", "Chat 3"]) await pick(t);
    await userEvent.click(screen.getByRole("button", { name: "Stop sharing" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Close shares for the selected 2 conversations\?/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Stop sharing" }));
    await waitFor(() => expect(api.revokeShares).toHaveBeenCalledWith(["c1", "c2"]));
    await waitFor(() => expect(within(rowFor("Chat 1")).queryByLabelText("Shared")).toBeNull());
    expect(toastTitles()).toContain("Shares closed.");
  });

  it("shows the chunks that were closed even when a later chunk fails", async () => {
    const shared = { shareStatus: "active", shareID: "s" };
    const rows = Array.from({ length: 101 }, (_, i) => row(i + 1, shared));
    api.fetchConversationPage.mockResolvedValue(page(rows));
    api.revokeShares.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error("revoke exploded"));
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(screen.getByRole("checkbox", { name: "Select all loaded conversations" }));
    await userEvent.click(screen.getByRole("button", { name: "Stop sharing" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Stop sharing" }));
    await waitFor(() => expect(api.revokeShares).toHaveBeenCalledTimes(2));
    expect(api.revokeShares.mock.calls[0]![0]).toHaveLength(100);
    await waitFor(() => expect(within(rowFor("Chat 1")).queryByLabelText("Shared")).toBeNull());
    expect(within(rowFor("Chat 101")).queryByLabelText("Shared")).not.toBeNull();
    expect(toastTitles()).not.toContain("Shares closed.");
  });

  it("keeps stop-sharing disabled when no selected conversation is shared", async () => {
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await pick("Chat 1");
    expect((screen.getByRole("button", { name: "Stop sharing" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("moves the selection with the batch call and reports a mismatch as an error", async () => {
    api.moveManyToProject.mockResolvedValueOnce(2);
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await pick("Chat 1");
    await pick("Chat 2");
    await userEvent.click(screen.getByRole("button", { name: "Move to project" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Alpha" }));
    await waitFor(() => expect(api.moveManyToProject).toHaveBeenCalledWith(["c1", "c2"], "P1"));
    await waitFor(() => expect(toastTitles()).toContain("Moved 2 conversations to Alpha"));

    api.moveManyToProject.mockResolvedValueOnce(1);
    await pick("Chat 1");
    await pick("Chat 2");
    await userEvent.click(screen.getByRole("button", { name: "Move to project" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Default" }));
    await waitFor(() => expect(toastTitles()).toContain("The selected conversations could not be moved."));
  });

  it("clears the selection when a filter changes", async () => {
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await pick("Chat 1");
    expect(screen.getByText("1 conversation selected")).toBeTruthy();
    await userEvent.click(screen.getByRole("radio", { name: "Active" }));
    await waitFor(() => expect(screen.queryByText("1 conversation selected")).toBeNull());
  });
});

describe("RecentPage export", () => {
  it("downloads everything and warns about a partial export", async () => {
    api.exportAllJsonl.mockResolvedValue('{"id":1}\n{"_type":"export_manifest","complete":false,"exported":5,"failed":2}\n');
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(screen.getByRole("button", { name: "Export all conversations" }));
    await waitFor(() => expect(download.downloadBlob).toHaveBeenCalled());
    expect(download.downloadBlob.mock.calls[0]![1]).toMatch(/^my-conversations-\d{4}-\d{2}-\d{2}\.jsonl$/);
    expect(getToasts().find((t) => t.kind === "warning")?.title).toBe("Export finished with gaps: 5 exported, 2 failed.");
  });

  it("reports a plain success without a manifest and an error on failure", async () => {
    api.exportAllJsonl.mockResolvedValueOnce('{"id":1}\n');
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(screen.getByRole("button", { name: "Export all conversations" }));
    await waitFor(() => expect(toastTitles()).toContain("Your conversations were downloaded."));
    api.exportAllJsonl.mockRejectedValueOnce(new Error("503"));
    await userEvent.click(screen.getByRole("button", { name: "Export all conversations" }));
    await waitFor(() => expect(toastTitles()).toContain("Your conversations could not be exported."));
  });
});

describe("RecentPage header", () => {
  it("starts a new chat inside the filtered project", async () => {
    search = "project=P1";
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(within(screen.getByTestId("header-actions")).getByRole("button", { name: "New chat" }));
    expect(router.push).toHaveBeenCalledWith("/chat?project_id=P1");
  });

  it("starts a plain chat when no project is filtered", async () => {
    renderWithI18n(<RecentPage />);
    await screen.findByText("Chat 1");
    await userEvent.click(within(screen.getByTestId("header-actions")).getByRole("button", { name: "New chat" }));
    expect(router.push).toHaveBeenCalledWith("/chat");
  });
});
