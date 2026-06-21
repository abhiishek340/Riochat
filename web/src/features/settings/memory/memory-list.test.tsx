// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const api = vi.hoisted(() => ({ saveMemory: vi.fn(), deleteMemory: vi.fn() }));
vi.mock("./api", async (orig) => ({ ...(await orig<object>()), saveMemory: api.saveMemory, deleteMemory: api.deleteMemory }));

import { localEntry, type MemoryEntry } from "./api";
import { MemoryList } from "./memory-list";

beforeEach(() => {
  api.saveMemory.mockReset().mockResolvedValue(undefined);
  api.deleteMemory.mockReset().mockResolvedValue(undefined);
  clearToasts();
});
afterEach(cleanup);

const entries = (n: number): MemoryEntry[] => Array.from({ length: n }, (_, i) => localEntry(`k${i}`, `v${i}`, "preference"));

describe("MemoryList", () => {
  it("shows the counter and disables Add at the limit", async () => {
    renderWithApp(<MemoryList entries={entries(20)} loading={false} scope="preference" limit={20} onChange={() => undefined} />);
    expect(await screen.findByText("20 / 20")).toBeTruthy();
    const add = screen.getByRole("button", { name: "Full" }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
  });

  it("shows placeholders while loading", async () => {
    renderWithApp(<MemoryList entries={[]} loading scope="preference" limit={20} onChange={() => undefined} />);
    expect(await screen.findByText("-- / 20")).toBeTruthy();
  });

  it("replaces the value of an existing key in place with a single PUT", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithApp(<MemoryList entries={entries(2)} loading={false} scope="preference" limit={20} onChange={onChange} />);
    await user.click(await screen.findByRole("button", { name: "Add" }));
    await user.type(screen.getByRole("textbox", { name: "Name" }), " k1 ");
    await user.type(screen.getByRole("textbox", { name: "What to remember" }), " fresh ");
    await user.click(screen.getByRole("button", { name: "Save note" }));
    await waitFor(() => expect(onChange).toHaveBeenCalled());
    expect(api.saveMemory).toHaveBeenCalledTimes(1);
    expect(api.saveMemory).toHaveBeenCalledWith({ memoryKey: "k1", value: "fresh", scope: "preference" });
    const next = onChange.mock.calls[0]?.[0] as MemoryEntry[];
    expect(next.map((e) => e.memoryKey)).toEqual(["k0", "k1"]);
    expect(next[1]?.value).toBe("fresh");
  });

  it("edits in place with Enter and cancels with Escape", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithApp(<MemoryList entries={entries(1)} loading={false} scope="preference" onChange={onChange} />);
    await user.click(await screen.findByRole("button", { name: /^Edit k/ }));
    const box = screen.getByRole("textbox", { name: "What to remember" });
    await user.clear(box);
    await user.type(box, "updated{Enter}");
    await waitFor(() => expect(api.saveMemory).toHaveBeenCalledWith({ memoryKey: "k0", value: "updated", scope: "preference" }));
    await user.click(await screen.findByRole("button", { name: /^Edit k/ }));
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("textbox", { name: "What to remember" })).toBeNull();
  });

  it("deletes after a second confirming click", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithApp(<MemoryList entries={entries(2)} loading={false} scope="preference" onChange={onChange} />);
    await user.click((await screen.findAllByRole("button", { name: /^Delete k/ }))[0] as HTMLElement);
    expect(api.deleteMemory).not.toHaveBeenCalled();
    const row = (await screen.findByText("Delete this note?")).closest("li") as HTMLElement;
    await user.click(within(row).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(api.deleteMemory).toHaveBeenCalledWith("k0"));
    expect((onChange.mock.calls[0]?.[0] as MemoryEntry[]).map((e) => e.memoryKey)).toEqual(["k1"]);
  });

  it("reports a failed add and keeps the dialog open", async () => {
    api.saveMemory.mockRejectedValueOnce(new Error("nope"));
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithApp(<MemoryList entries={[]} loading={false} scope="preference" onChange={onChange} />);
    await user.click(await screen.findByRole("button", { name: "Add" }));
    await user.type(screen.getByRole("textbox", { name: "Name" }), "a");
    await user.type(screen.getByRole("textbox", { name: "What to remember" }), "b");
    await user.click(screen.getByRole("button", { name: "Save note" }));
    await waitFor(() => expect(toastTitles()).toContain("Could not save the note"));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("applies a slow save on top of a delete that finished first", async () => {
    let finishSave!: () => void;
    api.saveMemory.mockReturnValueOnce(new Promise<void>((resolve) => (finishSave = resolve)));
    const user = userEvent.setup();
    let seen: MemoryEntry[] = [];
    function Harness() {
      const [list, setList] = useState(entries(2));
      seen = list;
      return <MemoryList entries={list} loading={false} scope="preference" onChange={setList} />;
    }
    renderWithApp(<Harness />);
    // Start editing k0 and submit; the request stays pending.
    await user.click((await screen.findAllByRole("button", { name: /^Edit k/ }))[0] as HTMLElement);
    const box = screen.getByRole("textbox", { name: "What to remember" });
    await user.clear(box);
    await user.type(box, "changed{Enter}");
    await waitFor(() => expect(api.saveMemory).toHaveBeenCalledTimes(1));
    // Meanwhile delete k1.
    await user.click(screen.getAllByRole("button", { name: /^Delete k/ })[0] as HTMLElement);
    const row = (await screen.findByText("Delete this note?")).closest("li") as HTMLElement;
    await user.click(within(row).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(seen.map((e) => e.memoryKey)).toEqual(["k0"]));
    finishSave();
    await waitFor(() => expect(seen[0]?.value).toBe("changed"));
    expect(seen.map((e) => e.memoryKey)).toEqual(["k0"]);
  });
});
