// @vitest-environment jsdom
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import { useSelection } from "../lib/use-selection";
import { CellActions, DataTable, type Column } from "./data-table";
import { Pagination } from "./pagination";

afterEach(cleanup);

interface Row {
  id: number;
  name: string;
}
const rows: Row[] = [
  { id: 1, name: "alpha" },
  { id: 2, name: "beta" },
  { id: 3, name: "gamma" },
];
const columns: Column<Row>[] = [{ id: "name", header: "Name", cell: (r) => r.name }];

function Harness({ expand = new Set<number>() }: { expand?: ReadonlySet<number> }) {
  const selection = useSelection<number>();
  return (
    <>
      <DataTable columns={columns} rows={rows} rowKey={(r) => r.id} selection={selection} label="people" renderExpanded={(r) => (expand.has(r.id) ? <p>details of {r.name}</p> : null)} />
      <output data-testid="count">{selection.count}</output>
    </>
  );
}

function RowLabelHarness() {
  const selection = useSelection<number>();
  return <DataTable columns={columns} rows={rows} rowKey={(r) => r.id} selection={selection} rowLabel={(r) => r.name} />;
}

describe("DataTable", () => {
  it("selects single rows and every row from the header", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness />);
    const boxes = await screen.findAllByRole("checkbox");
    expect(boxes).toHaveLength(4);
    await user.click(boxes[1] as HTMLElement);
    expect(screen.getByTestId("count").textContent).toBe("1");
    await user.click(boxes[0] as HTMLElement);
    expect(screen.getByTestId("count").textContent).toBe("3");
    await user.click(boxes[0] as HTMLElement);
    expect(screen.getByTestId("count").textContent).toBe("0");
  });

  it("shows an expanded row only under the rows that ask for it", async () => {
    renderWithApp(<Harness expand={new Set([2])} />);
    expect(await screen.findByText("details of beta")).toBeTruthy();
    expect(screen.queryByText("details of alpha")).toBeNull();
  });

  it("names each row checkbox after the row when asked", async () => {
    renderWithApp(<RowLabelHarness />);
    expect(await screen.findByRole("checkbox", { name: "Select alpha" })).toBeTruthy();
    expect(screen.getByRole("checkbox", { name: "Select gamma" })).toBeTruthy();
  });

  it("replaces the empty row with a retry notice when the load failed", async () => {
    const user = userEvent.setup();
    const retry = vi.fn();
    renderWithApp(<DataTable columns={columns} rows={[]} rowKey={(r: Row) => r.id} empty="Nothing here" error onRetry={retry} />);
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("Nothing here")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalledTimes(1);
  });

  it("keeps showing rows that are already loaded when a refresh fails", async () => {
    renderWithApp(<DataTable columns={columns} rows={rows} rowKey={(r: Row) => r.id} error onRetry={() => undefined} />);
    expect(await screen.findByText("alpha")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("opens a clickable row from the keyboard but leaves keys aimed at inner controls alone", async () => {
    const user = userEvent.setup();
    const open = vi.fn();
    const inner: Column<Row>[] = [...columns, { id: "act", header: "Act", cell: () => (
      <CellActions>
        <button type="button">inner</button>
      </CellActions>
    ) }];
    renderWithApp(<DataTable columns={inner} rows={rows} rowKey={(r: Row) => r.id} onRowClick={open} />);
    const first = (await screen.findAllByRole("row"))[1] as HTMLElement;
    first.focus();
    await user.keyboard("{Enter}");
    expect(open).toHaveBeenCalledTimes(1);
    expect(open).toHaveBeenCalledWith(rows[0]);
    const buttons = screen.getAllByRole("button", { name: "inner" });
    (buttons[0] as HTMLElement).focus();
    await user.keyboard("{Enter}");
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("shows the empty text for no rows", async () => {
    renderWithApp(<DataTable columns={columns} rows={[]} rowKey={(r: Row) => r.id} empty="Nothing here" />);
    expect(await screen.findByText("Nothing here")).toBeTruthy();
  });
});

describe("Pagination", () => {
  it("moves between pages and disables the ends", async () => {
    const user = userEvent.setup();
    const onPage = vi.fn();
    function Wrapper() {
      const [page, setPage] = useState(1);
      return <Pagination page={page} pageSize={25} total={60} onPage={(p) => { onPage(p); setPage(p); }} />;
    }
    renderWithApp(<Wrapper />);
    const group = await screen.findByText(/Page 1 of 3/);
    expect(group).toBeTruthy();
    const previous = screen.getByRole("button", { name: "Previous page" }) as HTMLButtonElement;
    const next = screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement;
    expect(previous.disabled).toBe(true);
    await user.click(next);
    expect(onPage).toHaveBeenCalledWith(2);
    await user.click(next);
    await user.click(next);
    expect(onPage).toHaveBeenLastCalledWith(3);
    expect((screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement).disabled).toBe(true);
    expect(within(document.body).getByText(/Page 3 of 3/)).toBeTruthy();
  });
});
