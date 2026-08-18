// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import { IntCell } from "./sources-table";

afterEach(cleanup);

describe("IntCell", () => {
  it("commits on Enter and reverts a rejected value", async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn(() => false);
    renderWithApp(<IntCell value={4} label="Priority" onCommit={onCommit} />);
    const input = screen.getByRole("textbox", { name: "Priority" }) as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "0{Enter}");
    expect(onCommit).toHaveBeenCalledWith("0");
    expect(input.value).toBe("4");
  });

  it("does not commit what Escape discards", async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn(() => true);
    renderWithApp(<IntCell value={4} label="Priority" onCommit={onCommit} />);
    const input = screen.getByRole("textbox", { name: "Priority" }) as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "9{Escape}");
    expect(onCommit).not.toHaveBeenCalled();
    expect(input.value).toBe("4");
  });
});
