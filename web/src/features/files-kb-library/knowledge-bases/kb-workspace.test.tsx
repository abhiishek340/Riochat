// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui";
import { dismiss, getToasts } from "@/components/ui/toast-store";
import type { KnowledgeBase, KnowledgeBaseFile } from "../shared/types";

const router = { replace: vi.fn(), push: vi.fn() };
const policy = { value: { policy: { knowledgeBaseEnabled: true }, loaded: true } };

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());
vi.mock("next/navigation", () => ({ useRouter: () => router }));
vi.mock("@/hooks/use-media-query", () => ({ useIsMobile: () => false, useMediaQuery: () => false }));
vi.mock("@/lib/session/feature-policy", () => ({ useFeaturePolicy: () => policy.value }));
vi.mock("../shared/session", () => ({ hasSession: async () => true }));
vi.mock("./api", async () => {
  const actual = (await vi.importActual("./api")) as Record<string, unknown>;
  return {
    ...actual,
    listKnowledgeBases: vi.fn(),
    getKnowledgeBase: vi.fn(),
    createKnowledgeBase: vi.fn(),
    updateKnowledgeBase: vi.fn(),
    deleteKnowledgeBase: vi.fn(),
    listKbFiles: vi.fn(),
    listAvailableFiles: vi.fn(),
    addFilesToKb: vi.fn(),
    removeFileFromKb: vi.fn(),
    fetchKbContent: vi.fn(),
    fetchKbSnapshot: vi.fn(),
    uploadPlatformFile: vi.fn(),
    deletePlatformFile: vi.fn(),
    submitKbEmbeddings: vi.fn(),
  };
});
vi.mock("../files/api", () => ({ uploadFile: vi.fn() }));

import * as api from "./api";
import { KnowledgeBaseWorkspace } from "./kb-workspace";

const m = vi.mocked(api);

function kb(id: string, over: Partial<KnowledgeBase> = {}): KnowledgeBase {
  return {
    publicID: id,
    name: `Base ${id}`,
    description: "",
    enabled: true,
    scope: "user",
    fileCount: 0,
    readyFileCount: 0,
    processingFileCount: 0,
    revision: 1,
    sortOrder: 0,
    createdAt: "",
    updatedAt: "",
    ...over,
  };
}

function kbFile(n: number, over: Partial<KnowledgeBaseFile> = {}): KnowledgeBaseFile {
  return {
    fileID: `file${n}`,
    fileName: `doc-${n}.pdf`,
    mimeType: "application/pdf",
    detectedMIME: "application/pdf",
    fileCategory: "pdf",
    sizeBytes: 2048,
    processingStatus: "ready",
    processingReady: true,
    extractStatus: "ready",
    embedStatus: "ready",
    embedError: "",
    chunkCount: 12,
    canVectorize: false,
    vectorizationReason: "",
    ragOptOut: false,
    createdAt: "",
    updatedAt: "",
    ...over,
  };
}

function renderWorkspace(mode: "user" | "admin" = "user") {
  return render(
    <TooltipProvider>
      <KnowledgeBaseWorkspace mode={mode} />
    </TooltipProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  policy.value = { policy: { knowledgeBaseEnabled: true }, loaded: true };
  m.getKnowledgeBase.mockRejectedValue(new Error("not found"));
  m.listKbFiles.mockResolvedValue({ results: [], total: 0 });
  m.fetchKbSnapshot.mockResolvedValue({ knowledgeBase: kb("x"), statuses: [] });
  m.listAvailableFiles.mockResolvedValue({ results: [], total: 0 });
});
afterEach(() => {
  cleanup();
  dismiss();
});

describe("access (scenario 17)", () => {
  it("renders nothing and redirects home when the policy is off", async () => {
    policy.value = { policy: { knowledgeBaseEnabled: false }, loaded: true };
    const { container } = renderWorkspace();
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith("/"));
    expect(container.textContent).toBe("");
    expect(m.listKnowledgeBases).not.toHaveBeenCalled();
  });
  it("renders nothing until the policy has loaded", () => {
    policy.value = { policy: { knowledgeBaseEnabled: true }, loaded: false };
    const { container } = renderWorkspace();
    expect(container.textContent).toBe("");
    expect(router.replace).not.toHaveBeenCalled();
  });
});

describe("list and detail", () => {
  it("selects the first base and shows its files with retrieval labels (scenario 27)", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a", { fileCount: 2, readyFileCount: 1 })], total: 1 });
    m.listKbFiles.mockResolvedValue({
      results: [kbFile(1), kbFile(2, { fileName: "late.pdf", embedStatus: "queued", chunkCount: 0 })],
      total: 2,
    });
    renderWorkspace();
    expect(await screen.findByText("doc-1.pdf")).toBeTruthy();
    expect(screen.getByText("Searchable")).toBeTruthy();
    expect(screen.getByText("Building index")).toBeTruthy();
    expect(screen.getByText("1 of 2 sources searchable")).toBeTruthy();
    expect(m.listKnowledgeBases.mock.calls[0]).toEqual(["user", { page: 1, pageSize: 50, q: "", sort: "default" }, expect.anything()]);
  });

  it("makes built-in bases view-only for users (scenario 19)", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("b", { scope: "builtin", name: "Handbook", fileCount: 1, readyFileCount: 1 })], total: 1 });
    m.listKbFiles.mockResolvedValue({ results: [kbFile(1, { canVectorize: true })], total: 1 });
    renderWorkspace();
    await screen.findByText("doc-1.pdf");
    expect(screen.getAllByText("Built-in").length).toBeGreaterThan(0);
    expect((screen.getByRole("checkbox", { name: "Select Handbook" }) as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "Add files" })).toBeNull();
    expect(screen.queryByRole("button", { name: /out of this knowledge base/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Build index/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Delete knowledge base/ })).toBeNull();
  });

  it("shows the empty state", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [], total: 0 });
    renderWorkspace();
    expect(await screen.findByText("No knowledge bases yet")).toBeTruthy();
    expect(screen.getByText("Choose a knowledge base")).toBeTruthy();
  });
});

describe("create (scenario 18)", () => {
  it("trims the payload, reloads and selects the new base", async () => {
    m.listKnowledgeBases.mockResolvedValueOnce({ results: [], total: 0 });
    m.createKnowledgeBase.mockResolvedValue(kb("new", { name: "Docs", description: "intro" }));
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("new", { name: "Docs", description: "intro" })], total: 1 });
    const user = userEvent.setup();
    renderWorkspace();
    await screen.findByText("No knowledge bases yet");
    await user.click(screen.getByRole("button", { name: "New" }));
    const dialog = await screen.findByRole("dialog");
    const save = within(dialog).getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await user.type(within(dialog).getByLabelText(/Name/), "   ");
    expect(save.disabled).toBe(true);
    await user.type(within(dialog).getByLabelText("Description"), " intro ");
    await user.clear(within(dialog).getByLabelText(/Name/));
    await user.type(within(dialog).getByLabelText(/Name/), "Docs{Enter}");
    await waitFor(() => expect(m.createKnowledgeBase).toHaveBeenCalledWith("user", { name: "Docs", description: "intro" }));
    await waitFor(() => expect(getToasts().some((t) => t.title === "Knowledge base saved")).toBe(true));
    expect((await screen.findAllByText("Docs")).length).toBeGreaterThan(0);
  });
});

describe("delete (scenario 22)", () => {
  it("sends delete_files only when the option is ticked and only offers it for bases with files", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a", { name: "Full", fileCount: 3, readyFileCount: 3 })], total: 1 });
    m.deleteKnowledgeBase.mockResolvedValue({ deleted: true });
    const user = userEvent.setup();
    renderWorkspace();
    await screen.findByText("Full", { selector: "span" });
    await user.click(screen.getByRole("button", { name: /^Delete knowledge base / }));
    let dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("checkbox"));
    m.listKnowledgeBases.mockResolvedValue({ results: [], total: 0 });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(m.deleteKnowledgeBase).toHaveBeenCalledWith("user", "a", true));
    await waitFor(() => expect(getToasts().some((t) => t.title === "Knowledge base deleted")).toBe(true));

    cleanup();
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("e", { name: "Hollow" })], total: 1 });
    renderWorkspace();
    await screen.findByText("Hollow", { selector: "span" });
    await user.click(screen.getByRole("button", { name: /^Delete knowledge base / }));
    dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("checkbox")).toBeNull();
  });
});

describe("add files (scenario 20)", () => {
  it("adds the chosen files and refreshes", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a")], total: 1 });
    m.listAvailableFiles.mockResolvedValue({ results: [kbFile(1), kbFile(2), kbFile(3)], total: 3 });
    m.addFilesToKb.mockResolvedValue({ updated: true });
    const user = userEvent.setup();
    renderWorkspace();
    await user.click(await screen.findByRole("button", { name: "Add files" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("doc-1.pdf");
    for (const n of [1, 2, 3]) await user.click(within(dialog).getByRole("checkbox", { name: `Select doc-${n}.pdf` }));
    expect(within(dialog).getByText("3/100 selected")).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "Add to knowledge base" }));
    await waitFor(() => expect(m.addFilesToKb).toHaveBeenCalledWith("user", "a", ["file1", "file2", "file3"]));
    await waitFor(() => expect(getToasts().some((t) => t.title === "Files added")).toBe(true));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("blocks the 101st file", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a")], total: 1 });
    m.listAvailableFiles.mockResolvedValue({ results: Array.from({ length: 101 }, (_, i) => kbFile(i + 1)), total: 101 });
    renderWorkspace();
    fireEvent.click(await screen.findByRole("button", { name: "Add files" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("doc-1.pdf");
    for (let n = 1; n <= 100; n += 1) fireEvent.click(within(dialog).getByRole("checkbox", { name: `Select doc-${n}.pdf` }));
    expect(within(dialog).getByText("100/100 selected")).toBeTruthy();
    expect((within(dialog).getByRole("checkbox", { name: "Select doc-101.pdf" }) as HTMLInputElement).disabled).toBe(true);
  }, 40_000);

  it("uploads, adds the successes and reports the failures (scenario 21)", async () => {
    const { uploadFile } = await import("../files/api");
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a")], total: 1 });
    vi.mocked(uploadFile).mockImplementation(async (file) => {
      if (file.name === "bad.txt") throw new Error("too big");
      return { file: { fileID: `up-${file.name}` }, quota: null, reused: false } as never;
    });
    m.addFilesToKb.mockResolvedValue({ updated: true });
    const user = userEvent.setup();
    renderWorkspace();
    await user.click(await screen.findByRole("button", { name: "Add files" }));
    const dialog = await screen.findByRole("dialog");
    const files = ["a.txt", "b.txt", "c.txt", "bad.txt"].map((n) => new File(["x"], n, { type: "text/plain" }));
    await user.upload(within(dialog).getByTestId("kb-file-picker"), files);
    await waitFor(() => expect(m.addFilesToKb).toHaveBeenCalledWith("user", "a", ["up-a.txt", "up-b.txt", "up-c.txt"]));
    await waitFor(() => expect(getToasts().some((t) => t.title === "3 files uploaded and added")).toBe(true));
    expect(getToasts().find((t) => t.title === "Some uploads failed")?.description).toBe("3 uploaded, 1 failed. too big");
  });
});

describe("admin mode (scenario 25)", () => {
  it("toggles availability with PATCH enabled:false and shows the off badge", async () => {
    let current = kb("g", { scope: "builtin", name: "Guide", enabled: true });
    m.listKnowledgeBases.mockImplementation(async () => ({ results: [current], total: 1 }));
    m.updateKnowledgeBase.mockImplementation(async (_mode, _id, patch) => {
      current = { ...current, ...patch } as KnowledgeBase;
      return current;
    });
    const user = userEvent.setup();
    renderWorkspace("admin");
    const toggle = await screen.findByRole("switch", { name: "Knowledge base availability" });
    expect(screen.getByText("Built-in knowledge bases")).toBeTruthy();
    await user.click(toggle);
    await waitFor(() => expect(m.updateKnowledgeBase).toHaveBeenCalledWith("admin", "g", { enabled: false }));
    await waitFor(() => expect(getToasts().some((t) => t.title === "Built-in knowledge base switched off")).toBe(true));
    const sidebar = screen.getByRole("complementary", { name: "Knowledge bases" });
    expect(await within(sidebar).findByText("Off")).toBeTruthy();
    // Admins can edit built-in bases.
    expect(screen.getByRole("button", { name: "Add files" })).toBeTruthy();
  });
});

describe("polling (scenario 24)", () => {
  afterEach(() => vi.useRealTimers());
  it("replaces the list row when the snapshot reports a new revision", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a", { fileCount: 1, readyFileCount: 0, processingFileCount: 1 })], total: 1 });
    m.listKbFiles.mockResolvedValue({ results: [kbFile(1, { embedStatus: "queued", chunkCount: 0, processing: true } as never)], total: 1 });
    m.fetchKbSnapshot.mockResolvedValue({
      knowledgeBase: kb("a", { fileCount: 1, readyFileCount: 1, processingFileCount: 0, revision: 2 }),
      statuses: [
        { fileID: "file1", embedStatus: "ready", chunkCount: 5, processing: false, processingReady: true, processingStatus: "ready", extractStatus: "ready", updatedAt: "now" },
      ],
    });
    renderWorkspace();
    await screen.findByText("1 source not ready yet");
    await vi.advanceTimersByTimeAsync(2600);
    expect(m.fetchKbSnapshot).toHaveBeenCalledWith("user", "a", ["file1"], expect.anything());
    expect(await screen.findByText("1 source, searchable")).toBeTruthy();
    expect(await screen.findByText("Searchable")).toBeTruthy();
  });
});

describe("error states", () => {
  it("offers a retry when the list of bases cannot be loaded", async () => {
    m.listKnowledgeBases.mockRejectedValueOnce(new Error("offline"));
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a", { name: "Back again" })], total: 1 });
    const user = userEvent.setup();
    renderWorkspace();
    // A failed load must not pretend the account simply has no bases.
    expect(await screen.findByText("Knowledge bases did not load")).toBeTruthy();
    expect(screen.queryByText("No knowledge bases yet")).toBeNull();
    expect(getToasts().some((t) => t.title === "Could not load knowledge bases")).toBe(true);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect((await screen.findAllByText("Back again")).length).toBeGreaterThan(0);
    expect(screen.queryByText("Knowledge bases did not load")).toBeNull();
  });

  it("offers a retry when the files of a base cannot be loaded", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a")], total: 1 });
    m.listKbFiles.mockRejectedValueOnce(new Error("offline"));
    m.listKbFiles.mockResolvedValue({ results: [kbFile(1)], total: 1 });
    const user = userEvent.setup();
    renderWorkspace();
    expect(await screen.findByText("The files did not load")).toBeTruthy();
    expect(screen.queryByText("Nothing in here yet")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("doc-1.pdf")).toBeTruthy();
  });

  it("offers a retry when the files that could be added cannot be loaded", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a")], total: 1 });
    m.listAvailableFiles.mockRejectedValueOnce(new Error("offline"));
    m.listAvailableFiles.mockResolvedValue({ results: [kbFile(7)], total: 1 });
    const user = userEvent.setup();
    renderWorkspace();
    await user.click(await screen.findByRole("button", { name: "Add files" }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("The list did not load.")).toBeTruthy();
    expect(within(dialog).queryByText("No files left to add")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Try again" }));
    expect(await within(dialog).findByText("doc-7.pdf")).toBeTruthy();
  });
});

describe("add dialog keyboard and semantics", () => {
  it("is a plain list whose checkboxes are the controls; Enter and Space toggle, the row click does too", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a")], total: 1 });
    m.listAvailableFiles.mockResolvedValue({ results: [kbFile(1), kbFile(2), kbFile(3)], total: 3 });
    const user = userEvent.setup();
    renderWorkspace();
    await user.click(await screen.findByRole("button", { name: "Add files" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("doc-1.pdf");
    // No listbox/option roles wrapping interactive children.
    expect(within(dialog).queryByRole("listbox")).toBeNull();
    expect(within(dialog).queryByRole("option")).toBeNull();
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(3);

    const first = within(dialog).getByRole("checkbox", { name: "Select doc-1.pdf" }) as HTMLInputElement;
    first.focus();
    await user.keyboard("{Enter}");
    expect(first.checked).toBe(true);
    await user.keyboard(" ");
    expect(first.checked).toBe(false);
    await user.click(within(dialog).getByText("doc-2.pdf"));
    expect((within(dialog).getByRole("checkbox", { name: "Select doc-2.pdf" }) as HTMLInputElement).checked).toBe(true);
    expect(within(dialog).getByText("1/100 selected")).toBeTruthy();
  });

  it("names each platform-source delete button after its file in admin mode", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("g", { scope: "builtin" })], total: 1 });
    m.listAvailableFiles.mockResolvedValue({ results: [kbFile(1), kbFile(2)], total: 2 });
    const user = userEvent.setup();
    renderWorkspace("admin");
    await user.click(await screen.findByRole("button", { name: "Add files" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("doc-1.pdf");
    expect(within(dialog).getByRole("button", { name: "Delete source doc-1.pdf" })).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "Delete source doc-2.pdf" })).toBeTruthy();
  });
});

describe("vectorize then poll", () => {
  afterEach(() => vi.useRealTimers());
  it("keeps polling a queued file even though the list said processing:false", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a", { fileCount: 1 })], total: 1 });
    m.listKbFiles.mockResolvedValue({
      results: [kbFile(1, { embedStatus: "none", chunkCount: 0, canVectorize: true, processing: false } as never)],
      total: 1,
    });
    m.submitKbEmbeddings.mockResolvedValue({ submittedFileIDs: ["file1"], skipped: [] });
    m.fetchKbSnapshot.mockResolvedValue({
      knowledgeBase: kb("a", { fileCount: 1 }),
      statuses: [{ fileID: "file1", embedStatus: "ready", chunkCount: 4, processing: false, processingReady: true, processingStatus: "ready", extractStatus: "ready", updatedAt: "now" }],
    });
    renderWorkspace();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await user.click(await screen.findByRole("button", { name: /^Build index for / }));
    expect(m.submitKbEmbeddings).toHaveBeenCalledWith("user", ["file1"]);
    expect(await screen.findByText("Building index")).toBeTruthy();
    await vi.advanceTimersByTimeAsync(2600);
    // The poll asks about the file we just queued, and the answer lands.
    expect(m.fetchKbSnapshot).toHaveBeenCalledWith("user", "a", ["file1"], expect.anything());
    expect(await screen.findByText("Searchable")).toBeTruthy();
  });
});

describe("visible reloads keep the open base", () => {
  it("keeps the selected base after a new search when it is still among the results", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a", { name: "Alpha" }), kb("b", { name: "Beta" })], total: 2 });
    const user = userEvent.setup();
    renderWorkspace();
    await user.click(await screen.findByRole("button", { name: /^Beta/ }));
    expect(screen.getByRole("heading", { level: 1, name: "Beta" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Search knowledge bases" }));
    await user.type(screen.getByRole("textbox"), "a");
    await waitFor(() => expect(m.listKnowledgeBases.mock.calls.some((c) => c[1].q === "a")).toBe(true));
    await waitFor(() => expect(m.listKnowledgeBases.mock.calls.length).toBeGreaterThan(1));
    expect(screen.getByRole("heading", { level: 1, name: "Beta" })).toBeTruthy();
  });

  it("names the per-file buttons after the file", async () => {
    m.listKnowledgeBases.mockResolvedValue({ results: [kb("a", { fileCount: 1 })], total: 1 });
    m.listKbFiles.mockResolvedValue({ results: [kbFile(1, { canVectorize: true })], total: 1 });
    renderWorkspace();
    expect(await screen.findByRole("button", { name: "Build index for doc-1.pdf" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Take doc-1.pdf out of this knowledge base" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "More actions for Base a" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete knowledge base Base a" })).toBeTruthy();
  });
});
