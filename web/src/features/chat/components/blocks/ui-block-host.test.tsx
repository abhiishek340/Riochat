// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactElement } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";
import { MarkdownContext, type BlockHostApi } from "../markdown/context";
import { UiBlockHost } from "./ui-block-host";

afterEach(cleanup);

function memoryApi(over: Partial<BlockHostApi> = {}): BlockHostApi & { writes: unknown[][] } {
  const store = new Map<string, Record<string, unknown>>();
  const listeners = new Set<() => void>();
  const writes: unknown[][] = [];
  return {
    messageId: "m1",
    readState: (id) => store.get(id),
    writeState: (id, state, title) => {
      writes.push([id, state, title]);
      if (state) store.set(id, state);
      else store.delete(id);
      listeners.forEach((l) => l());
    },
    subscribe: (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    busy: false,
    duplicateKeys: new Set(),
    ...over,
    writes,
  };
}

function view(ui: ReactElement, blocks?: BlockHostApi) {
  return render(
    <I18nProvider>
      <TooltipProvider>
        <MarkdownContext.Provider value={{ scope: "t", streaming: false, readOnly: false, blocks }}>{ui}</MarkdownContext.Provider>
      </TooltipProvider>
    </I18nProvider>,
  );
}

const CALC = JSON.stringify({
  component: "panel",
  id: "calc",
  title: "Loan",
  state: { years: 5 },
  computed: { months: "years * 12" },
  children: [
    { component: "slider", bind: "years", min: 1, max: 30, label: "Years" },
    { component: "text", text: "{months} months" },
  ],
});

describe("UiBlockHost", () => {
  it("shows a skeleton while the block streams", async () => {
    view(<UiBlockHost code='{"component":"chart"' closed={false} />);
    expect(await screen.findByRole("status")).toBeTruthy();
  });

  it("falls back to the source for malformed blocks", async () => {
    view(<UiBlockHost code="not json at all" closed />);
    expect(await screen.findByText(/malformed/i)).toBeTruthy();
  });

  it("renders a live block and recomputes when a control changes, persisting the diff", async () => {
    const api = memoryApi();
    view(<UiBlockHost code={CALC} closed />, api);
    expect(await screen.findByText("60 months")).toBeTruthy();
    fireEvent.change(screen.getByRole("slider"), { target: { value: "10" } });
    expect(await screen.findByText("120 months")).toBeTruthy();
    expect(api.writes.at(-1)).toEqual(["calc", { years: 10 }, "Loan"]);
    fireEvent.click(screen.getByRole("button", { name: /restore/i }));
    expect(await screen.findByText("60 months")).toBeTruthy();
    expect(api.writes.at(-1)).toEqual(["calc", null, "Loan"]);
  });

  it("restores stored state on mount", async () => {
    const api = memoryApi();
    api.writeState("calc", { years: 2 });
    view(<UiBlockHost code={CALC} closed />, api);
    expect(await screen.findByText("24 months")).toBeTruthy();
  });

  it("keeps changes local when no persistence is available", async () => {
    view(<UiBlockHost code={CALC} closed />);
    fireEvent.change(await screen.findByRole("slider"), { target: { value: "3" } });
    expect(await screen.findByText("36 months")).toBeTruthy();
  });

  it("does not persist blocks whose key is duplicated in the message", async () => {
    const api = memoryApi({ duplicateKeys: new Set(["calc"]) });
    view(<UiBlockHost code={CALC} closed />, api);
    fireEvent.change(await screen.findByRole("slider"), { target: { value: "3" } });
    expect(await screen.findByText("36 months")).toBeTruthy();
    expect(api.writes).toHaveLength(0);
  });

  it("sends follow-up questions through the host", async () => {
    const ask = vi.fn();
    const api = memoryApi({ ask });
    const code = JSON.stringify({ component: "panel", id: "q", children: [{ component: "button", label: "Explain", ask: "Why 60?" }] });
    view(<UiBlockHost code={code} closed />, api);
    fireEvent.click(await screen.findByRole("button", { name: "Explain" }));
    expect(ask).toHaveBeenCalledWith("Why 60?");
  });

  it("disables ask buttons while a reply is running", async () => {
    const api = memoryApi({ ask: vi.fn(), busy: true });
    const code = JSON.stringify({ component: "button", label: "Go", ask: "x" });
    view(<UiBlockHost code={code} closed />, api);
    expect((await screen.findByRole("button", { name: "Go" })).hasAttribute("disabled")).toBe(true);
  });

  it("filters a data table with where and a bound control", async () => {
    const code = JSON.stringify({
      component: "panel",
      id: "t",
      state: { max: 10 },
      children: [
        { component: "slider", bind: "max", min: 0, max: 30, label: "Max" },
        { component: "data-table", columns: [{ key: "n", label: "Name" }, { key: "p", label: "Price" }], rows: [{ n: "tea", p: 5 }, { n: "wine", p: 25 }], where: "{row.p <= max}" },
      ],
    });
    view(<UiBlockHost code={code} closed />);
    expect(await screen.findByText("tea")).toBeTruthy();
    expect(screen.queryByText("wine")).toBeNull();
    fireEvent.change(screen.getByRole("slider"), { target: { value: "30" } });
    expect(await screen.findByText("wine")).toBeTruthy();
  });

  it("draws a chart from a per-point formula", async () => {
    const code = JSON.stringify({ component: "chart", type: "line", x: [1, 2, 3], series: [{ name: "double", data: "{x * 2}" }] });
    const { container } = view(<UiBlockHost code={code} closed />);
    expect(await screen.findByText("double")).toBeTruthy();
    expect(container.querySelectorAll("svg path").length).toBeGreaterThan(0);
  });

  it("explains components it cannot draw", async () => {
    view(<UiBlockHost code='{"component":"gantt","tasks":[]}' closed />);
    expect(await screen.findByText(/cannot show "gantt"/)).toBeTruthy();
  });
});
