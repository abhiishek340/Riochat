"use client";

import { useRef, useState, type FormEvent } from "react";
import { ExternalLink } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { resolveApiBase } from "@/lib/api/base-url";
import type { AuthPayload } from "@/lib/api/types";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { openExternal } from "../desktop/bridge";
import { CODE_LENGTH, digitsOnly, isValidPassword } from "../lib/password-policy";
import { completeRegistration, startRegistrationCode } from "./api";
import { Turnstile } from "./turnstile";
import { RESEND_SECONDS, useCooldown } from "./use-cooldown";

export function RegisterForm({
  verificationEnabled,
  turnstileSiteKey,
  turnstileRequired,
  onResult,
}: {
  verificationEnabled: boolean;
  turnstileSiteKey: string;
  turnstileRequired: boolean;
  onResult: (payload: AuthPayload) => Promise<void> | void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [sentOnce, setSentOnce] = useState(false);
  const [debugCode, setDebugCode] = useState("");
  const [sending, setSending] = useState(false);
  const [busy, setBusy] = useState(false);
  const [turnstileToken, setTurnstileToken] = useState("");
  const [widgetKey, setWidgetKey] = useState(0);
  const cooldown = useCooldown();
  const inFlight = useRef(false);

  const spendToken = () => {
    setTurnstileToken("");
    setWidgetKey((k) => k + 1);
  };

  const changeEmail = (value: string) => {
    setEmail(value);
    setSentOnce(false);
    setDebugCode("");
    cooldown.reset();
  };

  const sendCode = async () => {
    if (!verificationEnabled || sending || cooldown.seconds > 0) return;
    if (turnstileRequired && !turnstileToken) {
      toast.error(t("authFlow.register.turnstileRequired"));
      return;
    }
    setSending(true);
    const usedToken = turnstileRequired && turnstileToken !== "";
    try {
      const result = await startRegistrationCode(email.trim(), turnstileRequired ? turnstileToken : undefined);
      if (result.sent) {
        setSentOnce(true);
        setDebugCode(result.debugCode);
        cooldown.start(RESEND_SECONDS);
      }
    } catch (error) {
      toast.error(errorMessage(error, t("authFlow.register.sendFailed")));
    } finally {
      setSending(false);
      if (usedToken) spendToken();
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (inFlight.current) return;
    if (!isValidPassword(password)) {
      toast.error(t("authFlow.validation.passwordInvalid"));
      return;
    }
    inFlight.current = true;
    setBusy(true);
    const usedToken = turnstileRequired && turnstileToken !== "";
    try {
      const payload = await completeRegistration({
        email: email.trim(),
        password,
        code: verificationEnabled ? code : "",
        turnstileToken: turnstileRequired ? turnstileToken : undefined,
      });
      await onResult(payload);
    } catch (error) {
      toast.error(errorMessage(error, t("authFlow.register.failed")));
    } finally {
      inFlight.current = false;
      setBusy(false);
      if (usedToken) spendToken();
    }
  };

  const sendDisabled = sending || cooldown.seconds > 0 || !email.trim() || (turnstileRequired && !turnstileToken);
  const submitDisabled = (verificationEnabled && code.length !== CODE_LENGTH) || (turnstileRequired && !turnstileToken) || !email.trim();

  return (
    <form onSubmit={submit} className="grid gap-4" aria-label={t("authFlow.register.formLabel")}>
      <Field required>
        <FieldLabel>{t("authFlow.register.email")}</FieldLabel>
        <Input name="email" type="email" autoComplete="email" value={email} onChange={(e) => changeEmail(e.target.value)} />
      </Field>
      <Field required>
        <FieldLabel>{t("authFlow.login.password")}</FieldLabel>
        <Input
          name="new-password"
          type="password"
          autoComplete="new-password"
          minLength={8}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <FieldHint>{t("authFlow.register.passwordHint")}</FieldHint>
      </Field>
      {verificationEnabled ? (
        <Field required>
          <FieldLabel>{t("authFlow.register.code")}</FieldLabel>
          <div className="flex gap-2">
            <Input
              name="code"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={CODE_LENGTH}
              value={code}
              onChange={(e) => setCode(digitsOnly(e.target.value))}
              className="tracking-widest"
            />
            <Button type="button" variant="outline" onClick={sendCode} disabled={sendDisabled} loading={sending}>
              {cooldown.seconds > 0
                ? t("authFlow.verify.resendIn", { seconds: cooldown.seconds })
                : sentOnce
                  ? t("authFlow.verify.resend")
                  : t("authFlow.verify.send")}
            </Button>
          </div>
          {debugCode ? <p className="text-[13px] text-muted-foreground">{t("authFlow.verify.devCode", { code: debugCode })}</p> : null}
        </Field>
      ) : null}
      {turnstileRequired ? <Turnstile siteKey={turnstileSiteKey} onToken={setTurnstileToken} resetKey={widgetKey} /> : null}
      <Button type="submit" loading={busy} disabled={submitDisabled}>
        {busy ? t("authFlow.register.submitting") : t("authFlow.register.submit")}
      </Button>
    </form>
  );
}

/** Desktop only: registration needs a widget the wrapper cannot show, so hand off to the system browser. */
export function RegisterInBrowser() {
  const t = useT();
  return (
    <div className="grid gap-3 rounded-lg border bg-muted/40 p-4 text-sm">
      <p className="text-muted-foreground">{t("authFlow.register.browserOnly")}</p>
      <Button type="button" variant="outline" onClick={() => void openExternal(`${resolveApiBase()}/login`)}>
        <ExternalLink />
        {t("authFlow.register.openBrowser")}
      </Button>
    </div>
  );
}
