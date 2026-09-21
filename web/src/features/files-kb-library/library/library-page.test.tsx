// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui";
import { dismiss, getToasts } from "@/components/ui/toast-store";
import type { LibraryItem } from "./model";

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());
vi.mock("../shared/session", () => ({ hasSession: async () => true }));
vi.mock("./api", async () => {
  const actual = (await vi.importActual("./api")) as Record<string, unknown>;
  return {
    ...actual,
    listMine: vi.fn(),
    listVisible: vi.fn(),
    getItem: vi.fn(),
    createItem: vi.fn(),
    updateItem: vi.fn(),
    setItemEnabled: vi.fn(),
    deleteItem: vi.fn(),
  };
});

import * as api from "./api";
import { LibraryPage } from "./library-page";

const m = vi.mocked(api);

function item(id: number, over: Partial<LibraryItem> = {}): LibraryItem {
  return {
    id,
    scope: "user",
    title: `item${id}`,
    trigger: `item${id}`,
    description: `About item ${id}`,
    enabled: true,
    sortOrder: 0,
    createdAt: "",
    updatedAt: "",
    ...over,
  };
}

const empty = { items: [], total: 0 };

function renderPage() {
  return render(
    <TooltipProvider>
      <LibraryPage />
    </TooltipProvider>,
  );
}

let observed: IntersectionObserverCallback | null = null;

beforeEach(() => {
  vi.clearAllMocks();
  observed = null;
  class FakeObserver {
    constructor(cb: IntersectionObserverCallback) {
      observed = cb;
    }
    observe() {}
    disconnect() {}
    unobserve() {}
    takeRecords() {
      return [];
    }
  }
  vi.stubGlobal("IntersectionObserver", FakeObserver);
  m.listMine.mockResolvedValue(empty);
  m.listVisible.mockResolvedValue(empty);
});
afterEach(() => {
  cleanup();
  dismiss();
  vi.unstubAllGlobals();
});

describe("skills tab", () => {
  it("lists own skills above built-in ones and shows the empty state otherwise", async () => {
    m.listMine.mockResolvedValue({ items: [item(1, { title: "mine", trigger: "mine" })], total: 1 });
    m.listVisible.mockResolvedValue({ items: [item(2, { scope: "builtin", trigger: "shared" }), item(1)], total: 2 });
    renderPage();
    const names = await screen.findAllByText(/^\/(mine|shared)$/);
    expect(names.map((n) => n.textContent)).toEqual(["/mine", "/shared"]);
  });

  it("shows the empty state", async () => {
    renderPage();
    expect(await screen.findByText("No skills of your own yet")).toBeTruthy();
  });

  it("creates a skill with the normalised name (scenario 28)", async () => {
    m.createItem.mockImplementation(async (_kind, w) => item(5, { title: w.name, trigger: w.name, body: w.body }));
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("No skills of your own yet");
    await user.click(screen.getByRole("button", { name: "New" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText(/Name/), "/review");
    await user.type(within(dialog).getByLabelText(/SKILL\.md/), "x");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(m.createItem).toHaveBeenCalledWith("skills", { name: "review", description: "", body: "x", enabled: true }));
    expect(await screen.findByText("/review")).toBeTruthy();
    expect(getToasts().some((t) => t.title === "Skill created")).toBe(true);
  });

  it("validates before sending anything (scenario 29)", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("No skills of your own yet");
    await user.click(screen.getByRole("button", { name: "New" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText(/Name/), "review");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(getToasts().some((t) => t.title === "Enter a name and the SKILL.md text.")).toBe(true);
    await user.clear(within(dialog).getByLabelText(/Name/));
    await user.type(within(dialog).getByLabelText(/Name/), "a".repeat(65));
    await user.type(within(dialog).getByLabelText(/SKILL\.md/), "x");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(getToasts().some((t) => t.title.startsWith("A name can have up to 64 characters"))).toBe(true);
    expect(m.createItem).not.toHaveBeenCalled();
  });

  it("flips the switch at once, reorders, and rolls back on failure (scenario 30)", async () => {
    m.listMine.mockResolvedValue({ items: [item(2, { trigger: "alpha" }), item(1, { trigger: "beta" })], total: 2 });
    let release: (v: LibraryItem) => void = () => undefined;
    m.setItemEnabled.mockImplementation(() => new Promise<LibraryItem>((_, reject) => (release = () => reject(new Error("server says no")))));
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("/alpha");
    await user.click(screen.getByRole("switch", { name: "Turn alpha on or off" }));
    // Optimistic: alpha dims and drops below beta.
    await waitFor(() => expect(screen.getAllByText(/^\/(alpha|beta)$/).map((n) => n.textContent)).toEqual(["/beta", "/alpha"]));
    await act(async () => release(item(1)));
    await waitFor(() => expect(getToasts().some((t) => t.title === "Could not update the skill")).toBe(true));
    await waitFor(() => expect(screen.getAllByText(/^\/(alpha|beta)$/).map((n) => n.textContent)).toEqual(["/alpha", "/beta"]));
  });

  it("opens a built-in skill read-only after fetching the full object (scenario 31)", async () => {
    m.listVisible.mockResolvedValue({ items: [item(9, { scope: "builtin", trigger: "docs" })], total: 1 });
    m.getItem.mockResolvedValue(item(9, { scope: "builtin", trigger: "docs", body: "# Full SKILL text" }));
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByText("/docs"));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("# Full SKILL text")).toBeTruthy();
    expect(m.getItem).toHaveBeenCalledWith("skills", 9);
    expect(within(dialog).queryByRole("textbox")).toBeNull();
  });

  it("filters by keyword and reports no matches (scenario 33)", async () => {
    m.listMine.mockResolvedValue({
      items: [item(1, { trigger: "tidy", description: "Refactor code" }), item(2, { trigger: "other", description: "Something else" })],
      total: 2,
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("/tidy");
    await user.type(screen.getByPlaceholderText("Search skills"), "REFACTOR");
    expect(screen.getByText("/tidy")).toBeTruthy();
    expect(screen.queryByText("/other")).toBeNull();
    await user.clear(screen.getByPlaceholderText("Search skills"));
    await user.type(screen.getByPlaceholderText("Search skills"), "zzz");
    expect(screen.getByText("No matching skills")).toBeTruthy();
  });
});

describe("prompts tab", () => {
  async function openPrompts() {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("No skills of your own yet");
    await user.click(screen.getByRole("tab", { name: "Prompts" }));
    return user;
  }

  it("pages through the user's prompts, stops after a failed page and resumes on Retry (scenario 32)", async () => {
    const first = Array.from({ length: 100 }, (_, i) => item(i + 1, { trigger: `p${i + 1}`, body: "c" }));
    const rest = Array.from({ length: 50 }, (_, i) => item(i + 101, { trigger: `p${i + 101}`, body: "c" }));
    let failNext = true;
    m.listMine.mockImplementation(async (kind, params) => {
      if (kind === "skills") return empty;
      if (params.page === 1) return { items: first, total: 150 };
      if (failNext) {
        failNext = false;
        throw new Error("page two broke");
      }
      return { items: rest, total: 150 };
    });
    const user = await openPrompts();
    await screen.findByText("/p1");
    expect(m.listMine.mock.calls.filter((c) => c[0] === "prompts").map((c) => c[1].page)).toEqual([1]);

    await act(async () => observed?.([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver));
    expect(await screen.findByRole("button", { name: "Try again" })).toBeTruthy();
    expect(getToasts().some((t) => t.title === "Could not load prompts")).toBe(true);

    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("/p150");
    expect(m.listMine.mock.calls.filter((c) => c[0] === "prompts").map((c) => c[1].page)).toEqual([1, 2, 2]);
  });

  it("restarts from page 1 with the typed search after the debounce", async () => {
    const user = await openPrompts();
    m.listMine.mockClear();
    await user.type(screen.getByPlaceholderText("Search prompts"), "refactor");
    await waitFor(() => expect(m.listMine.mock.calls.some((c) => c[0] === "prompts" && c[1].q === "refactor" && c[1].page === 1)).toBe(true));
  });

  it("deletes after confirmation and closes at once", async () => {
    m.listMine.mockImplementation(async (kind) => (kind === "prompts" ? { items: [item(1, { trigger: "gone", body: "c" })], total: 1 } : empty));
    m.deleteItem.mockResolvedValue(undefined);
    const user = await openPrompts();
    await screen.findByText("/gone");
    await user.click(screen.getByRole("button", { name: "Delete gone" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(m.deleteItem).toHaveBeenCalledWith("prompts", 1));
    await waitFor(() => expect(screen.queryByText("/gone")).toBeNull());
    expect(getToasts().some((t) => t.title === "Prompt deleted")).toBe(true);
  });
});

describe("load failures and semantics", () => {
  it("does not show the 'no skills yet' empty state when loading failed, and retries on request", async () => {
    m.listMine.mockRejectedValueOnce(new Error("offline"));
    m.listMine.mockResolvedValue({ items: [item(1, { title: "back", trigger: "back" })], total: 1 });
    const user = userEvent.setup();
    renderPage();
    expect(await screen.findByText("The list did not load")).toBeTruthy();
    expect(screen.queryByText("No skills of your own yet")).toBeNull();
    expect(getToasts().some((t) => t.title === "Could not load skills")).toBe(true);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("/back")).toBeTruthy();
    expect(screen.queryByText("The list did not load")).toBeNull();
  });

  it("does the same for prompts and keeps the list a list of items with their own buttons", async () => {
    m.listMine.mockImplementation(async (kind) => {
      if (kind === "prompts") return { items: [item(9, { title: "greet", trigger: "greet" })], total: 1 };
      return empty;
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("No skills of your own yet");
    await user.click(screen.getByRole("tab", { name: "Prompts" }));
    expect(await screen.findByText("/greet")).toBeTruthy();
    const panel = screen.getByRole("tabpanel", { name: "Prompts" });
    // The active tab names and controls the panel it opens.
    expect(screen.getByRole("tab", { name: "Prompts" }).getAttribute("aria-controls")).toBe(panel.id);
    const [card] = screen.getAllByRole("listitem");
    expect(within(card as HTMLElement).getByRole("button", { name: /^\/greet/ })).toBeTruthy();
    expect(within(card as HTMLElement).getByRole("switch", { name: "Turn greet on or off" })).toBeTruthy();
  });

  it("accepts a 64 character name made of emoji, as the server counts characters", async () => {
    m.createItem.mockImplementation(async (_kind, w) => item(5, { title: w.name, trigger: w.name, body: w.body }));
    const user = userEvent.setup();
    renderPage();
    await screen.findByText("No skills of your own yet");
    await user.click(screen.getByRole("button", { name: "New" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText(/Name/), "😀".repeat(64));
    await user.type(within(dialog).getByLabelText(/SKILL\.md/), "x");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(m.createItem).toHaveBeenCalledTimes(1));
    expect(getToasts().some((t) => t.title.startsWith("A name can have up to"))).toBe(false);
  });
});
