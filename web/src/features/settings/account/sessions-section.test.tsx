// @vitest-environment jsdom
import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import type { SessionRow } from "./api";
import { deviceTitle, locationText, SessionsSection } from "./sessions-section";

vi.mock("@/lib/api/session-store", async (orig) => ({ ...(await orig<object>()), endSession: vi.fn() }));
afterEach(cleanup);

const session = (id: string, over: Partial<SessionRow> = {}): SessionRow => ({
  sessionID: id,
  current: false,
  deviceLabel: "",
  deviceName: "",
  browserName: "Firefox",
  osName: "Linux",
  clientIP: "",
  countryCode: "",
  regionName: "",
  cityName: "",
  createdAt: "2025-02-03T04:05:06Z",
  updatedAt: "bad date",
  ...over,
});

describe("session labels", () => {
  it("titles by browser and system, then label, then name, then unknown", () => {
    expect(deviceTitle(session("a"), "?")).toBe("Firefox (Linux)");
    expect(deviceTitle(session("a", { browserName: "", deviceLabel: "Work laptop" }), "?")).toBe("Work laptop");
    expect(deviceTitle(session("a", { browserName: "", osName: "", deviceName: "pc" }), "?")).toBe("pc");
    expect(deviceTitle(session("a", { browserName: "", osName: "" }), "?")).toBe("?");
  });
  it("joins city, region and country", () => {
    expect(locationText({ cityName: "Oslo", regionName: "", countryCode: "NO" })).toBe("Oslo, NO");
  });
});

describe("SessionsSection", () => {
  it("gives every row's menu button its own name and shows unknown fallbacks", () => {
    renderWithApp(
      <SessionsSection
        sessions={[session("a", { current: true }), session("b", { browserName: "Chrome", osName: "macOS" })]}
        loading={false}
        onRemoved={vi.fn()}
      />,
    );
    const names = screen.getAllByRole("button").map((b) => b.getAttribute("aria-label"));
    expect(names).toEqual(["Actions for Firefox (Linux)", "Actions for Chrome (macOS)"]);
    const table = screen.getByRole("table", { name: "Signed-in devices" });
    const rows = within(table).getAllByRole("row");
    expect(within(rows[1] as HTMLElement).getByText("This device")).toBeTruthy();
    expect(within(rows[1] as HTMLElement).getByText("Unknown location")).toBeTruthy();
    expect(within(rows[1] as HTMLElement).getByText("Unknown address")).toBeTruthy();
    // A date that does not parse reads as a dash.
    expect(within(rows[1] as HTMLElement).getByText("-")).toBeTruthy();
  });

  it("shows a loading row, then an empty row", () => {
    renderWithApp(<SessionsSection sessions={[]} loading onRemoved={vi.fn()} />);
    expect(screen.getByText("Loading…")).toBeTruthy();
    cleanup();
    renderWithApp(<SessionsSection sessions={[]} loading={false} onRemoved={vi.fn()} />);
    expect(screen.getByText("No sessions.")).toBeTruthy();
  });
});
