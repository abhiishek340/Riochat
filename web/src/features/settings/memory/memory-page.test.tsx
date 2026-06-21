// @vitest-environment jsdom
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToasts, renderWithApp } from "../test-utils";

const api = vi.hoisted(() => ({ listMemories: vi.fn() }));
vi.mock("./api", async (orig) => ({ ...(await orig<object>()), listMemories: api.listMemories }));
vi.mock("@/components/shell/header-context", () => ({ usePageTitle: () => undefined }));

import { localEntry } from "./api";
import { MemorySettingsPage } from "./memory-page";

beforeEach(() => {
  api.listMemories.mockReset();
  clearToasts();
});
afterEach(cleanup);

describe("MemorySettingsPage", () => {
  it("shows a retry notice instead of an empty list that could be added to, then recovers", async () => {
    const user = userEvent.setup();
    api.listMemories.mockRejectedValueOnce(new Error("offline")).mockResolvedValue([localEntry("city", "Oslo", "profile")]);
    renderWithApp(<MemorySettingsPage />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Could not load your notes");
    expect(screen.queryByRole("button", { name: "Add" })).toBeNull();
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Oslo")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("lists only the notes of the selected scope", async () => {
    api.listMemories.mockResolvedValue([localEntry("city", "Oslo", "profile"), localEntry("tone", "dry", "preference")]);
    renderWithApp(<MemorySettingsPage />);
    expect(await screen.findByText("Oslo")).toBeTruthy();
    expect(screen.queryByText("dry")).toBeNull();
  });
});
