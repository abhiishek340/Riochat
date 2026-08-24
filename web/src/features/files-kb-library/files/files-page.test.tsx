// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui";
import { dismiss, getToasts } from "@/components/ui/toast-store";
import type { FileObject } from "../shared/types";

const searchParams = new URLSearchParams();

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());
vi.mock("next/navigation", () => ({ useSearchParams: () => searchParams }));
vi.mock("@/hooks/use-media-query", () => ({ useIsMobile: () => false, useMediaQuery: () => false }));
vi.mock("../shared/session", () => ({ hasSession: async () => true }));
vi.mock("./api", () => ({
  listFiles: vi.fn(),
  locateFile: vi.fn(),
  fetchContent: vi.fn(),
  fetchExtract: vi.fn(),
  fetchStatuses: vi.fn(async () => []),
  patchFile: vi.fn(),
  deleteFile: vi.fn(),
  uploadFile: vi.fn(),
  submitEmbeddings: vi.fn(),
}));

import * as api from "./api";
import { FilesPage } from "./files-page";

const mocked = vi.mocked(api);

function makeFile(n: number, over: Partial<FileObject> = {}): FileObject {
  return {
    fileID: `f${n}`,
    fileName: `file-${n}.pdf`,
    mimeType: "application/pdf",
    detectedMIME: "application/pdf",
    fileCategory: "pdf",
    sizeBytes: 1000,
    sha256: "",
    purpose: "",
    status: "active",
    processingStatus: "ready",
    processingReady: true,
    processingErrorCode: "",
    processingErrorMessage: "",
    extractStatus: "ready",
    embedStatus: "none",
    embedError: "",
    chunkCount: 0,
    canVectorize: false,
    vectorizationReason: "",
    ragOptOut: false,
    createdAt: "2025-01-01T00:00:00Z",
    updatedAt: "2025-01-01T00:00:00Z",
    expiresAt: null,
    lastAccessedAt: null,
    ...over,
  };
}

const QUOTA = { quotaBytes: 0, usedBytes: 0 };

function renderPage() {
  return render(
    <TooltipProvider>
      <FilesPage />
    </TooltipProvider>,
  );
}

beforeEach(() => {
  for (const key of [...searchParams.keys()]) searchParams.delete(key);
  vi.clearAllMocks();
  window.localStorage.clear();
  mocked.fetchContent.mockImplementation(async () => new Response("hello", { headers: { "content-type": "text/plain" } }));
  mocked.fetchExtract.mockResolvedValue({ fileID: "f1", extractText: "extracted words", previewText: "", extractChars: 15, extractPages: 1, ocrUsed: false });
  mocked.fetchStatuses.mockResolvedValue([]);
  Object.assign(URL, { createObjectURL: () => "blob:test", revokeObjectURL: () => undefined });
});
afterEach(() => {
  cleanup();
  dismiss();
});

describe("FilesPage list loading", () => {
  it("loads page 1 with the documented query and shows the footer (scenario 1)", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1), makeFile(2)], total: 2, quota: QUOTA });
    renderPage();
    expect(await screen.findAllByText("file-1.pdf")).toBeTruthy();
    expect(mocked.listFiles.mock.calls[0]?.[0]).toEqual({ page: 1, pageSize: 100, q: "", kinds: [], sort: "created" });
    expect(await screen.findByText("That is everything")).toBeTruthy();
  });

  it("keeps loading pages while more exist, de-duplicating", async () => {
    const page = (p: number, ids: number[]) => ({ results: ids.map((n) => makeFile(n)), total: 250, quota: QUOTA });
    mocked.listFiles.mockImplementation(async (params) => {
      if (params.page === 1) return page(1, Array.from({ length: 100 }, (_, i) => i + 1));
      if (params.page === 2) return page(2, Array.from({ length: 100 }, (_, i) => i + 100)); // overlaps one ID
      return page(3, Array.from({ length: 50 }, (_, i) => i + 200));
    });
    renderPage();
    await waitFor(() => expect(mocked.listFiles.mock.calls.map((c) => c[0].page)).toEqual([1, 2, 3]), { timeout: 5000 });
    expect(await screen.findByText("That is everything")).toBeTruthy();
    expect(within(screen.getByRole("list")).getAllByText("file-100.pdf")).toHaveLength(1);
  });

  it("shows the empty state when there are no files", async () => {
    mocked.listFiles.mockResolvedValue({ results: [], total: 0, quota: QUOTA });
    renderPage();
    expect(await screen.findByText("Your library is empty")).toBeTruthy();
  });

  it("sends the search text after the debounce and replaces the list (scenario 2)", async () => {
    mocked.listFiles.mockImplementation(async (params) =>
      params.q === "report"
        ? { results: [makeFile(9, { fileName: "report.pdf" })], total: 1, quota: QUOTA }
        : { results: [makeFile(1), makeFile(2)], total: 2, quota: QUOTA },
    );
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    await user.click(screen.getByRole("button", { name: "Search files" }));
    await user.type(screen.getByPlaceholderText("Search by file name"), "report");
    expect((await screen.findAllByText("report.pdf")).length).toBeGreaterThan(0);
    expect(mocked.listFiles.mock.calls.some((c) => c[0].q === "report")).toBe(true);
    expect(screen.queryAllByText("file-1.pdf")).toHaveLength(0);
    // Closing the search clears it and restores the full list.
    await user.click(screen.getByRole("button", { name: "Close search" }));
    expect(await screen.findAllByText("file-1.pdf")).toBeTruthy();
  });

  it("filters by the chosen types in selection order (scenario 3)", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1)], total: 1, quota: QUOTA });
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    await user.click(screen.getByRole("button", { name: "Filter by type" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "PDF" }));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "Images" }));
    await waitFor(() => expect(mocked.listFiles.mock.calls.at(-1)?.[0].kinds).toEqual(["pdf", "image"]));
    await user.click(await screen.findByRole("menuitemcheckbox", { name: "All types" }));
    await waitFor(() => expect(mocked.listFiles.mock.calls.at(-1)?.[0].kinds).toEqual([]));
  });
});

describe("deep link (scenario 4)", () => {
  it("shows a file from outside page 1 first, selected, on the Extract tab", async () => {
    searchParams.set("file", "abc");
    searchParams.set("tab", "extract");
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1), makeFile(2)], total: 2, quota: QUOTA });
    mocked.locateFile.mockResolvedValue(makeFile(77, { fileID: "abc", fileName: "linked.pdf" }));
    mocked.fetchExtract.mockResolvedValue({ fileID: "abc", extractText: "linked text", previewText: "", extractChars: 11, extractPages: 1, ocrUsed: false });
    renderPage();
    expect(await screen.findByText("linked text")).toBeTruthy();
    const rows = within(screen.getByRole("list")).getAllByRole("listitem");
    expect(within(rows[0] as HTMLElement).getByText("linked.pdf")).toBeTruthy();
    expect(screen.getByRole("tab", { name: /Extracted text/ }).getAttribute("aria-selected")).toBe("true");
    expect(mocked.locateFile).toHaveBeenCalledWith("abc", 100, expect.anything());
  });

  it("treats any other tab value as the preview", async () => {
    searchParams.set("tab", "whatever");
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1)], total: 1, quota: QUOTA });
    renderPage();
    await screen.findAllByText("file-1.pdf");
    expect(screen.getByRole("tab", { name: "Preview" }).getAttribute("aria-selected")).toBe("true");
  });
});

describe("preview", () => {
  it("does not request content for a failed file (scenario 14)", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1, { status: "failed" })], total: 1, quota: QUOTA });
    renderPage();
    expect(await screen.findByText("This file failed to process, so there is no preview.")).toBeTruthy();
    expect(screen.getByText("Preview unavailable")).toBeTruthy();
    expect(mocked.fetchContent).not.toHaveBeenCalled();
  });

  it("shows readable text of an unknown type as text (scenario 13)", async () => {
    mocked.listFiles.mockResolvedValue({
      results: [makeFile(1, { fileName: "data.xyz", mimeType: "application/octet-stream", detectedMIME: "application/octet-stream" })],
      total: 1,
      quota: QUOTA,
    });
    mocked.fetchContent.mockImplementation(async () => new Response("plain words", { headers: { "content-type": "application/octet-stream" } }));
    renderPage();
    expect(await screen.findByText("plain words")).toBeTruthy();
  });

  it("falls back to unsupported for a markdown file full of binary garbage (scenario 13)", async () => {
    mocked.listFiles.mockResolvedValue({
      results: [makeFile(1, { fileName: "notes.md", mimeType: "text/markdown", detectedMIME: "text/markdown" })],
      total: 1,
      quota: QUOTA,
    });
    mocked.fetchContent.mockImplementation(
      async () => new Response("\u0000\u0001\u0002�".repeat(100), { headers: { "content-type": "text/markdown" } }),
    );
    renderPage();
    expect(await screen.findByText("This type cannot be previewed")).toBeTruthy();
  });
});

describe("actions", () => {
  it("rejects an empty rename without a request (scenario 8)", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1)], total: 1, quota: QUOTA });
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    await user.click(screen.getAllByRole("button", { name: /^More actions for / })[0] as HTMLElement);
    await user.click(await screen.findByRole("menuitem", { name: "Rename" }));
    const input = await screen.findByLabelText("New file name");
    await user.clear(input);
    await user.type(input, "   {Enter}");
    await waitFor(() => expect(getToasts().some((t) => t.title === "A file name cannot be empty")).toBe(true));
    expect(mocked.patchFile).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("New file name")).not.toBeNull();
  });

  it("restores the name when the server refuses the rename (scenario 8)", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1)], total: 1, quota: QUOTA });
    mocked.patchFile.mockRejectedValue(new Error("nope"));
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    await user.click(screen.getAllByRole("button", { name: /^More actions for / })[0] as HTMLElement);
    await user.click(await screen.findByRole("menuitem", { name: "Rename" }));
    const input = await screen.findByLabelText("New file name");
    await user.clear(input);
    await user.type(input, "better.pdf{Enter}");
    await waitFor(() => expect(getToasts().some((t) => t.title === "Could not rename the file")).toBe(true));
    expect(await screen.findAllByText("file-1.pdf")).not.toHaveLength(0);
    expect(screen.queryAllByText("better.pdf")).toHaveLength(0);
  });

  it("deletes a file after confirmation and selects its neighbour", async () => {
    let remaining = [makeFile(1), makeFile(2)];
    mocked.listFiles.mockImplementation(async () => ({ results: remaining, total: remaining.length, quota: QUOTA }));
    mocked.deleteFile.mockImplementation(async (id) => {
      remaining = remaining.filter((f) => f.fileID !== id);
      return { deleted: true, fileID: id, quota: QUOTA };
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    await user.click(screen.getAllByRole("button", { name: /^Delete file-/ })[0] as HTMLElement);
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(mocked.deleteFile).toHaveBeenCalledWith("f1"));
    await waitFor(() => expect(screen.queryAllByText("file-1.pdf")).toHaveLength(0));
    expect(getToasts().some((t) => t.title === "File deleted")).toBe(true);
    expect(screen.getByRole("heading", { level: 1, name: "file-2.pdf" })).toBeTruthy();
  });

  it("refuses to index more than 100 files and sends nothing (scenario 11)", async () => {
    const files = Array.from({ length: 101 }, (_, i) => makeFile(i + 1, { canVectorize: true }));
    mocked.listFiles.mockResolvedValue({ results: files, total: 101, quota: QUOTA });
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    const sidebar = screen.getByRole("complementary", { name: "Your files" });
    await user.click(within(sidebar).getByRole("button", { name: "Select all" }));
    await user.click(within(sidebar).getByRole("button", { name: "Build index" }));
    await waitFor(() => expect(getToasts().some((t) => t.title === "You can index at most 100 files at a time.")).toBe(true));
    expect(mocked.submitEmbeddings).not.toHaveBeenCalled();
  });

  it("marks submitted files as queued and warns about refused ones (scenario 11)", async () => {
    mocked.listFiles.mockResolvedValue({
      results: [makeFile(1, { canVectorize: true }), makeFile(2, { canVectorize: true })],
      total: 2,
      quota: QUOTA,
    });
    mocked.submitEmbeddings.mockResolvedValue({ submittedFileIDs: ["f1"], skipped: [{ fileID: "f2", reason: "queue_busy" }] });
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    const sidebar = screen.getByRole("complementary", { name: "Your files" });
    await user.click(within(sidebar).getByRole("button", { name: "Select all" }));
    await user.click(within(sidebar).getByRole("button", { name: "Build index" }));
    await waitFor(() => expect(mocked.submitEmbeddings).toHaveBeenCalledWith(["f1", "f2"]));
    await waitFor(() => expect(getToasts().some((t) => t.title === "Queued 1 for indexing, but 1 could not be queued.")).toBe(true));
    expect(await screen.findAllByText("Queued")).not.toHaveLength(0);
  });

  it("bulk deletes 25 files in chunks with a final summary (scenario 10)", async () => {
    const files = Array.from({ length: 25 }, (_, i) => makeFile(i + 1));
    mocked.listFiles.mockResolvedValue({ results: files, total: 25, quota: QUOTA });
    mocked.deleteFile.mockImplementation(async (id) => {
      if (id === "f3" || id === "f20") throw new Error("busy");
      return { deleted: true, fileID: id, quota: QUOTA };
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    await user.click(screen.getByRole("button", { name: "Select all" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("25 files will be removed for good.")).toBeTruthy();
    await act(async () => {
      fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    });
    await waitFor(() => expect(mocked.deleteFile).toHaveBeenCalledTimes(25));
    await waitFor(() => expect(getToasts().some((t) => t.title === "Some files were not deleted")).toBe(true));
    expect(getToasts().find((t) => t.title === "Some files were not deleted")?.description).toBe("23 deleted, 2 failed.");
  });
});

describe("status polling (scenario 12)", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("merges fresh statuses and drops a file that stays missing", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mocked.listFiles.mockResolvedValue({
      results: [makeFile(1, { embedStatus: "queued" }), makeFile(2, { embedStatus: "queued" })],
      total: 2,
      quota: QUOTA,
    });
    mocked.fetchStatuses.mockResolvedValue([
      { fileID: "f1", embedStatus: "ready", chunkCount: 4, processing: false, processingReady: true, processingStatus: "ready", extractStatus: "ready", updatedAt: "2025-01-02T00:00:00Z" },
    ]);
    renderPage();
    await screen.findAllByText("file-2.pdf");
    expect(screen.getAllByText("Queued").length).toBeGreaterThan(0);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    expect(mocked.fetchStatuses.mock.calls[0]?.[0]).toEqual(["f1", "f2"]);
    await waitFor(() => expect(screen.getByRole("heading", { level: 1, name: "file-1.pdf" })).toBeTruthy());
    expect(screen.getAllByText("file-2.pdf").length).toBeGreaterThan(0);

    // Still absent after two more polls and more than three seconds: gone.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4500);
    });
    await waitFor(() => expect(screen.queryAllByText("file-2.pdf")).toHaveLength(0));
  });
});

describe("error states and paging failures", () => {
  it("does not claim the library is empty when the first load fails, and retries on request", async () => {
    mocked.listFiles.mockRejectedValueOnce(new Error("offline"));
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1)], total: 1, quota: QUOTA });
    const user = userEvent.setup();
    renderPage();
    expect(await screen.findByText("Your files did not load")).toBeTruthy();
    expect(screen.queryByText("Your library is empty")).toBeNull();
    expect(getToasts().some((t) => t.title === "Could not load files")).toBe(true);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect((await screen.findAllByText("file-1.pdf")).length).toBeGreaterThan(0);
    expect(screen.queryByText("Your files did not load")).toBeNull();
  });

  it("stops paging automatically after a failed page and resumes on retry", async () => {
    const first = Array.from({ length: 100 }, (_, i) => makeFile(i + 1));
    let failSecond = true;
    mocked.listFiles.mockImplementation(async (params) => {
      if (params.page === 1) return { results: first, total: 150, quota: QUOTA };
      if (failSecond) throw new Error("boom");
      return { results: Array.from({ length: 50 }, (_, i) => makeFile(i + 101)), total: 150, quota: QUOTA };
    });
    const user = userEvent.setup();
    renderPage();
    expect(await screen.findByText("Could not load more files.")).toBeTruthy();
    // Give any runaway retry loop time to show itself.
    await new Promise((r) => setTimeout(r, 150));
    expect(mocked.listFiles.mock.calls.filter((c) => c[0].page === 2)).toHaveLength(1);
    failSecond = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("That is everything")).toBeTruthy();
    expect(mocked.listFiles.mock.calls.filter((c) => c[0].page === 2)).toHaveLength(2);
  });
});

describe("rename editor and accessibility", () => {
  it("does not save when the editor is dismissed with Escape (blur must not commit)", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1)], total: 1, quota: QUOTA });
    mocked.patchFile.mockResolvedValue(makeFile(1, { fileName: "never.pdf" }));
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("file-1.pdf");
    await user.click(screen.getAllByRole("button", { name: /^More actions for / })[0] as HTMLElement);
    await user.click(await screen.findByRole("menuitem", { name: "Rename" }));
    const input = await screen.findByLabelText("New file name");
    await user.clear(input);
    await user.type(input, "never.pdf");
    // Escape unmounts the field; a blur dispatched at that moment must be ignored.
    fireEvent.keyDown(input, { key: "Escape" });
    fireEvent.blur(input);
    await waitFor(() => expect(screen.queryByLabelText("New file name")).toBeNull());
    expect(mocked.patchFile).not.toHaveBeenCalled();
    expect(screen.getAllByText("file-1.pdf").length).toBeGreaterThan(0);
  });

  it("gives the retrieval indicator in a row a text alternative and the content a tab panel", async () => {
    mocked.listFiles.mockResolvedValue({
      results: [makeFile(1, { embedStatus: "ready", ragOptOut: true }), makeFile(2, { embedStatus: "ready" })],
      total: 2,
      quota: QUOTA,
    });
    renderPage();
    await screen.findAllByText("file-1.pdf");
    expect(screen.getByText("Left out of smart retrieval")).toBeTruthy();
    expect(screen.getByText("Smart retrieval can use this file")).toBeTruthy();
    expect(screen.getByRole("tabpanel", { name: "Preview" })).toBeTruthy();
    expect(screen.getAllByRole("tab").length).toBe(2);
  });
});

describe("opening files safely", () => {
  it("hands a hostile page type to Open and Download as plain text, never as a page", async () => {
    mocked.listFiles.mockResolvedValue({
      results: [makeFile(1, { fileName: "evil.html", mimeType: "text/html", fileCategory: "code" })],
      total: 1,
      quota: QUOTA,
    });
    mocked.fetchContent.mockImplementation(
      async () => new Response("<script>steal()</script>", { headers: { "content-type": "text/html" } }),
    );
    const created: Blob[] = [];
    Object.assign(URL, {
      createObjectURL: (blob: Blob) => {
        created.push(blob);
        return `blob:${created.length}`;
      },
    });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const user = userEvent.setup();
    renderPage();
    await screen.findAllByText("evil.html");
    const button = await screen.findByRole("button", { name: "Open in a new tab" });
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
    await user.click(button);
    expect(open).toHaveBeenCalledTimes(1);
    const url = open.mock.calls[0]?.[0] as string;
    const blob = created[Number(url.replace("blob:", "")) - 1] as Blob;
    expect(blob.type).toBe("text/plain");
    open.mockRestore();
  });
});

describe("spreadsheet preview", () => {
  it("shows a CSV as a table whose first row is the header", async () => {
    mocked.listFiles.mockResolvedValue({
      results: [makeFile(1, { fileName: "stock.csv", mimeType: "text/csv", fileCategory: "spreadsheet" })],
      total: 1,
      quota: QUOTA,
    });
    mocked.fetchContent.mockImplementation(async () => new Response("name,qty\napple,3\npear,5\n", { headers: { "content-type": "text/csv" } }));
    renderPage();
    expect(await screen.findByRole("columnheader", { name: "name" })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "qty" })).toBeTruthy();
    expect(screen.getByRole("cell", { name: "apple" })).toBeTruthy();
    expect(screen.getByText("3 rows")).toBeTruthy();
  });
});

describe("background reloads", () => {
  it("re-reads the pages already on screen so the selection deep in the list survives a delete", async () => {
    let gone = new Set<string>();
    mocked.listFiles.mockImplementation(async (params) => {
      const all = Array.from({ length: 150 }, (_, i) => makeFile(i + 1)).filter((f) => !gone.has(f.fileID));
      const start = (params.page - 1) * 100;
      return { results: all.slice(start, start + 100), total: all.length, quota: QUOTA };
    });
    mocked.deleteFile.mockImplementation(async (id) => {
      gone = new Set([...gone, id]);
      return { deleted: true, fileID: id, quota: QUOTA };
    });
    const user = userEvent.setup();
    renderPage();
    // The short first screen tops itself up with page 2.
    await waitFor(() => expect(mocked.listFiles.mock.calls.map((c) => c[0].page)).toContain(2));
    await user.click((await screen.findAllByText("file-120.pdf"))[0] as HTMLElement);
    expect(screen.getByRole("heading", { level: 1, name: "file-120.pdf" })).toBeTruthy();

    mocked.listFiles.mockClear();
    await user.click(screen.getByRole("button", { name: "Delete file-3.pdf" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(mocked.deleteFile).toHaveBeenCalledWith("f3"));
    await waitFor(() => expect(mocked.listFiles.mock.calls.map((c) => c[0].page).sort()).toEqual([1, 2]));
    await screen.findByText("That is everything");
    expect(screen.getByRole("heading", { level: 1, name: "file-120.pdf" })).toBeTruthy();
    expect(screen.getAllByText("file-120.pdf").length).toBeGreaterThan(0);
  });
});

describe("row controls name their file", () => {
  it("gives every row its own menu and delete button names", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1), makeFile(2)], total: 2, quota: QUOTA });
    renderPage();
    await screen.findAllByText("file-1.pdf");
    expect(screen.getByRole("button", { name: "More actions for file-1.pdf" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "More actions for file-2.pdf" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete file-2.pdf" })).toBeTruthy();
  });

  it("links each tab to the panel it controls", async () => {
    mocked.listFiles.mockResolvedValue({ results: [makeFile(1)], total: 1, quota: QUOTA });
    renderPage();
    await screen.findAllByText("file-1.pdf");
    const panel = screen.getByRole("tabpanel", { name: "Preview" });
    expect(screen.getByRole("tab", { name: "Preview" }).getAttribute("aria-controls")).toBe(panel.id);
  });

  it("shows the loading skeleton, not the empty library, while a new search is on its way", async () => {
    let release: (() => void) | undefined;
    mocked.listFiles.mockImplementation(async (params) => {
      if (params.q === "zzz") await new Promise<void>((resolve) => (release = resolve));
      return params.q === "zzz" ? { results: [], total: 0, quota: QUOTA } : { results: [], total: 0, quota: QUOTA };
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Your library is empty");
    await user.click(screen.getByRole("button", { name: "Search files" }));
    await user.type(screen.getByPlaceholderText("Search by file name"), "zzz");
    await waitFor(() => expect(screen.getByRole("status", { name: "Loading…" })).toBeTruthy());
    expect(screen.queryByText("Your library is empty")).toBeNull();
    release?.();
    expect(await screen.findByText("Nothing matches")).toBeTruthy();
  });
});
