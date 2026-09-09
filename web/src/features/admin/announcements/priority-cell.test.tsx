// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import { PriorityCell } from "./announcements-page";

afterEach(cleanup);

function setup() {
  const onCommit = vi.fn();
  const onInvalid = vi.fn();
  renderWithApp(<PriorityCell value={3} disabled={false} label="Priority" onCommit={onCommit} onInvalid={onInvalid} />);
  return { onCommit, onInvalid, user: userEvent.setup(), input: screen.getByRole("textbox", { name: "Priority" }) };
}

describe("PriorityCell", () => {
  it("commits a new number on Enter", async () => {
    const { user, input, onCommit } = setup();
    await user.clear(input);
    await user.type(input, "9{Enter}");
    expect(onCommit).toHaveBeenCalledWith(9);
  });

  it("discards the draft on Escape instead of committing it", async () => {
    const { user, input, onCommit, onInvalid } = setup();
    await user.clear(input);
    await user.type(input, "12{Escape}");
    expect(onCommit).not.toHaveBeenCalled();
    expect(onInvalid).not.toHaveBeenCalled();
    expect((screen.getByRole("textbox", { name: "Priority" }) as HTMLInputElement).value).toBe("3");
  });

  it("reports junk and drops a blank draft", async () => {
    const { user, input, onCommit, onInvalid } = setup();
    await user.clear(input);
    await user.type(input, "abc{Enter}");
    expect(onInvalid).toHaveBeenCalledTimes(1);
    await user.clear(screen.getByRole("textbox", { name: "Priority" }));
    await user.tab();
    expect(onCommit).not.toHaveBeenCalled();
  });
});
