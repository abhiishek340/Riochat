"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "@/components/ui/toast-store";
import { fetchMe } from "@/lib/api/endpoints/auth";
import { isApiError } from "@/lib/api/errors";
import { endSession } from "@/lib/api/session-store";
import type { AuthUser } from "@/lib/api/types";
import { normalizeLocale, type Locale } from "@/lib/i18n/config";
import { useI18n } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { publishUserUpdate } from "@/lib/session/provider";
import { useTheme } from "@/lib/theme/provider";
import type { Accent } from "@/lib/theme/config";
import { CODE_LENGTH, digitsOnly, isValidPassword, normalizeUsername } from "../lib/password-policy";
import { cancelTwoFactorSetup, completeOnboarding, confirmTwoFactorSetup, patchProfile, patchUsername, startTwoFactorSetup, type TwoFactorSetup } from "./api";
import {
  appearanceWithPreset,
  detectTimezone,
  initialTimezone,
  isBootstrap,
  LAST_STEP,
  personalizePatch,
  planAccountSave,
  previousStep,
  storedPreset,
  validateAccount,
  type AccountProblem,
} from "./model";

export type SetupState = "unavailable" | "enabled" | "starting" | "ready" | "failed";

const PROBLEM_KEY: Record<AccountProblem, "problemUsername" | "problemShape" | "problemName" | "problemPassword"> = {
  changeUsername: "problemUsername",
  usernameShape: "problemShape",
  displayName: "problemName",
  password: "problemPassword",
};

export function useOnboardingFlow(user: AuthUser, onHold: (holding: boolean) => void) {
  const { t, locale, setLocale } = useI18n();
  const errorMessage = useErrorMessage();
  const theme = useTheme();

  const [step, setStep] = useState(1);
  const [busy, setBusy] = useState(false);
  const [username, setUsernameState] = useState(user.username);
  const [displayName, setDisplayName] = useState(user.displayName);
  const [password, setPassword] = useState("");
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null);
  const [setupState, setSetupState] = useState<SetupState>(user.twoFactorAvailable ? (user.twoFactorEnabled ? "enabled" : "starting") : "unavailable");
  const [code, setCode] = useState("");
  const [skipped, setSkipped] = useState(false);
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [preset, setPresetState] = useState<Accent>(() => storedPreset(user.appearancePreferences));
  const deviceZone = useMemo(() => detectTimezone(), []);
  const [timezone, setTimezone] = useState(() => initialTimezone(user.timezone, deviceZone));
  const setupRequested = useRef(false);
  const working = useRef(false);

  const bootstrap = isBootstrap(user);
  const profileLocale = normalizeLocale(user.locale);

  useEffect(() => onHold(recoveryCodes.length > 0), [recoveryCodes, onHold]);

  /** Runs one saving action at a time and always releases the buttons. */
  const guarded = useCallback(async (action: () => Promise<void>) => {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    try {
      await action();
    } finally {
      working.current = false;
      setBusy(false);
    }
  }, []);

  const begin = useCallback(async () => {
    setSetupState("starting");
    try {
      const started = await startTwoFactorSetup();
      setSetup(started);
      setCode("");
      setSetupState("ready");
    } catch (error) {
      setupRequested.current = false; // the attempt may be retried
      setSetupState("failed");
      toast.error(t("onboarding.twoFactor.startFailed"), { description: errorMessage(error) });
    }
  }, [t, errorMessage]);

  // Start the enrollment exactly once, the first time the step is reached.
  useEffect(() => {
    if (step !== 3 || setupRequested.current) return;
    if (!user.twoFactorAvailable || user.twoFactorEnabled) return;
    setupRequested.current = true;
    void begin();
  }, [step, user.twoFactorAvailable, user.twoFactorEnabled, begin]);

  const chooseLocale = useCallback((next: Locale) => void setLocale(next), [setLocale]);

  const start = () =>
    guarded(async () => {
      if (locale !== profileLocale) {
        try {
          const updated = await patchProfile({ locale });
          publishUserUpdate(updated);
        } catch (error) {
          toast.error(t("onboarding.welcome.saveFailed"), { description: errorMessage(error) });
          return;
        }
      }
      setStep(2);
    });

  const saveAccount = () =>
    guarded(async () => {
      const draft = { username, displayName, password };
      const problem = validateAccount(user, draft);
      if (problem) {
        toast.error(t(`onboarding.account.${PROBLEM_KEY[problem]}`));
        return;
      }
      const plan = planAccountSave(user, draft);
      try {
        let latest: AuthUser | null = null;
        if (plan.username !== null) latest = await patchUsername(plan.username);
        if (plan.displayName !== null) latest = await patchProfile({ displayName: plan.displayName });
        if (latest) publishUserUpdate(latest);
        setStep(3);
      } catch (error) {
        toast.error(t("onboarding.account.saveFailed"), { description: errorMessage(error) });
      }
    });

  const enableTwoFactor = () =>
    guarded(async () => {
      if (user.twoFactorEnabled || !setup) return;
      if (code.length !== CODE_LENGTH) {
        toast.error(t("onboarding.twoFactor.codeRequired"));
        return;
      }
      try {
        const confirmed = await confirmTwoFactorSetup(code);
        if (!confirmed.enabled) throw new Error("errors.onboarding.twoFactorNotEnabled");
        const fresh = await fetchMe();
        publishUserUpdate(fresh);
        if (!fresh.twoFactorEnabled) throw new Error("errors.onboarding.twoFactorNotSynced");
        setRecoveryCodes(confirmed.recoveryCodes);
        setSkipped(false);
        setSetup(null);
        setSetupState("enabled");
        setStep(4);
      } catch (error) {
        const own = error instanceof Error ? error.message : "";
        if (own === "errors.onboarding.twoFactorNotEnabled") toast.error(t("onboarding.twoFactor.notEnabled"));
        else if (own === "errors.onboarding.twoFactorNotSynced") toast.error(t("onboarding.twoFactor.notSynced"));
        else toast.error(t("onboarding.twoFactor.enableFailed"), { description: errorMessage(error) });
      }
    });

  const skipTwoFactor = () =>
    guarded(async () => {
      try {
        if (setup) {
          await cancelTwoFactorSetup();
          // Coming back to this step later may enrol afresh.
          setupRequested.current = false;
          setSetupState("starting");
        }
        setSetup(null);
        setSkipped(true);
        setStep(4);
      } catch (error) {
        toast.error(t("onboarding.twoFactor.skipFailed"), { description: errorMessage(error) });
      }
    });

  const choosePreset = useCallback(
    (next: Accent) => {
      setPresetState(next);
      theme.setAccent(next);
    },
    [theme],
  );

  const saveTheme = () =>
    guarded(async () => {
      const next = appearanceWithPreset(user.appearancePreferences, preset);
      if (next !== user.appearancePreferences) {
        try {
          publishUserUpdate(await patchProfile({ appearancePreferences: next }));
        } catch (error) {
          toast.error(t("onboarding.theme.saveFailed"), { description: errorMessage(error) });
          return;
        }
      }
      setStep(5);
    });

  const savePersonalization = () =>
    guarded(async () => {
      const patch = personalizePatch(user.timezone, timezone);
      if (Object.keys(patch).length > 0) {
        try {
          publishUserUpdate(await patchProfile({ ...patch }));
        } catch (error) {
          toast.error(t("onboarding.personalize.saveFailed"), { description: errorMessage(error) });
          return;
        }
      }
      setStep(6);
    });

  const finish = () =>
    guarded(async () => {
      if (bootstrap && !isValidPassword(password)) {
        toast.error(t("onboarding.account.problemPassword"));
        setStep(2);
        return;
      }
      try {
        const fresh = await fetchMe();
        publishUserUpdate(fresh);
        if (fresh.twoFactorAvailable && !fresh.twoFactorEnabled && !skipped) {
          toast.error(t("onboarding.twoFactor.notSynced"));
          return;
        }
        const done = await completeOnboarding(bootstrap ? password : undefined);
        if (bootstrap) {
          toast.success(t("onboarding.ready.relogin"));
          await endSession();
          return;
        }
        toast.success(t("onboarding.ready.done"));
        onHold(false);
        publishUserUpdate(done);
      } catch (error) {
        if (isApiError(error) && error.errorCode === "auth.password_reuse_not_allowed") setStep(2);
        toast.error(t("onboarding.ready.failed"), { description: errorMessage(error) });
      }
    });

  const back = () => {
    const prev = previousStep(step);
    if (prev !== null) setStep(prev);
  };

  return {
    step,
    last: LAST_STEP,
    busy,
    bootstrap,
    canGoBack: previousStep(step) !== null,
    back,
    // welcome
    locale,
    chooseLocale,
    start,
    // account
    username,
    setUsername: (value: string) => setUsernameState(normalizeUsername(value).slice(0, 16)),
    displayName,
    setDisplayName,
    password,
    setPassword,
    saveAccount,
    // two-factor
    setup,
    setupState,
    retrySetup: () => {
      setupRequested.current = true;
      void begin();
    },
    code,
    setCode: (value: string) => setCode(digitsOnly(value, CODE_LENGTH)),
    enableTwoFactor,
    skipTwoFactor,
    recoveryCodes,
    // theme
    preset,
    choosePreset,
    saveTheme,
    // personalisation
    timezone,
    setTimezone,
    deviceZone,
    mode: theme.mode,
    setMode: theme.setMode,
    savePersonalization,
    // ready
    finish,
  };
}

export type OnboardingFlow = ReturnType<typeof useOnboardingFlow>;
