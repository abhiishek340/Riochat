// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const mocks = vi.hoisted(() => ({ fetchUsage: vi.fn() }));
vi.mock("./api", () => ({ fetchUsage: mocks.fetchUsage }));

import { UsageLog } from "./usage-log";
import type { LedgerItem } from "./types";

const display = { currency: "USD", rate: 1 } as const;

const item = (id: number, over: Partial<LedgerItem> = {}): LedgerItem => ({
  id,
  conversationID: id,
  platformModelName: `model-${id}`,
  isFreeModel: false,
  billingAt: "2025-03-04T05:06:07Z",
  createdAt: "2025-03-04T05:06:07Z",
  inputTokens: 10,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  outputTokens: 5,
  reasoningTokens: 0,
  callCount: 1,
  durationSeconds: 0,
  latencyMS: 250,
  billedUSD: 0.01,
  balanceAfterUSD: 9.5,
  pricingSnapshotJSON: "",
  ...over,
});

beforeEach(() => {
  mocks.fetchUsage.mockReset().mockResolvedValue({ items: [item(1)], total: 80 });
  clearToasts();
});
afterEach(cleanup);

const queries = () => mocks.fetchUsage.mock.calls.map((c) => c[0] as { page: number; pageSize: number });

describe("UsageLog", () => {
  it("asks for the new page size on page 1 with a single request", async () => {
    const user = userEvent.setup();
    renderWithApp(<UsageLog display={display} refreshKey={0} />);
    await screen.findByText("model-1");
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => expect(queries().at(-1)).toMatchObject({ page: 2, pageSize: 25 }));
    const before = mocks.fetchUsage.mock.calls.length;
    await user.selectOptions(screen.getByRole("combobox", { name: "Rows per page" }), "50");
    await waitFor(() => expect(queries().at(-1)).toMatchObject({ page: 1, pageSize: 50 }));
    // Exactly one request followed the change: never (page 2, size 50) first.
    expect(mocks.fetchUsage.mock.calls.length - before).toBe(1);
    expect(queries().slice(before).some((q) => q.page === 2 && q.pageSize === 50)).toBe(false);
  });

  it("goes back to the first page when the type filter changes", async () => {
    const user = userEvent.setup();
    renderWithApp(<UsageLog display={display} refreshKey={0} />);
    await screen.findByText("model-1");
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => expect(queries().at(-1)?.page).toBe(2));
    await user.selectOptions(screen.getByRole("combobox", { name: /type|filter/i }), "free");
    await waitFor(() => expect(mocks.fetchUsage.mock.calls.at(-1)?.[0]).toMatchObject({ page: 1, status: "free" }));
  });

  it("shows a retry notice instead of an empty log when loading fails", async () => {
    mocks.fetchUsage.mockRejectedValueOnce(new Error("down"));
    const user = userEvent.setup();
    renderWithApp(<UsageLog display={display} refreshKey={0} />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Could not load the usage log");
    expect(toastTitles()).toContain("Could not load the usage log");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("model-1")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("lets keyboard users reach the cost formulas", async () => {
    renderWithApp(<UsageLog display={display} refreshKey={0} />);
    await screen.findByText("model-1");
    const table = screen.getByRole("table", { name: "Usage log" });
    // The base-billing cell carries a formula tooltip and must take focus.
    expect(table.querySelectorAll("tbody [tabindex='0']").length).toBeGreaterThan(0);
  });
});
