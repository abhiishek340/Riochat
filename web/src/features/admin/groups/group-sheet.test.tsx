// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PermissionGroup } from "../lib/wire";
import { renderWithApp } from "../test-utils";

const llm = vi.hoisted(() => ({
  listAllUpstreams: vi.fn(),
  listModels: vi.fn(),
  listVendors: vi.fn(),
  createGroup: vi.fn(),
  getGroupModels: vi.fn(),
  getGroupUsers: vi.fn(),
  patchGroup: vi.fn(),
  putGroupModels: vi.fn(),
  putGroupUsers: vi.fn(),
}));
vi.mock("../llm-api", () => llm);
vi.mock("../login/api", () => ({ listProviders: vi.fn().mockResolvedValue([]) }));

import { GroupSheet } from "./group-sheet";

const group = { id: 7, name: "Team", description: "", isDefault: false, rateMultiplierPercent: 100, modelCount: 1, manualModelCount: 1, ruleModelCount: 0, userCount: 1, manualUserCount: 1, subscriptionUserCount: 0, createdAt: "", updatedAt: "" } as PermissionGroup;

beforeEach(() => {
  Object.values(llm).forEach((fn) => fn.mockReset());
  llm.listAllUpstreams.mockResolvedValue([]);
  llm.listVendors.mockResolvedValue([]);
  llm.getGroupModels.mockResolvedValue({ modelIDs: [1], rules: [{ type: "vendor", value: "acme" }] });
  llm.getGroupUsers.mockResolvedValue({ userIDs: [5] });
});
afterEach(cleanup);

function open() {
  return renderWithApp(<GroupSheet target={group} onOpenChange={() => undefined} onChanged={() => undefined} />);
}

describe("GroupSheet", () => {
  it("refuses to save over assignments it could not read, and offers a retry", async () => {
    const user = userEvent.setup();
    llm.getGroupModels.mockRejectedValueOnce(new Error("offline"));
    open();
    expect((await screen.findAllByRole("alert")).length).toBeGreaterThan(0);
    const save = screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false));
    expect(screen.queryAllByRole("alert")).toHaveLength(0);
    expect(llm.putGroupModels).not.toHaveBeenCalled();
  });

  it("sends the loaded assignments along with the group on save", async () => {
    const user = userEvent.setup();
    llm.patchGroup.mockResolvedValue({});
    llm.putGroupModels.mockResolvedValue({});
    llm.putGroupUsers.mockResolvedValue({});
    open();
    const save = await screen.findByRole("button", { name: "Save" });
    await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(false));
    await user.click(save);
    await waitFor(() => expect(llm.putGroupUsers).toHaveBeenCalledWith(7, [5]));
    expect(llm.putGroupModels).toHaveBeenCalledWith(7, [1], [{ type: "vendor", value: "acme" }]);
  });
});
