// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthPayload, LoginOptions } from "@/lib/api/types";
import { sessionStore } from "@/lib/api/session-store";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";
import { CHALLENGE_KEY, saveChallenge } from "../lib/handoff-storage";
import { DEFAULT_LOGIN_OPTIONS } from "../lib/login-options";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";

const router = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
let search = "";
vi.mock("next/navigation", () => ({ useRouter: () => router, useSearchParams: () => new URLSearchParams(search), usePathname: () => "/login" }));

const setup = vi.hoisted(() => ({ value: null as unknown }));
vi.mock("./use-login-options", () => ({ useLoginOptions: () => setup.value }));
const existing = vi.hoisted(() => ({ findExistingSession: vi.fn() }));
vi.mock("./existing-session", () => existing);
const api = vi.hoisted(() => ({ loginWithPassword: vi.fn(), verifySecondFactor: vi.fn(), startEmailSecondFactor: vi.fn() }));
vi.mock("./api", async (orig) => ({ ...(await orig<object>()), ...api }));
const platform = vi.hoisted(() => ({ desktop: false }));
vi.mock("@/lib/api/platform", async (orig) => ({ ...(await orig<object>()), isDesktop: () => platform.desktop }));
const bridge = vi.hoisted(() => ({ openExternal: vi.fn() }));
vi.mock("../desktop/bridge", async (orig) => ({ ...(await orig<object>()), openExternal: bridge.openExternal }));
vi.mock("./provider-flow", () => ({ startProviderFlow: vi.fn(), isSilentProviderError: () => false }));

import { LoginPage } from "./login-page";

const configure = (options: Partial<LoginOptions> = {}, defaultNext = "/chat", loaded = true) => {
  setup.value = { options: { ...DEFAULT_LOGIN_OPTIONS, ...options }, defaultNext, loaded };
};
const signedIn = { accessToken: "tok", sessionID: "sid", twoFactorRequired: false } as AuthPayload;

async function signIn() {
  await userEvent.type(await screen.findByLabelText(/account/i, { selector: "input" }), "alice");
  await userEvent.type(screen.getByLabelText(/^password/i, { selector: "input" }), "secret-pw");
  await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

beforeEach(() => {
  search = "";
  platform.desktop = false;
  Object.values(api).forEach((fn) => fn.mockReset());
  router.replace.mockReset();
  existing.findExistingSession.mockReset();
  existing.findExistingSession.mockResolvedValue({ result: "none", localMode: false });
  bridge.openExternal.mockReset();
  window.sessionStorage.clear();
  sessionStore.clear({ silent: true });
  clearToasts();
  configure();
});
afterEach(cleanup);

describe("LoginPage", () => {
  it("signs in and replaces the history entry with the requested page", async () => {
    search = "next=/files";
    api.loginWithPassword.mockResolvedValue(signedIn);
    renderWithI18n(<LoginPage />);
    await signIn();
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith("/files"));
    expect(api.loginWithPassword).toHaveBeenCalledWith("alice", "secret-pw");
    expect(sessionStore.token()).toBe("tok");
  });

  it("shows a loading status while the sign-in options are on their way, and hides the form from assistive tech", () => {
    configure({}, "/chat", false);
    const { container } = renderWithI18n(<LoginPage />);
    expect(screen.getByRole("status")).toBeTruthy();
    expect(container.querySelector("[aria-busy='true']")?.hasAttribute("inert")).toBe(true);
  });

  it("never follows an unsafe next value", async () => {
    search = "next=//evil.example";
    configure({}, "/projects");
    api.loginWithPassword.mockResolvedValue(signedIn);
    renderWithI18n(<LoginPage />);
    await signIn();
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith("/projects"));
  });

  it("redirects at once when a session already exists", async () => {
    search = "next=/library";
    existing.findExistingSession.mockResolvedValue({ result: "signed-in", localMode: false });
    renderWithI18n(<LoginPage />);
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith("/library"));
  });

  it("shows the second-factor prompt and finishes the sign-in with the code", async () => {
    api.loginWithPassword.mockResolvedValue({ twoFactorRequired: true, twoFactorChallengeToken: "challenge-token-123456789", verificationMethods: ["two_factor", "email"] } as AuthPayload);
    api.verifySecondFactor.mockResolvedValue(signedIn);
    renderWithI18n(<LoginPage />);
    await signIn();
    const code = await screen.findByLabelText(/authenticator code/i);
    expect(screen.getByRole("button", { name: "Use an email code instead" })).toBeTruthy();
    await userEvent.type(code, "123456");
    await userEvent.click(screen.getByRole("button", { name: "Verify" }));
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith("/chat"));
    expect(api.verifySecondFactor).toHaveBeenCalledWith({ challengeToken: "challenge-token-123456789", verificationMethod: "two_factor", code: "123456" });
  });

  it("shows the prompt straight away when a provider hand-off left a challenge", async () => {
    saveChallenge("stored-challenge-token-0001", ["email"]);
    renderWithI18n(<LoginPage />);
    await screen.findByLabelText(/email code/i);
    expect(api.loginWithPassword).not.toHaveBeenCalled();
  });

  it("goes back to the password form when the challenge is cancelled", async () => {
    saveChallenge("stored-challenge-token-0001", ["two_factor"]);
    renderWithI18n(<LoginPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Back to password sign-in" }));
    await screen.findByLabelText(/account/i, { selector: "input" });
    expect(window.sessionStorage.getItem(CHALLENGE_KEY)).toBeNull();
  });

  it("reports a login that returns neither a challenge nor a token", async () => {
    api.loginWithPassword.mockResolvedValue({ twoFactorRequired: false } as AuthPayload);
    renderWithI18n(<LoginPage />);
    await signIn();
    await waitFor(() => expect(toastTitles()).toContain("Sign-in did not complete. Please try again."));
    expect(router.replace).not.toHaveBeenCalled();
  });

  it("shows only provider buttons for a provider-only deployment", async () => {
    configure({
      usernameEnabled: false,
      emailEnabled: false,
      emailRegistrationEnabled: false,
      providerAuthBridge: { enabled: true, callbackBaseURL: "https://x", protocolVersion: 1 },
      providers: [{ publicID: "p", slug: "gh", name: "GitHub", type: "oauth2", logoURL: "", loginEnabled: true, registrationEnabled: true }],
    });
    renderWithI18n(<LoginPage />);
    await screen.findByRole("button", { name: /Continue with GitHub/ });
    expect(screen.queryByLabelText(/password/i)).toBeNull();
  });

  it("lands on the register form when registration is all that is offered", async () => {
    configure({ usernameEnabled: false, emailEnabled: true, emailRegistrationEnabled: true });
    renderWithI18n(<LoginPage />);
    expect(await screen.findByLabelText(/^email(?! code)/i, { selector: "input" })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Create an account" }));
    await screen.findByRole("heading", { name: "Create your account" });
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByRole("heading", { name: "Sign in to Riochat" });
  });

  it("prefills the reset email from a typed address and returns to sign-in afterwards", async () => {
    configure({ passwordResetEnabled: true, emailVerificationEnabled: true });
    renderWithI18n(<LoginPage />);
    await userEvent.type(await screen.findByLabelText(/account/i, { selector: "input" }), "me@x.io");
    await userEvent.click(screen.getByRole("button", { name: "Forgot password?" }));
    expect(((await screen.findByLabelText(/^email(?! code)/i, { selector: "input" })) as HTMLInputElement).value).toBe("me@x.io");
    await userEvent.click(screen.getByRole("button", { name: "Back to sign in" }));
    await screen.findByRole("heading", { name: "Sign in to Riochat" });
  });

  it("offers browser registration on desktop when a human check is required", async () => {
    platform.desktop = true;
    bridge.openExternal.mockResolvedValue(true);
    configure({ turnstileRegistrationEnabled: true, turnstileSiteKey: "site-key" });
    renderWithI18n(<LoginPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Register in browser" }));
    expect(bridge.openExternal).toHaveBeenCalledWith("/login");
    expect(screen.queryByRole("button", { name: "Create an account" })).toBeNull();
  });

  it("keeps the form collapsed and busy until the options arrive", () => {
    configure({}, "/chat", false);
    const { container } = renderWithI18n(<LoginPage />);
    expect(container.querySelector("[aria-busy='true']")).not.toBeNull();
  });

  it("takes the collapsed form out of the tab order and the accessibility tree while loading", () => {
    configure({}, "/chat", false);
    const { container } = renderWithI18n(<LoginPage />);
    expect(container.querySelector("[aria-busy='true']")?.hasAttribute("inert")).toBe(true);
    cleanup();
    configure({}, "/chat", true);
    const loaded = renderWithI18n(<LoginPage />);
    expect(loaded.container.querySelector("[aria-busy='false']")?.hasAttribute("inert")).toBe(false);
  });

  it("waits for the server's landing page before leaving with an existing session and no usable next", async () => {
    search = "next=https://evil.example";
    existing.findExistingSession.mockResolvedValue({ result: "signed-in", localMode: false });
    configure({}, "/chat", false);
    const view = renderWithI18n(<LoginPage />);
    await waitFor(() => expect(existing.findExistingSession).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(router.replace).not.toHaveBeenCalled();
    configure({}, "/workspace", true);
    view.rerender(
      <I18nProvider>
        <TooltipProvider>
          <LoginPage />
        </TooltipProvider>
      </I18nProvider>,
    );
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith("/workspace"));
    expect(router.replace).toHaveBeenCalledTimes(1);
  });

  it("does not wait for the options when next is a safe path", async () => {
    search = "next=/files";
    existing.findExistingSession.mockResolvedValue({ result: "signed-in", localMode: false });
    configure({}, "/chat", false);
    renderWithI18n(<LoginPage />);
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith("/files"));
  });
});
