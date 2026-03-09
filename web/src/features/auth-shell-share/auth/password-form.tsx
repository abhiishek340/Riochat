"use client";

import { useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import type { AuthPayload } from "@/lib/api/types";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import type { AuthCapabilities } from "../lib/login-options";
import { loginWithPassword } from "./api";
import { isChallengeExpired, lockMinutes } from "./sign-in";

export function PasswordForm({
  caps,
  account,
  onAccountChange,
  onResult,
  onForgot,
}: {
  caps: AuthCapabilities;
  account: string;
  onAccountChange: (value: string) => void;
  onResult: (payload: AuthPayload) => Promise<void> | void;
  onForgot: (account: string) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const inFlight = useRef(false);

  const label =
    caps.accountMode === "both" ? t("authFlow.login.account") : caps.accountMode === "email" ? t("authFlow.login.email") : t("authFlow.login.username");
  const placeholder =
    caps.accountMode === "both" ? t("authFlow.login.accountPlaceholder") : caps.accountMode === "email" ? t("authFlow.login.email") : t("authFlow.login.username");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    try {
      const payload = await loginWithPassword(account.trim(), password);
      await onResult(payload);
    } catch (error) {
      const minutes = lockMinutes(error);
      if (minutes !== null) toast.error(t("authFlow.login.locked", { minutes }));
      else if (isChallengeExpired(error)) toast.error(t("authFlow.login.challengeExpired"));
      else toast.error(errorMessage(error, t("authFlow.login.retry")));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="grid gap-4" aria-label={t("authFlow.login.formLabel")}>
      <Field required>
        <FieldLabel>{label}</FieldLabel>
        <Input
          name="username"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          placeholder={placeholder}
          value={account}
          onChange={(e) => onAccountChange(e.target.value)}
        />
      </Field>
      <Field required>
        <div className="flex items-center justify-between gap-2">
          <FieldLabel>{t("authFlow.login.password")}</FieldLabel>
          {caps.passwordReset ? (
            <button
              type="button"
              onClick={() => onForgot(account)}
              className="rounded-sm text-[13px] font-medium text-primary outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
            >
              {t("authFlow.login.forgot")}
            </button>
          ) : null}
        </div>
        <Input name="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      <Button type="submit" loading={busy} disabled={!account.trim() || !password}>
        {busy ? t("authFlow.login.submitting") : t("authFlow.login.submit")}
      </Button>
    </form>
  );
}
