// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());

import { QuotaPanel } from "./quota-panel";

afterEach(cleanup);

describe("QuotaPanel (scenario 15)", () => {
  it("shows an unlimited quota without a bar", () => {
    render(<QuotaPanel quota={{ quotaBytes: 0, usedBytes: 1048576 }} />);
    expect(screen.getByText("1.0 MB / Unlimited")).toBeTruthy();
    expect(screen.getByText("No storage cap")).toBeTruthy();
    expect(screen.queryByRole("progressbar")).toBeNull();
  });
  it("shows usage, percent and what is left", () => {
    render(<QuotaPanel quota={{ quotaBytes: 10 * 1048576, usedBytes: 2.5 * 1048576 }} />);
    expect(screen.getByText("2.5 MB / 10.0 MB")).toBeTruthy();
    expect(screen.getByText("25% used")).toBeTruthy();
    expect(screen.getByText("7.5 MB left")).toBeTruthy();
    const bar = screen.getByRole("progressbar", { name: "Storage" });
    expect(bar.getAttribute("aria-valuenow")).toBe("25");
    expect(bar.getAttribute("aria-valuetext")).toBe("25% used");
  });
});
