// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test-utils";
import { readConversation } from "./model";

const api = vi.hoisted(() => ({
  createShare: vi.fn(),
  fetchShare: vi.fn(),
  regenerateTitle: vi.fn(),
  renameConversation: vi.fn(),
  revokeShare: vi.fn(),
  setLabels: vi.fn(),
}));
vi.mock("./api", () => api);

import { RenameDialog, ShareDialog } from "./dialogs";

const row = (title: string) => readConversation({ publicID: "c1", title, status: "active" })!;

beforeEach(() => Object.values(api).forEach((fn) => fn.mockReset()));
afterEach(cleanup);

describe("RenameDialog", () => {
  it("treats saving the untitled placeholder as no change", async () => {
    const onOpenChange = vi.fn();
    renderWithI18n(<RenameDialog row={row("")} open onOpenChange={onOpenChange} onRenamed={vi.fn()} />);
    await userEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect(api.renameConversation).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("saves a real new title", async () => {
    api.renameConversation.mockResolvedValue({});
    const onRenamed = vi.fn();
    renderWithI18n(<RenameDialog row={row("Old")} open onOpenChange={vi.fn()} onRenamed={onRenamed} />);
    const input = await screen.findByRole("textbox");
    await userEvent.clear(input);
    await userEvent.type(input, "Fresh");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onRenamed).toHaveBeenCalledWith("c1", "Fresh"));
  });
});

describe("ShareDialog", () => {
  it("lets the owner retry when the share cannot be read", async () => {
    api.fetchShare.mockRejectedValueOnce(new Error("down"));
    api.fetchShare.mockResolvedValueOnce(null);
    renderWithI18n(<ShareDialog row={row("T")} open onOpenChange={vi.fn()} onChanged={vi.fn()} />);
    await userEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(api.fetchShare).toHaveBeenCalledTimes(2));
    await screen.findByRole("button", { name: /create/i });
  });
});
