"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { toast } from "@/components/ui/toast-store";
import type { AuthPayload } from "@/lib/api/types";
import { isDesktop } from "@/lib/api/platform";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { safeNextPath } from "@/lib/utils/navigation";
import { clearChallenge, readChallenge, type TwoFactorMethod } from "../lib/handoff-storage";
import { correctMode, deriveCapabilities, type AuthMode } from "../lib/login-options";
import { findExistingSession } from "./existing-session";
import { PasswordForm } from "./password-form";
import { ProviderButtons } from "./provider-buttons";
import { RegisterForm, RegisterInBrowser } from "./register-form";
import { ResetForm } from "./reset-form";
import { SecondFactorForm } from "./second-factor-form";
import { settleAuth } from "./sign-in";
import { useLoginOptions } from "./use-login-options";

interface Challenge {
  token: string;
  methods: TwoFactorMethod[];
}

/** `/login`: password, second factor, registration, reset and provider buttons in one card. */
export function LoginPage() {
  const t = useT();
  const router = useRouter();
  const params = useSearchParams();
  const setup = useLoginOptions();

  const [desktop, setDesktop] = useState(false);
  const [localMode, setLocalMode] = useState(false);
  const [localFailed, setLocalFailed] = useState(false);
  const [mode, setMode] = useState<AuthMode>("login");
  const [challenge, setChallenge] = useState<Challenge | null>(null);
  const [account, setAccount] = useState("");
  const [resetEmail, setResetEmail] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [signedInAlready, setSignedInAlready] = useState(false);

  const destination = useMemo(() => safeNextPath(params.get("next"), setup.defaultNext), [params, setup.defaultNext]);
  const caps = useMemo(() => deriveCapabilities(setup.options, { desktop }), [setup.options, desktop]);

  // Who is already signed in, and is a provider hand-off waiting on a second factor?
  useEffect(() => {
    setDesktop(isDesktop());
    const pending = readChallenge();
    if (pending) setChallenge({ token: pending.token, methods: pending.methods });
  }, []);

  useEffect(() => {
    let live = true;
    setLocalFailed(false);
    setSignedInAlready(false);
    void findExistingSession().then(({ result, localMode: local }) => {
      if (!live) return;
      setLocalMode(local);
      if (result === "signed-in") setSignedInAlready(true);
      else if (result === "local-failed") setLocalFailed(true);
    });
    return () => {
      live = false;
    };
  }, [attempt]);

  // An invalid or missing `next` falls back to the server's landing page, so wait for that document
  // before leaving; a valid `next` needs nothing from the server.
  const nextIsExplicit = safeNextPath(params.get("next"), "") !== "";
  useEffect(() => {
    if (signedInAlready && (setup.loaded || nextIsExplicit)) router.replace(destination);
  }, [signedInAlready, setup.loaded, nextIsExplicit, destination, router]);

  // Move to a mode this deployment actually offers.
  useEffect(() => {
    if (!setup.loaded) return;
    setMode((current) => correctMode(current, caps));
  }, [caps, setup.loaded]);

  const finish = useCallback(
    async (payload: AuthPayload) => {
      const outcome = await settleAuth(payload);
      if (outcome.kind === "challenge") {
        clearChallenge();
        setChallenge({ token: outcome.token, methods: outcome.methods });
      } else if (outcome.kind === "empty") {
        toast.error(t("authFlow.login.failed"));
      } else {
        clearChallenge();
        router.replace(destination);
      }
    },
    [destination, router, t],
  );

  const cancelChallenge = useCallback(() => {
    clearChallenge();
    setChallenge(null);
  }, []);

  if (localMode) {
    return (
      <div role="status" className="grid justify-items-center gap-4 py-6 text-center">
        {localFailed ? (
          <>
            <p className="text-sm text-muted-foreground">{t("authFlow.local.failed")}</p>
            <Button variant="outline" onClick={() => setAttempt((n) => n + 1)}>
              {t("common.actions.retry")}
            </Button>
          </>
        ) : (
          <>
            <Spinner className="size-5" />
            <p className="text-sm text-muted-foreground">{t("authFlow.local.starting")}</p>
          </>
        )}
      </div>
    );
  }

  const secondFactor = mode === "login" && challenge !== null;
  const showProviders = mode === "login" && !secondFactor && caps.providers.length > 0;
  const nothingToDo = setup.loaded && !caps.passwordLogin && caps.providers.length === 0 && !caps.registrationInApp && !caps.registerInBrowser;

  const heading =
    mode === "register"
      ? t("authFlow.register.title")
      : mode === "reset"
        ? t("authFlow.reset.title")
        : secondFactor
          ? t("authFlow.verify.title")
          : t("authFlow.login.title");
  const subheading =
    mode === "register"
      ? t("authFlow.register.subtitle")
      : mode === "reset"
        ? t("authFlow.reset.subtitle")
        : secondFactor
          ? t("authFlow.verify.subtitle")
          : t("authFlow.login.subtitle");

  const canToggleRegister = caps.registrationInApp || caps.registerInBrowser;

  return (
    <div className="grid gap-6">
      <div className="grid gap-1.5">
        <h1 className="text-2xl font-semibold tracking-tight">{heading}</h1>
        <p className="text-sm text-muted-foreground">{subheading}</p>
      </div>

      {!setup.loaded ? (
        <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner className="size-4" />
          {t("common.states.loading")}
        </div>
      ) : null}

      {/* Collapsed (and marked busy) until the server's options arrive, so the form never reshuffles. */}
      <div
        aria-busy={!setup.loaded}
        // Collapsed content must not stay reachable by keyboard or screen reader.
        inert={!setup.loaded}
        className={cn(
          "grid transition-[grid-template-rows,opacity] duration-300 ease-out motion-reduce:transition-none",
          setup.loaded ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0",
        )}
      >
        <div className="min-h-0 overflow-hidden">
          <div className="grid gap-5 p-0.5">
            {nothingToDo ? <p className="rounded-lg border bg-muted/40 p-4 text-sm text-muted-foreground">{t("authFlow.login.unavailable")}</p> : null}

            {mode === "login" && secondFactor && challenge ? (
              <SecondFactorForm
                token={challenge.token}
                methods={challenge.methods}
                passwordLogin={caps.passwordLogin}
                onResult={finish}
                onCancel={cancelChallenge}
                onExpired={cancelChallenge}
              />
            ) : null}

            {mode === "login" && !secondFactor && caps.passwordLogin ? (
              <PasswordForm
                caps={caps}
                account={account}
                onAccountChange={setAccount}
                onResult={finish}
                onForgot={(typed) => {
                  setResetEmail(typed.includes("@") ? typed.trim() : "");
                  setMode("reset");
                }}
              />
            ) : null}

            {mode === "register" && caps.registrationInApp ? (
              <RegisterForm
                verificationEnabled={setup.options.emailVerificationEnabled}
                turnstileSiteKey={setup.options.turnstileSiteKey.trim()}
                turnstileRequired={caps.turnstileRequired}
                onResult={finish}
              />
            ) : null}

            {mode === "reset" && caps.passwordReset ? (
              <ResetForm
                initialEmail={resetEmail}
                onDone={(email) => {
                  setAccount(email);
                  setMode("login");
                }}
              />
            ) : null}

            {showProviders ? (
              <>
                {caps.passwordLogin ? (
                  <div className="flex items-center gap-3 text-xs uppercase tracking-wide text-muted-foreground" aria-hidden>
                    <span className="h-px flex-1 bg-border" />
                    {t("authFlow.providers.or")}
                    <span className="h-px flex-1 bg-border" />
                  </div>
                ) : null}
                <ProviderButtons providers={caps.providers} bridgeEnabled={setup.options.providerAuthBridge.enabled} intent="login" next={destination} />
              </>
            ) : null}

            {mode === "login" && !secondFactor && caps.registerInBrowser ? <RegisterInBrowser /> : null}

            <FooterLinks
              mode={mode}
              canToggleRegister={caps.registrationInApp && canToggleRegister}
              onMode={(next) => setMode(next)}
              hidden={secondFactor}
            />
          </div>
        </div>
      </div>
    </div>
  );
}

function FooterLinks({
  mode,
  canToggleRegister,
  onMode,
  hidden,
}: {
  mode: AuthMode;
  canToggleRegister: boolean;
  onMode: (mode: AuthMode) => void;
  hidden: boolean;
}) {
  const t = useT();
  if (hidden) return null;
  const link =
    "rounded-sm font-medium text-primary outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring";
  if (mode === "reset") {
    return (
      <p className="text-center text-sm text-muted-foreground">
        {t("authFlow.reset.remembered")}{" "}
        <button type="button" className={link} onClick={() => onMode("login")}>
          {t("authFlow.reset.back")}
        </button>
      </p>
    );
  }
  if (!canToggleRegister) return null;
  return (
    <p className="text-center text-sm text-muted-foreground">
      {mode === "register" ? t("authFlow.login.haveAccount") : t("authFlow.login.noAccount")}{" "}
      <button type="button" className={link} onClick={() => onMode(mode === "register" ? "login" : "register")}>
        {mode === "register" ? t("authFlow.login.signIn") : t("authFlow.login.createAccount")}
      </button>
    </p>
  );
}
