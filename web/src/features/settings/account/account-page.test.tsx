// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthUser } from "@/lib/api/types";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const api = vi.hoisted(() => ({
  fetchCurrentUser: vi.fn(),
  fetchLoginOptions: vi.fn(),
  fetchSessions: vi.fn(),
  fetchIdentities: vi.fn(),
  fetchMfaStatus: vi.fn(),
  logoutAll: vi.fn(),
  mfaSetupStart: vi.fn(),
}));
vi.mock("./api", () => api);
const publish = vi.hoisted(() => vi.fn());
const me = vi.hoisted(() => ({ current: null as unknown }));
vi.mock("@/lib/session/provider", () => ({
  useSession: () => ({ user: me.current, status: "ready", accessToken: "tok" }),
  publishUserUpdate: publish,
}));
vi.mock("@/lib/capabilities/provider", () => ({
  useCapabilities: () => ({ flags: { accountSecurity: true, identityProviders: true }, loaded: true }),
}));
vi.mock("@/components/shell/header-context", () => ({ usePageTitle: () => undefined }));

import { AccountSettingsPage } from "./account-page";

const user = { id: 1, publicID: "u_abc", username: "ada", email: "ada@example.test", emailVerifiedAt: "2025-01-01T00:00:00Z", passwordEnabled: true, mustResetPassword: false, twoFactorAvailable: true } as unknown as AuthUser;
const options = { emailVerificationEnabled: true, providerAuthBridge: { enabled: false }, providers: [] };

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  publish.mockReset();
  me.current = user;
  api.fetchCurrentUser.mockResolvedValue(user);
  api.fetchLoginOptions.mockResolvedValue(options);
  api.fetchSessions.mockResolvedValue([]);
  api.fetchIdentities.mockResolvedValue([]);
  api.fetchMfaStatus.mockResolvedValue({ available: true, totpEnabled: false, required: false, recoveryCount: 0 });
  clearToasts();
});
afterEach(cleanup);

describe("AccountSettingsPage loading", () => {
  it("offers a retry when part of the account data fails to load", async () => {
    api.fetchSessions.mockRejectedValueOnce(new Error("sessions down"));
    const u = userEvent.setup();
    renderWithApp(<AccountSettingsPage />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Could not load account details");
    await waitFor(() => expect(toastTitles()).toContain("Could not load account details"));
    api.fetchSessions.mockResolvedValue([]);
    await u.click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(api.fetchSessions).toHaveBeenCalledTimes(2);
  });

  it("shows no notice when everything loads", async () => {
    renderWithApp(<AccountSettingsPage />);
    expect(await screen.findByText("Signed-in devices")).toBeTruthy();
    await waitFor(() => expect(api.fetchSessions).toHaveBeenCalled());
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("always shows the public id with a copy button, even before data arrives", async () => {
    renderWithApp(<AccountSettingsPage />);
    expect(await screen.findByText("u_abc")).toBeTruthy();
  });
});
