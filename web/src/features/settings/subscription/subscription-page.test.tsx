// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const api = vi.hoisted(() => ({
  fetchConfig: vi.fn(),
  fetchPlans: vi.fn(),
  fetchOverview: vi.fn(),
  fetchDaily: vi.fn(),
  fetchMonthly: vi.fn(),
  fetchActivity: vi.fn(),
}));
vi.mock("./api", () => api);
const reloadUser = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock("@/lib/session/provider", () => ({
  useSession: () => ({ user: { publicID: "u_1", subscriptionTier: "", subscriptionPlanID: 0 }, accessToken: "tok", reloadUser }),
}));
vi.mock("@/components/shell/header-context", () => ({ usePageTitle: () => undefined }));
vi.mock("./heatmap", () => ({ ActivityHeatmap: () => <div data-testid="heatmap" /> }));
vi.mock("./usage-trend", () => ({ UsageTrend: () => <div data-testid="trend" /> }));
vi.mock("./usage-log", () => ({ UsageLog: () => <div data-testid="log" /> }));
vi.mock("./summary", () => ({ BillingSummary: () => <div data-testid="summary" /> }));

import { SubscriptionSettingsPage } from "./subscription-page";

const config = { mode: "usage", paymentProviders: [], displayCurrency: "USD", usdToCNYRate: 0, epayTypes: [] };

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  reloadUser.mockClear();
  api.fetchConfig.mockResolvedValue(config);
  api.fetchPlans.mockResolvedValue([]);
  api.fetchOverview.mockResolvedValue({ mode: "usage", balanceUSD: 1, totalSpentUSD: 0, plan: null, entitlements: [] });
  api.fetchDaily.mockResolvedValue([]);
  api.fetchMonthly.mockResolvedValue([]);
  api.fetchActivity.mockResolvedValue([]);
  clearToasts();
});
afterEach(cleanup);

describe("SubscriptionSettingsPage loading", () => {
  it("shows the usual panels and no notice when everything loads", async () => {
    renderWithApp(<SubscriptionSettingsPage />);
    expect(await screen.findByTestId("summary")).toBeTruthy();
    await waitFor(() => expect(api.fetchMonthly).toHaveBeenCalled());
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("toasts and shows a retry notice when one request fails, then recovers", async () => {
    api.fetchOverview.mockRejectedValueOnce(new Error("overview down"));
    const user = userEvent.setup();
    renderWithApp(<SubscriptionSettingsPage />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Could not load billing information");
    await waitFor(() => expect(toastTitles()).toContain("Could not load billing information"));
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(api.fetchOverview).toHaveBeenCalledTimes(2);
  });
});
