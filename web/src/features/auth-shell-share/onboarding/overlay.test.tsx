// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import type { AuthUser } from "@/lib/api/types";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";

const session = vi.hoisted(() => ({ user: null as unknown, publish: vi.fn() }));
vi.mock("@/lib/session/provider", () => ({ useSession: () => ({ user: session.user }), publishUserUpdate: (u: unknown) => session.publish(u) }));

const api = vi.hoisted(() => ({
  patchProfile: vi.fn(),
  patchUsername: vi.fn(),
  completeOnboarding: vi.fn(),
  startTwoFactorSetup: vi.fn(),
  confirmTwoFactorSetup: vi.fn(),
  cancelTwoFactorSetup: vi.fn(),
}));
vi.mock("./api", () => api);

const auth = vi.hoisted(() => ({ fetchMe: vi.fn() }));
vi.mock("@/lib/api/endpoints/auth", async (orig) => ({ ...(await orig<object>()), fetchMe: auth.fetchMe }));
const store = vi.hoisted(() => ({ endSession: vi.fn() }));
vi.mock("@/lib/api/session-store", async (orig) => ({ ...(await orig<object>()), endSession: store.endSession }));

import { OnboardingOverlay } from "./overlay";

function makeUser(over: Partial<AuthUser> = {}): AuthUser {
  return {
    id: 1,
    publicID: "u1",
    username: "alice",
    displayName: "",
    avatarURL: "",
    email: "a@b.co",
    phone: "",
    role: "user",
    status: "active",
    timezone: "",
    locale: "en-US",
    profilePreferences: "",
    appearancePreferences: "",
    onboardingCompletedAt: null,
    emailVerifiedAt: null,
    passwordEnabled: true,
    mustResetPassword: false,
    initialUsernameRequired: false,
    initialSecurityRequired: true,
    twoFactorAvailable: true,
    twoFactorEnabled: false,
    twoFactorRequired: false,
    subscriptionTier: "free",
    subscriptionPlanName: "free",
    subscriptionStatus: "free",
    createdAt: "",
    updatedAt: "",
    ...over,
  } as AuthUser;
}

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  auth.fetchMe.mockReset();
  store.endSession.mockReset();
  session.publish.mockReset();
  clearToasts();
});
afterEach(cleanup);

const click = (name: string | RegExp) => userEvent.click(screen.getByRole("button", { name }));

describe("OnboardingOverlay", () => {
  it("renders nothing without a user or when security is not required", () => {
    session.user = null;
    const { container, unmount } = renderWithI18n(<OnboardingOverlay />);
    expect(container.textContent).toBe("");
    unmount();
    session.user = makeUser({ initialSecurityRequired: false });
    renderWithI18n(<OnboardingOverlay />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("is a modal dialog that Escape cannot dismiss", async () => {
    session.user = makeUser();
    renderWithI18n(<OnboardingOverlay />);
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeNull();
  });

  it("moves focus to the new step's heading so keyboard and screen-reader users are not left on a removed button", async () => {
    session.user = makeUser({ twoFactorAvailable: false, initialUsernameRequired: false, displayName: "Alice" });
    renderWithI18n(<OnboardingOverlay />);
    await screen.findByRole("heading", { level: 2, name: /^Welcome/ });
    await click("Get started");
    const heading = await screen.findByRole("heading", { level: 2, name: "Your profile" });
    await waitFor(() => expect(document.activeElement).toBe(heading));
  });

  it("walks a bootstrap administrator through the account step and sends the password only at the end", async () => {
    const admin = makeUser({ role: "superadmin", username: "admin", mustResetPassword: true, initialUsernameRequired: true, twoFactorAvailable: false });
    session.user = admin;
    api.patchUsername.mockResolvedValue({ ...admin, username: "boss" });
    api.patchProfile.mockImplementation(async (body: object) => ({ ...admin, ...body }));
    auth.fetchMe.mockResolvedValue({ ...admin, username: "boss" });
    api.completeOnboarding.mockResolvedValue({ ...admin, initialSecurityRequired: false });

    renderWithI18n(<OnboardingOverlay />);
    await screen.findByText("Welcome, administrator");
    await click("Get started");
    await screen.findByText("Secure the administrator account");

    await userEvent.clear(screen.getByLabelText(/^username/i));
    await userEvent.type(screen.getByLabelText(/^username/i), "Boss");
    await userEvent.type(screen.getByLabelText(/new password/i), "abc");
    await click("Continue");
    expect(toastTitles()).toContain("Use 8 to 128 characters, and not digits only.");
    expect(api.patchUsername).not.toHaveBeenCalled();

    await userEvent.clear(screen.getByLabelText(/new password/i));
    await userEvent.type(screen.getByLabelText(/new password/i), "goodpass1");
    await click("Continue");
    await waitFor(() => expect(api.patchUsername).toHaveBeenCalledWith("boss"));
    expect(api.completeOnboarding).not.toHaveBeenCalled();

    await screen.findByText("Protect your sign-in");
    expect(screen.getByText("Two-factor sign-in is not available on this server.")).toBeTruthy();
    await click("Continue");
    await screen.findByText("Choose your colours");
    await click("Continue");
    await screen.findByText("Time and appearance");
    await click("Continue");
    await screen.findByText("You are all set");
    await click("Finish");

    await waitFor(() => expect(api.completeOnboarding).toHaveBeenCalledWith("goodpass1"));
    expect(store.endSession).toHaveBeenCalled();
    expect(toastTitles()).toContain("Setup complete. Sign in again with your new password.");
  });

  it("sends a returning-account flow back to the account step when the password was used before", async () => {
    const admin = makeUser({ role: "superadmin", username: "boss", mustResetPassword: true, twoFactorAvailable: false });
    session.user = admin;
    api.patchProfile.mockResolvedValue(admin);
    auth.fetchMe.mockResolvedValue(admin);
    api.completeOnboarding.mockRejectedValue(new ApiError({ message: "reuse", status: 400, errorCode: "auth.password_reuse_not_allowed" }));
    renderWithI18n(<OnboardingOverlay />);
    await click("Get started");
    await userEvent.type(await screen.findByLabelText(/new password/i), "goodpass1");
    await click("Continue");
    await screen.findByText("Protect your sign-in");
    await click("Continue");
    await click("Continue");
    await click("Continue");
    await click("Finish");
    await screen.findByText("Secure the administrator account");
    expect(store.endSession).not.toHaveBeenCalled();
  });

  it("validates the display name and the one-time code, and starts the enrolment once", async () => {
    session.user = makeUser();
    api.patchProfile.mockImplementation(async (body: object) => ({ ...(session.user as object), ...body }));
    api.startTwoFactorSetup.mockResolvedValue({ secret: "JBSWY3DPEHPK3PXP", otpauthURL: "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP", expiresAt: "" });
    renderWithI18n(<OnboardingOverlay />);
    await click("Get started");

    await userEvent.type(await screen.findByLabelText(/display name/i), "ab");
    await click("Continue");
    expect(toastTitles()).toContain("Your display name must be 3 to 16 characters.");
    await userEvent.type(screen.getByLabelText(/display name/i), "c");
    await click("Continue");
    await waitFor(() => expect(api.patchProfile).toHaveBeenCalledWith({ displayName: "abc" }));

    await screen.findByText("Protect your sign-in");
    await waitFor(() => expect(screen.getByLabelText("Setup key")).toBeTruthy());
    expect((screen.getByLabelText("Setup key") as HTMLInputElement).value).toBe("JBSWY3DPEHPK3PXP");
    expect(screen.getByRole("img", { name: "QR code for your authenticator app" })).toBeTruthy();
    expect(api.startTwoFactorSetup).toHaveBeenCalledTimes(1);

    await userEvent.type(screen.getByLabelText(/6-digit code/i), "12a345");
    expect((screen.getByLabelText(/6-digit code/i) as HTMLInputElement).value).toBe("12345");
    await click("Turn on");
    expect(toastTitles()).toContain("Enter the 6-digit code from your app.");
    expect(api.confirmTwoFactorSetup).not.toHaveBeenCalled();
  });

  it("cancels a started enrolment when skipped and keeps the recovery codes after enabling", async () => {
    session.user = makeUser({ displayName: "Alice" });
    api.startTwoFactorSetup.mockResolvedValue({ secret: "SECRET", otpauthURL: "otpauth://totp/x?secret=SECRET", expiresAt: "" });
    api.cancelTwoFactorSetup.mockResolvedValue(undefined);
    api.patchProfile.mockImplementation(async (body: object) => ({ ...(session.user as object), ...body }));
    renderWithI18n(<OnboardingOverlay />);
    await click("Get started");
    await click("Continue");
    await waitFor(() => expect(screen.getByLabelText("Setup key")).toBeTruthy());
    await click("Skip for now");
    await waitFor(() => expect(api.cancelTwoFactorSetup).toHaveBeenCalledTimes(1));
    await screen.findByText("Choose your colours");
  });

  it("shows recovery codes on the last step after a successful enrolment", async () => {
    const user = makeUser({ displayName: "Alice" });
    session.user = user;
    api.startTwoFactorSetup.mockResolvedValue({ secret: "SECRET", otpauthURL: "otpauth://totp/x?secret=SECRET", expiresAt: "" });
    api.confirmTwoFactorSetup.mockResolvedValue({ recoveryCodes: ["aaaa-1111", "bbbb-2222"], enabled: true, recoveryCount: 2 });
    auth.fetchMe.mockResolvedValue({ ...user, twoFactorEnabled: true });
    api.patchProfile.mockImplementation(async (body: object) => ({ ...user, ...body }));
    renderWithI18n(<OnboardingOverlay />);
    await click("Get started");
    await click("Continue");
    await waitFor(() => expect(screen.getByLabelText("Setup key")).toBeTruthy());
    await userEvent.type(screen.getByLabelText(/6-digit code/i), "123456");
    await click("Turn on");
    await screen.findByText("Choose your colours");
    await click("Continue");
    await screen.findByText("Time and appearance");
    await click("Continue");
    await screen.findByText("aaaa-1111");
    expect(screen.getByText("bbbb-2222")).toBeTruthy();
    expect(api.confirmTwoFactorSetup).toHaveBeenCalledWith("123456");
  });
});
