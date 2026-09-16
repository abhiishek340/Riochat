// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const api = vi.hoisted(() => ({ createCheckout: vi.fn(), redeemCode: vi.fn(), subscribeFree: vi.fn() }));
vi.mock("./api", () => api);

import { displayOf } from "./money";
import { PaymentDialog, PlansDialog, RedeemDialog, TopupDialog, type BillingContext } from "./payment-dialogs";
import type { Overview, Plan } from "./types";

const plan = (over: Partial<Plan> = {}): Plan => ({
  id: 1, code: "pro", name: "Pro", description: "", featureJSON: "", sortOrder: 0, periodCreditUSD: 5,
  prices: [{ id: 11, amountCents: 2000, billingInterval: "month", currency: "USD", isDefault: true }], ...over,
});
const freePlan = plan({ id: 2, code: "free", name: "Free", prices: [{ id: 21, amountCents: 0, billingInterval: "month", currency: "USD", isDefault: true }] });
const overview = (p: Plan | null): Overview => ({ mode: "period", balanceUSD: 3, totalSpentUSD: 0, plan: p, periodStartAt: "", periodEndAt: "", periodCreditUSD: 0, periodUsedUSD: 0, entitlements: [] });

function ctx(over: Partial<BillingContext> = {}): BillingContext {
  return {
    config: { mode: "period", paymentProviders: ["stripe", "epay"], displayCurrency: "USD", usdToCNYRate: 0, epayTypes: [] },
    plans: [freePlan, plan()],
    overview: overview(null),
    display: displayOf(null),
    loading: false,
    user: {},
    ...over,
  };
}

let opened: MockInstance<typeof window.open>;
beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  clearToasts();
  opened = vi.spyOn(window, "open").mockImplementation(() => null);
});
afterEach(() => {
  cleanup();
  opened.mockRestore();
});

describe("RedeemDialog", () => {
  it("sends the trimmed code and hands back the fresh overview", async () => {
    const fresh = overview(plan());
    api.redeemCode.mockResolvedValue(fresh);
    const onRedeemed = vi.fn();
    const user = userEvent.setup();
    renderWithApp(<RedeemDialog open onOpenChange={() => undefined} onRedeemed={onRedeemed} />);
    await user.type(await screen.findByRole("textbox", { name: "Code" }), "  CODE1 ");
    await user.click(screen.getByRole("button", { name: "Redeem" }));
    await waitFor(() => expect(api.redeemCode).toHaveBeenCalledWith("CODE1"));
    expect(onRedeemed).toHaveBeenCalledWith(fresh);
    expect(toastTitles()).toContain("Code redeemed");
  });
  it("refuses an empty code", async () => {
    const user = userEvent.setup();
    renderWithApp(<RedeemDialog open onOpenChange={() => undefined} onRedeemed={() => undefined} />);
    await user.click(await screen.findByRole("button", { name: "Redeem" }));
    expect(toastTitles()).toContain("Enter a redemption code");
    expect(api.redeemCode).not.toHaveBeenCalled();
  });
});

describe("TopupDialog", () => {
  it.each(["0", "abc", "-4"])("refuses %j without a request", async (text) => {
    const user = userEvent.setup();
    renderWithApp(<TopupDialog open onOpenChange={() => undefined} ctx={ctx()} />);
    const input = await screen.findByRole("spinbutton");
    await user.clear(input);
    if (text !== "abc") await user.type(input, text);
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(toastTitles()).toContain("Enter an amount above zero");
    expect(api.createCheckout).not.toHaveBeenCalled();
  });

  it("sends minor units of the display currency and opens the checkout without opener or referrer", async () => {
    api.createCheckout.mockResolvedValue("https://pay.test/c/1");
    const user = userEvent.setup();
    const display = displayOf({ displayCurrency: "CNY", usdToCNYRate: 7 });
    renderWithApp(<TopupDialog open onOpenChange={() => undefined} ctx={ctx({ display })} />);
    await screen.findByRole("spinbutton");
    // Amount defaults to 20; Stripe is the default method and charges CNY at the rate.
    expect(screen.getByRole("radio", { name: /Card \(Stripe\)/ }).textContent).toContain("¥20.00");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(api.createCheckout).toHaveBeenCalledTimes(1));
    const body = api.createCheckout.mock.calls[0]?.[0];
    expect(body).toMatchObject({ orderType: "topup", amountMinorUnits: 2000, cycles: 1, paymentProvider: "stripe" });
    expect(body.epayType).toBeUndefined();
    expect(body.successURL).toMatch(/\/settings\/subscription\?payment=success$/);
    expect(opened).toHaveBeenCalledWith("https://pay.test/c/1", "_blank", "noopener,noreferrer");
  });

  it("reports an empty checkout URL and opens nothing", async () => {
    api.createCheckout.mockResolvedValue("");
    const user = userEvent.setup();
    renderWithApp(<TopupDialog open onOpenChange={() => undefined} ctx={ctx()} />);
    await user.click(await screen.findByRole("button", { name: "Continue" }));
    await waitFor(() => expect(toastTitles()).toContain("Could not start the payment"));
    expect(opened).not.toHaveBeenCalled();
  });

  it("sends the EPay type only for EPay", async () => {
    api.createCheckout.mockResolvedValue("https://pay.test/c/2");
    const user = userEvent.setup();
    renderWithApp(<TopupDialog open onOpenChange={() => undefined} ctx={ctx()} />);
    await user.click(await screen.findByRole("radio", { name: /WeChat Pay/ }));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(api.createCheckout).toHaveBeenCalled());
    expect(api.createCheckout.mock.calls[0]?.[0]).toMatchObject({ paymentProvider: "epay", epayType: "wxpay" });
  });
});

describe("PlansDialog", () => {
  const pro = plan();
  it("blocks the free plan while a paid plan is active, with no request", async () => {
    const user = userEvent.setup();
    renderWithApp(<PlansDialog open onOpenChange={() => undefined} ctx={ctx({ overview: overview(pro) })} onPaid={vi.fn()} onSubscribed={vi.fn()} />);
    const buttons = await screen.findAllByRole("button", { name: "Unavailable" });
    expect((buttons[0] as HTMLButtonElement).disabled).toBe(true);
    await user.click(buttons[0] as HTMLElement).catch(() => undefined);
    expect(api.subscribeFree).not.toHaveBeenCalled();
  });

  it("opens payment for a paid plan", async () => {
    const onPaid = vi.fn();
    const user = userEvent.setup();
    renderWithApp(<PlansDialog open onOpenChange={() => undefined} ctx={ctx({ overview: overview(freePlan) })} onPaid={onPaid} onSubscribed={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: "Subscribe" }));
    expect(onPaid).toHaveBeenCalledTimes(1);
    expect(onPaid.mock.calls[0]?.[0]).toMatchObject({ plan: { id: 1 }, price: { id: 11 }, kind: "subscribe" });
    expect(api.subscribeFree).not.toHaveBeenCalled();
  });

  it("switches to a free plan with a direct call", async () => {
    api.subscribeFree.mockResolvedValue(undefined);
    const onSubscribed = vi.fn();
    const user = userEvent.setup();
    renderWithApp(<PlansDialog open onOpenChange={() => undefined} ctx={ctx({ overview: overview(null) })} onPaid={vi.fn()} onSubscribed={onSubscribed} />);
    await user.click(await screen.findByRole("button", { name: "Switch" }));
    await waitFor(() => expect(api.subscribeFree).toHaveBeenCalledWith(21));
    expect(onSubscribed).toHaveBeenCalled();
  });

  it("refuses a paid plan when no payment method exists", async () => {
    const user = userEvent.setup();
    const noPay = ctx({ config: { mode: "period", paymentProviders: [], displayCurrency: "USD", usdToCNYRate: 0, epayTypes: [] } });
    const onPaid = vi.fn();
    renderWithApp(<PlansDialog open onOpenChange={() => undefined} ctx={noPay} onPaid={onPaid} onSubscribed={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: "Subscribe" }));
    expect(toastTitles()).toContain("No payment method is set up");
    expect(onPaid).not.toHaveBeenCalled();
  });
});

describe("PaymentDialog", () => {
  it("creates a subscription checkout for the chosen price", async () => {
    api.createCheckout.mockResolvedValue("https://pay.test/s/1");
    const user = userEvent.setup();
    const target = { plan: plan(), price: plan().prices[0]!, kind: "upgrade" as const, protectedHigher: false };
    renderWithApp(<PaymentDialog target={target} onOpenChange={() => undefined} ctx={ctx()} />);
    expect(await screen.findByText("Upgrade your plan")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(api.createCheckout).toHaveBeenCalled());
    expect(api.createCheckout.mock.calls[0]?.[0]).toMatchObject({ orderType: "subscription", priceID: 11, cycles: 1, paymentProvider: "stripe" });
    expect(opened).toHaveBeenCalledWith("https://pay.test/s/1", "_blank", "noopener,noreferrer");
  });
});
