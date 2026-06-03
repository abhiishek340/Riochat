// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n/provider";
import { clearToasts, toastTitles } from "../test-utils";
import { useBulk } from "./use-bulk";
import { useSelection } from "./use-selection";

interface Row {
  id: number;
  role: string;
}

function setup(initial: Row[]) {
  return renderHook(
    () => {
      const [rows, setRows] = useState(initial);
      const selection = useSelection<number>();
      const bulk = useBulk<Row, number>({ getKey: (r) => r.id, setRows: (update) => setRows(update), selection });
      return { rows, selection, bulk };
    },
    { wrapper: ({ children }: { children: ReactNode }) => <I18nProvider>{children}</I18nProvider> },
  );
}

afterEach(() => {
  cleanup();
  clearToasts();
});

describe("useBulk", () => {
  it("skips rows that already match and says so when nothing is left", async () => {
    const { result } = setup([{ id: 1, role: "admin" }]);
    let ok = true;
    await act(async () => {
      ok = await result.current.bulk.run({ targets: result.current.rows, alreadyDone: (r) => r.role === "admin", optimistic: (r) => r, request: async () => undefined, successKey: "adminCommon.toast.bulkDone" });
    });
    expect(ok).toBe(false);
    expect(toastTitles()).toContain("Nothing to change, every selected item already has this value");
  });

  it("rolls failed rows back and narrows the selection to them", async () => {
    const { result } = setup([
      { id: 1, role: "user" },
      { id: 2, role: "user" },
      { id: 3, role: "admin" },
    ]);
    act(() => result.current.selection.set([1, 2, 3]));
    await act(async () => {
      await result.current.bulk.run({
        targets: result.current.rows,
        alreadyDone: (r) => r.role === "admin",
        optimistic: (r) => ({ ...r, role: "admin" }),
        request: async (r) => {
          if (r.id === 2) throw new Error("denied");
          return { ...r, role: "admin" };
        },
        successKey: "adminCommon.toast.bulkDone",
      });
    });
    expect(result.current.rows.map((r) => [r.id, r.role])).toEqual([
      [1, "admin"],
      [2, "user"],
      [3, "admin"],
    ]);
    expect([...result.current.selection.selected]).toEqual([2]);
    expect(toastTitles()).toContain("Some updates failed");
  });

  it("clears the selection after a full success", async () => {
    const { result } = setup([{ id: 1, role: "user" }]);
    act(() => result.current.selection.set([1]));
    await act(async () => {
      await result.current.bulk.run({ targets: result.current.rows, alreadyDone: () => false, optimistic: (r) => ({ ...r, role: "admin" }), request: async () => undefined, successKey: "adminCommon.toast.bulkDone" });
    });
    expect(result.current.selection.count).toBe(0);
    expect(result.current.rows[0]?.role).toBe("admin");
  });
});
