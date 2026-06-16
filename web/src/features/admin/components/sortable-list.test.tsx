// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import { SortableList } from "./sortable-list";

afterEach(cleanup);

interface Item {
  id: number;
  name: string;
}

function Harness({ onSelect }: { onSelect?: (item: Item) => void }) {
  const [items, setItems] = useState<Item[]>([
    { id: 1, name: "alpha" },
    { id: 2, name: "beta" },
  ]);
  return <SortableList items={items} itemKey={(x) => x.id} onChange={setItems} itemLabel={(x) => x.name} render={(x) => <span>{x.name}</span>} onSelect={onSelect} label="things" />;
}

describe("SortableList", () => {
  it("names the move buttons after their row and announces the new position", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Move up: beta" }));
    expect((await screen.findByRole("status")).textContent).toBe("beta moved to position 1 of 2");
    const names = screen.getAllByRole("listitem").map((li) => li.textContent);
    expect(names).toEqual(["beta", "alpha"]);
  });

  it("selects a row from the keyboard", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    renderWithApp(<Harness onSelect={onSelect} />);
    const row = (await screen.findAllByRole("listitem"))[1] as HTMLElement;
    row.focus();
    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledWith({ id: 2, name: "beta" });
  });
});
