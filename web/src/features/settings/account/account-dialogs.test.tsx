// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthUser } from "@/lib/api/types";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const api = vi.hoisted(() => ({
  passwordChangeStart: vi.fn(),
  passwordChangeComplete: vi.fn(),
  mfaSetupConfirm: vi.fn(),
  mfaSetupCancel: vi.fn(),
  mfaDisable: vi.fn(),
  mfaRegenerate: vi.fn(),
  deleteStart: vi.fn(),
  deleteAccount: vi.fn(),
}));
vi.mock("./api", () => api);
const endSession = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock("@/lib/api/session-store", async (orig) => ({ ...(await orig<object>()), endSession }));

import { DeleteAccountDialog } from "./delete-account-dialog";
import { PasswordDialog } from "./password-dialog";
import { TwoFactorDialog } from "./two-factor-dialog";

const user = (over: Partial<AuthUser> = {}): AuthUser => ({ passwordEnabled: true, mustResetPassword: false, email: "a@b.co", ...over }) as AuthUser;

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  endSession.mockClear();
  clearToasts();
});
afterEach(cleanup);

describe("PasswordDialog", () => {
  it("keeps Save off until the new password meets the policy and the current one is filled", async () => {
    const u = userEvent.setup();
    renderWithApp(<PasswordDialog open onOpenChange={() => undefined} user={user()} methods={["none"]} />);
    const save = await screen.findByRole("button", { name: "Save" });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    await u.type(screen.getByLabelText("New password"), "12345678");
    expect((save as HTMLButtonElement).disabled).toBe(true); // digits only
    await u.type(screen.getByLabelText("Current password"), "old-password");
    await u.clear(screen.getByLabelText("New password"));
    await u.type(screen.getByLabelText("New password"), "better-one-1");
    expect((save as HTMLButtonElement).disabled).toBe(false);
  });

  it("submits straight away when no verification is needed, then signs out", async () => {
    api.passwordChangeComplete.mockResolvedValue(undefined);
    const u = userEvent.setup();
    renderWithApp(<PasswordDialog open onOpenChange={() => undefined} user={user()} methods={["none"]} />);
    await u.type(await screen.findByLabelText("Current password"), "old-password");
    await u.type(screen.getByLabelText("New password"), "better-one-1");
    await u.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.passwordChangeComplete).toHaveBeenCalledTimes(1));
    expect(api.passwordChangeComplete).toHaveBeenCalledWith({ currentPassword: "old-password", newPassword: "better-one-1", verificationMethod: "none", code: "" });
    await waitFor(() => expect(endSession).toHaveBeenCalled());
    expect(toastTitles()).toContain("Password changed");
  });

  it("asks for a code first when a method exists, and sends it with the passwords", async () => {
    api.passwordChangeComplete.mockResolvedValue(undefined);
    const u = userEvent.setup();
    renderWithApp(<PasswordDialog open onOpenChange={() => undefined} user={user()} methods={["two_factor"]} />);
    await u.type(await screen.findByLabelText("Current password"), "old-password");
    await u.type(screen.getByLabelText("New password"), "better-one-1");
    await u.click(screen.getByRole("button", { name: "Continue" }));
    expect(api.passwordChangeComplete).not.toHaveBeenCalled();
    const confirm = await screen.findByRole("button", { name: "Confirm" });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    await u.type(screen.getByRole("textbox"), "123456");
    await u.click(confirm);
    await waitFor(() => expect(api.passwordChangeComplete).toHaveBeenCalled());
    expect(api.passwordChangeComplete.mock.calls[0]?.[0]).toMatchObject({ verificationMethod: "two_factor", code: "123456" });
  });

  it("a forced reset has no current-password field and cannot be cancelled", async () => {
    api.passwordChangeComplete.mockResolvedValue(undefined);
    const u = userEvent.setup();
    renderWithApp(<PasswordDialog open onOpenChange={() => undefined} user={user({ mustResetPassword: true })} methods={["two_factor"]} />);
    expect(await screen.findByText("Choose a new password")).toBeTruthy();
    expect(screen.queryByLabelText("Current password")).toBeNull();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    await u.type(screen.getByLabelText("New password"), "better-one-1");
    await u.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.passwordChangeComplete).toHaveBeenCalled());
    expect(api.passwordChangeComplete.mock.calls[0]?.[0]).toEqual({ currentPassword: undefined, newPassword: "better-one-1", verificationMethod: "none", code: "" });
  });

  it("limits the email code resend to once a minute", async () => {
    api.passwordChangeStart.mockResolvedValue({ sent: true, debugCode: "" });
    const u = userEvent.setup();
    renderWithApp(<PasswordDialog open onOpenChange={() => undefined} user={user()} methods={["email"]} />);
    await u.type(await screen.findByLabelText("Current password"), "old-password");
    await u.type(screen.getByLabelText("New password"), "better-one-1");
    await u.click(screen.getByRole("button", { name: "Continue" }));
    const send = await screen.findByRole("button", { name: "Send code" });
    await u.click(send);
    await waitFor(() => expect(screen.getByRole("button", { name: /Send again in \d+s/ })).toBeTruthy());
    await u.click(screen.getByRole("button", { name: /Send again in \d+s/ }));
    expect(api.passwordChangeStart).toHaveBeenCalledTimes(1);
  });
});

describe("TwoFactorDialog", () => {
  const setup = { secret: "ABCDEFGH", otpauthURL: "otpauth://totp/x?secret=ABCDEFGH", expiresAt: new Date(Date.now() + 5 * 60_000).toISOString() };

  it("asks for six digits before confirming", async () => {
    const u = userEvent.setup();
    renderWithApp(<TwoFactorDialog mode="setup" setup={setup} onOpenChange={() => undefined} onStartAgain={async () => null} onChanged={() => undefined} />);
    await u.type(await screen.findByLabelText("Code from your app"), "12345");
    await u.click(screen.getByRole("button", { name: "Turn on" }));
    expect(toastTitles()).toContain("Enter the full code first");
    expect(api.mfaSetupConfirm).not.toHaveBeenCalled();
  });

  it("shows recovery codes after enabling and clears them when closed", async () => {
    api.mfaSetupConfirm.mockResolvedValue(["aaaa-1111", "bbbb-2222"]);
    const onChanged = vi.fn();
    const onOpenChange = vi.fn();
    const u = userEvent.setup();
    renderWithApp(<TwoFactorDialog mode="setup" setup={setup} onOpenChange={onOpenChange} onStartAgain={async () => null} onChanged={onChanged} />);
    await u.type(await screen.findByLabelText("Code from your app"), "123456");
    await u.click(screen.getByRole("button", { name: "Turn on" }));
    expect(await screen.findByText("aaaa-1111")).toBeTruthy();
    expect(onChanged).toHaveBeenCalled();
    await u.click(screen.getByRole("button", { name: "Done" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(api.mfaSetupCancel).not.toHaveBeenCalled(); // setup was completed, nothing to cancel
  });

  it("disables Turn on and offers a new setup once expired", async () => {
    const expired = { ...setup, expiresAt: new Date(Date.now() - 1000).toISOString() };
    const again = vi.fn(async () => setup);
    const u = userEvent.setup();
    renderWithApp(<TwoFactorDialog mode="setup" setup={expired} onOpenChange={() => undefined} onStartAgain={again} onChanged={() => undefined} />);
    const enable = await screen.findByRole("button", { name: "Turn on" });
    expect((enable as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("This setup has expired. Generate a new one.")).toBeTruthy();
    await u.click(screen.getByRole("button", { name: "Generate again" }));
    expect(again).toHaveBeenCalledTimes(1);
  });

  it("cancels the pending setup on the server when closed", async () => {
    api.mfaSetupCancel.mockResolvedValue(undefined);
    const onOpenChange = vi.fn();
    const u = userEvent.setup();
    renderWithApp(<TwoFactorDialog mode="setup" setup={setup} onOpenChange={onOpenChange} onStartAgain={async () => null} onChanged={() => undefined} />);
    await u.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(api.mfaSetupCancel).toHaveBeenCalledTimes(1));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("disables with an authenticator code and closes", async () => {
    api.mfaDisable.mockResolvedValue(undefined);
    const onChanged = vi.fn();
    const onOpenChange = vi.fn();
    const u = userEvent.setup();
    renderWithApp(<TwoFactorDialog mode="manage" setup={null} onOpenChange={onOpenChange} onStartAgain={async () => null} onChanged={onChanged} />);
    await u.click(await screen.findByRole("button", { name: "Turn off" }));
    expect(toastTitles()).toContain("Enter the full code first");
    await u.type(screen.getByLabelText("Authenticator or recovery code"), "123456");
    await u.click(screen.getByRole("button", { name: "Turn off" }));
    await waitFor(() => expect(api.mfaDisable).toHaveBeenCalledWith("123456"));
    expect(onChanged).toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe("DeleteAccountDialog", () => {
  it("explains and blocks when no verification method exists", async () => {
    renderWithApp(<DeleteAccountDialog open onOpenChange={() => undefined} methods={["none"]} />);
    expect(await screen.findByText(/needs a verified email or two-factor/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("asks for proof, deletes, and signs out without a success toast", async () => {
    api.deleteAccount.mockResolvedValue(undefined);
    const u = userEvent.setup();
    renderWithApp(<DeleteAccountDialog open onOpenChange={() => undefined} methods={["two_factor"]} />);
    await u.click(await screen.findByRole("button", { name: "Continue" }));
    await u.type(await screen.findByRole("textbox"), "654321");
    await u.click(screen.getByRole("button", { name: "Delete account" }));
    await waitFor(() => expect(api.deleteAccount).toHaveBeenCalledWith("two_factor", "654321"));
    await waitFor(() => expect(endSession).toHaveBeenCalled());
    expect(toastTitles()).toEqual([]);
  });
});
