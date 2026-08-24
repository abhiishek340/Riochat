// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToasts, renderWithApp } from "../test-utils";

const mocks = vi.hoisted(() => ({
  listProviders: vi.fn(),
  updateProvider: vi.fn(),
  deleteProvider: vi.fn(),
  fetchProviderModels: vi.fn(),
  replace: vi.fn(),
}));
vi.mock("./api", async (orig) => ({
  ...(await orig<object>()),
  listProviders: mocks.listProviders,
  updateProvider: mocks.updateProvider,
  deleteProvider: mocks.deleteProvider,
  fetchProviderModels: mocks.fetchProviderModels,
}));
vi.mock("./use-provider-access", () => ({
  useProviderAccess: () => ({ loaded: true, access: { enabled: true, maxPerUser: 5, protocols: [], modelProtocols: [] } }),
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace: mocks.replace }) }));
vi.mock("@/components/shell/header-context", () => ({ usePageTitle: () => undefined }));

import { normalizeProvider, type PersonalProvider } from "./api";
import { ModelsSettingsPage } from "./models-page";

const provider = (id: string, over: Record<string, unknown> = {}): PersonalProvider =>
  normalizeProvider({ id, name: `P${id}`, baseURL: `https://api.${id}.test/v1`, host: `api.${id}.test`, protocol: "openai_chat_completions", status: "active", models: ["m"], ...over });

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

beforeEach(() => {
  Object.values(mocks).forEach((m) => m.mockReset());
  clearToasts();
});
afterEach(cleanup);

async function pick(user: ReturnType<typeof userEvent.setup>, rowName: string, item: RegExp) {
  const row = (await screen.findByText(rowName)).closest("tr") as HTMLElement;
  await user.click(within(row).getByRole("button", { name: /actions/i }));
  await user.click(await screen.findByRole("menuitem", { name: item }));
}

describe("ModelsSettingsPage", () => {
  it("keeps both results when two rows finish their toggles out of order", async () => {
    const user = userEvent.setup();
    mocks.listProviders.mockResolvedValue([provider("a"), provider("b")]);
    const first = deferred<PersonalProvider>();
    const second = deferred<PersonalProvider>();
    mocks.updateProvider.mockImplementation((id: string) => (id === "a" ? first.promise : second.promise));
    renderWithApp(<ModelsSettingsPage />);

    await pick(user, "Pa", /^Turn off$/);
    await pick(user, "Pb", /^Turn off$/);
    expect(mocks.updateProvider).toHaveBeenCalledTimes(2);

    second.resolve(provider("b", { status: "disabled" }));
    await waitFor(() => expect(within((screen.getByText("Pb").closest("tr") as HTMLElement)).getByText("Disabled")).toBeTruthy());
    first.resolve(provider("a", { status: "disabled" }));
    await waitFor(() => expect(within((screen.getByText("Pa").closest("tr") as HTMLElement)).getByText("Disabled")).toBeTruthy());
    // The slower request must not bring the faster row back to its old state.
    expect(within((screen.getByText("Pb").closest("tr") as HTMLElement)).getByText("Disabled")).toBeTruthy();
  });

  it("shows an error notice instead of a misleading empty table, and retries", async () => {
    const user = userEvent.setup();
    mocks.listProviders.mockRejectedValueOnce(new Error("offline")).mockResolvedValue([provider("a")]);
    renderWithApp(<ModelsSettingsPage />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Could not load your providers");
    expect(screen.queryByText(/No providers yet|Nothing here/i)).toBeNull();
    expect((screen.getByRole("button", { name: /^Add provider$/ }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Pa")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("disables Add at the per-user limit and lets keyboard users reach the reason", async () => {
    mocks.listProviders.mockResolvedValue(["a", "b", "c", "d", "e"].map((id) => provider(id)));
    renderWithApp(<ModelsSettingsPage />);
    await screen.findByText("Pe");
    const add = screen.getByRole("button", { name: "Add provider" }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    const wrapper = add.parentElement as HTMLElement;
    expect(wrapper.tabIndex).toBe(0);
    expect(wrapper.getAttribute("aria-label")).toBe("You have reached the limit of 5 providers.");
  });

  it("disables edit, check and turn-off for a suspended provider", async () => {
    const user = userEvent.setup();
    mocks.listProviders.mockResolvedValue([provider("a", { status: "suspended" })]);
    renderWithApp(<ModelsSettingsPage />);
    const row = (await screen.findByText("Pa")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Suspended")).toBeTruthy();
    await user.click(within(row).getByRole("button", { name: /actions/i }));
    for (const name of [/^Edit$/, /^Check connection$/, /^Turn off$/]) {
      expect(screen.getByRole("menuitem", { name }).getAttribute("aria-disabled") ?? screen.getByRole("menuitem", { name }).getAttribute("data-disabled")).not.toBeNull();
    }
    expect(screen.getByRole("menuitem", { name: /^Delete$/ }).getAttribute("data-disabled")).toBeNull();
  });

  it("makes the status reason and the model list reachable by keyboard", async () => {
    mocks.listProviders.mockResolvedValue([provider("a", { lastError: "upstream.timeout", models: ["m1", "m2"] }), provider("b", { status: "suspended" })]);
    renderWithApp(<ModelsSettingsPage />);
    const rowA = (await screen.findByText("Pa")).closest("tr") as HTMLElement;
    expect(within(rowA).getByText("Error").tabIndex).toBe(0);
    expect(within(rowA).getByText("2").tabIndex).toBe(0);
    const rowB = (await screen.findByText("Pb")).closest("tr") as HTMLElement;
    expect(within(rowB).getByText("Suspended").tabIndex).toBe(0);
  });

  it("names the table", async () => {
    mocks.listProviders.mockResolvedValue([provider("a")]);
    renderWithApp(<ModelsSettingsPage />);
    await screen.findByText("Pa");
    expect(screen.getByRole("table", { name: /models/i })).toBeTruthy();
  });
});
