// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import type { AuthPayload } from "@/lib/api/types";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";
import { DEFAULT_LOGIN_OPTIONS, deriveCapabilities } from "../lib/login-options";

const api = vi.hoisted(() => ({
  loginWithPassword: vi.fn(),
  verifySecondFactor: vi.fn(),
  startEmailSecondFactor: vi.fn(),
  startRegistrationCode: vi.fn(),
  completeRegistration: vi.fn(),
  startPasswordReset: vi.fn(),
  completePasswordReset: vi.fn(),
}));
vi.mock("./api", () => api);
vi.mock("../desktop/bridge", () => ({ openExternal: vi.fn(), shell: {} }));
vi.mock("@/lib/api/base-url", async (orig) => ({ ...(await orig<object>()), resolveApiBase: () => "" }));

import { PasswordForm } from "./password-form";
import { RegisterForm } from "./register-form";
import { ResetForm } from "./reset-form";
import { SecondFactorForm } from "./second-factor-form";

const payload = { accessToken: "a" } as AuthPayload;
const caps = (over = {}) => deriveCapabilities({ ...DEFAULT_LOGIN_OPTIONS, ...over }, { desktop: false });

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  clearToasts();
});
afterEach(cleanup);

describe("PasswordForm", () => {
  function setup(opts: { capabilities?: ReturnType<typeof caps>; onResult?: (p: AuthPayload) => void; onForgot?: (a: string) => void } = {}) {
    const onResult = opts.onResult ?? vi.fn();
    const onForgot = opts.onForgot ?? vi.fn();
    const Wrapper = () => {
      const [account, setAccount] = useState("");
      return <PasswordForm caps={opts.capabilities ?? caps()} account={account} onAccountChange={setAccount} onResult={onResult} onForgot={onForgot} />;
    };
    renderWithI18n(<Wrapper />);
    return { onResult, onForgot };
  }

  it("sends the trimmed account and the untouched password", async () => {
    api.loginWithPassword.mockResolvedValue(payload);
    const { onResult } = setup();
    await userEvent.type(screen.getByLabelText(/account/i, { selector: "input" }), "  alice ");
    await userEvent.type(screen.getByLabelText(/password/i, { selector: "input" }), " pw 12345 ");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(payload));
    expect(api.loginWithPassword).toHaveBeenCalledWith("alice", " pw 12345 ");
  });

  it("labels the account field by the enabled login kinds", () => {
    setup({ capabilities: caps({ usernameEnabled: false }) });
    expect(screen.getByLabelText(/^email/i, { selector: "input" })).toBeTruthy();
  });

  it("only offers password reset when the deployment allows it", async () => {
    const { onForgot } = setup({ capabilities: caps({ passwordResetEnabled: true }) });
    await userEvent.type(screen.getByLabelText(/account/i, { selector: "input" }), "me@example.com");
    await userEvent.click(screen.getByRole("button", { name: "Forgot password?" }));
    expect(onForgot).toHaveBeenCalledWith("me@example.com");
  });

  it("hides the reset link by default", () => {
    setup();
    expect(screen.queryByRole("button", { name: "Forgot password?" })).toBeNull();
  });

  it("explains a lockout in whole minutes", async () => {
    api.loginWithPassword.mockRejectedValue(new ApiError({ message: "locked", status: 423, errorCode: "auth.account_locked", retryAfterSeconds: 301 }));
    setup();
    await userEvent.type(screen.getByLabelText(/account/i, { selector: "input" }), "alice");
    await userEvent.type(screen.getByLabelText(/password/i, { selector: "input" }), "x");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(toastTitles().some((t) => t.includes("6 minutes"))).toBe(true));
  });

  it("sends a double click once", async () => {
    let release: (p: AuthPayload) => void = () => undefined;
    api.loginWithPassword.mockReturnValue(new Promise<AuthPayload>((resolve) => (release = resolve)));
    setup();
    await userEvent.type(screen.getByLabelText(/account/i, { selector: "input" }), "alice");
    await userEvent.type(screen.getByLabelText(/password/i, { selector: "input" }), "secret");
    const button = screen.getByRole("button", { name: "Sign in" });
    fireEvent.submit(button.closest("form") as HTMLFormElement);
    fireEvent.submit(button.closest("form") as HTMLFormElement);
    expect(api.loginWithPassword).toHaveBeenCalledTimes(1);
    release(payload);
  });
});

describe("SecondFactorForm", () => {
  const baseProps = () => ({ token: "t", methods: ["two_factor", "email"] as ("two_factor" | "email")[], passwordLogin: true, onResult: vi.fn(), onCancel: vi.fn(), onExpired: vi.fn() });

  it("refuses a short code without calling the server, then verifies a full one", async () => {
    api.verifySecondFactor.mockResolvedValue(payload);
    const props = baseProps();
    renderWithI18n(<SecondFactorForm {...props} />);
    const input = screen.getByLabelText(/authenticator code/i);
    await userEvent.type(input, "123");
    await userEvent.click(screen.getByRole("button", { name: "Verify" }));
    expect(api.verifySecondFactor).not.toHaveBeenCalled();
    expect(toastTitles()).toContain("Enter the full verification code.");
    await userEvent.type(input, "456");
    await userEvent.click(screen.getByRole("button", { name: "Verify" }));
    await waitFor(() => expect(props.onResult).toHaveBeenCalledWith(payload));
    expect(api.verifySecondFactor).toHaveBeenCalledWith({ challengeToken: "t", verificationMethod: "two_factor", code: "123456" });
  });

  it("keeps letters and hyphens for recovery codes", async () => {
    renderWithI18n(<SecondFactorForm {...baseProps()} />);
    const input = screen.getByLabelText(/authenticator code/i) as HTMLInputElement;
    await userEvent.type(input, "ab12-CD 34!");
    expect(input.value).toBe("ab12-CD34");
  });

  it("switches to the email method, strips non-digits and starts a resend countdown", async () => {
    api.startEmailSecondFactor.mockResolvedValue({ sent: true, expiresAt: "", debugCode: "999111" });
    renderWithI18n(<SecondFactorForm {...baseProps()} />);
    await userEvent.click(screen.getByRole("button", { name: "Use an email code instead" }));
    const input = screen.getByLabelText(/email code/i) as HTMLInputElement;
    await userEvent.type(input, "12a3-45678");
    expect(input.value).toBe("123456");
    await userEvent.click(screen.getByRole("button", { name: "Send code" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Resend in 60s" })).toBeTruthy());
    expect(api.startEmailSecondFactor).toHaveBeenCalledWith("t");
    expect(screen.getByText("Development code: 999111")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Resend in 60s" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("returns to the password form when the challenge has expired", async () => {
    api.verifySecondFactor.mockRejectedValue(new ApiError({ message: "x", status: 401, rawMessage: "two factor challenge expired" }));
    const props = baseProps();
    renderWithI18n(<SecondFactorForm {...props} />);
    await userEvent.type(screen.getByLabelText(/authenticator code/i), "123456");
    await userEvent.click(screen.getByRole("button", { name: "Verify" }));
    await waitFor(() => expect(props.onExpired).toHaveBeenCalled());
    expect(toastTitles()).toContain("That verification step timed out. Sign in again to restart it.");
  });

  it("offers a way back that depends on password login", async () => {
    const props = { ...baseProps(), passwordLogin: false };
    renderWithI18n(<SecondFactorForm {...props} />);
    await userEvent.click(screen.getByRole("button", { name: "Back to sign-in options" }));
    expect(props.onCancel).toHaveBeenCalled();
  });
});

describe("RegisterForm", () => {
  it("rejects a digits-only password before calling the server", async () => {
    renderWithI18n(<RegisterForm verificationEnabled={false} turnstileSiteKey="" turnstileRequired={false} onResult={vi.fn()} />);
    await userEvent.type(screen.getByLabelText(/^email(?! code)/i, { selector: "input" }), "a@b.co");
    await userEvent.type(screen.getByLabelText(/^password/i, { selector: "input" }), "12345678");
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));
    expect(api.completeRegistration).not.toHaveBeenCalled();
    expect(toastTitles()).toContain("Use 8 to 128 characters, and not digits only.");
  });

  it("requires the emailed code when verification is on and sends no turnstile token otherwise", async () => {
    api.startRegistrationCode.mockResolvedValue({ sent: true, expiresAt: "", debugCode: "" });
    api.completeRegistration.mockResolvedValue(payload);
    const onResult = vi.fn();
    renderWithI18n(<RegisterForm verificationEnabled turnstileSiteKey="" turnstileRequired={false} onResult={onResult} />);
    await userEvent.type(screen.getByLabelText(/^email(?! code)/i, { selector: "input" }), "a@b.co");
    await userEvent.type(screen.getByLabelText(/^password/i, { selector: "input" }), "goodpass1");
    const submit = screen.getByRole("button", { name: "Create account" }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Send code" }));
    await waitFor(() => expect(api.startRegistrationCode).toHaveBeenCalledWith("a@b.co", undefined));
    await userEvent.type(screen.getByLabelText(/email code/i), "123456");
    expect(submit.disabled).toBe(false);
    await userEvent.click(submit);
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(payload));
    expect(api.completeRegistration).toHaveBeenCalledWith({ email: "a@b.co", password: "goodpass1", code: "123456", turnstileToken: undefined });
  });

  it("sends an empty code when verification is off", async () => {
    api.completeRegistration.mockResolvedValue(payload);
    renderWithI18n(<RegisterForm verificationEnabled={false} turnstileSiteKey="" turnstileRequired={false} onResult={vi.fn()} />);
    await userEvent.type(screen.getByLabelText(/^email(?! code)/i, { selector: "input" }), "a@b.co");
    await userEvent.type(screen.getByLabelText(/^password/i, { selector: "input" }), "goodpass1");
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));
    await waitFor(() => expect(api.completeRegistration).toHaveBeenCalledWith(expect.objectContaining({ code: "" })));
  });
});

describe("ResetForm", () => {
  it("keeps Send disabled for a blank address and finishes with the email handed back", async () => {
    api.startPasswordReset.mockResolvedValue({ sent: true, expiresAt: "", debugCode: "" });
    api.completePasswordReset.mockResolvedValue(true);
    const onDone = vi.fn();
    renderWithI18n(<ResetForm initialEmail="" onDone={onDone} />);
    expect((screen.getByRole("button", { name: "Send code" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(screen.getByLabelText(/^email(?! code)/i, { selector: "input" }), " me@x.io ");
    await userEvent.type(screen.getByLabelText(/new password/i), "newpass99");
    await userEvent.click(screen.getByRole("button", { name: "Send code" }));
    await waitFor(() => expect(api.startPasswordReset).toHaveBeenCalledWith("me@x.io"));
    await userEvent.type(screen.getByLabelText(/email code/i), "654321");
    await userEvent.click(screen.getByRole("button", { name: "Set new password" }));
    await waitFor(() => expect(onDone).toHaveBeenCalledWith("me@x.io"));
    expect(api.completePasswordReset).toHaveBeenCalledWith({ email: "me@x.io", code: "654321", newPassword: "newpass99" });
  });
});
