"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import type { AuthPayload } from "@/lib/api/types";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { CODE_LENGTH, digitsOnly, sanitizeSecondFactorCode } from "../lib/password-policy";
import type { TwoFactorMethod } from "../lib/handoff-storage";
import { startEmailSecondFactor, verifySecondFactor } from "./api";
import { isChallengeExpired } from "./sign-in";
import { RESEND_SECONDS, useCooldown } from "./use-cooldown";

/** The code the active method accepts: six digits by email, longer alphanumerics for an app or recovery code. */
export function sanitizeCode(method: TwoFactorMethod, raw: string): string {
  return method === "email" ? digitsOnly(raw, CODE_LENGTH) : sanitizeSecondFactorCode(raw);
}

export function SecondFactorForm({
  token,
  methods,
  passwordLogin,
  onResult,
  onCancel,
  onExpired,
}: {
  token: string;
  methods: TwoFactorMethod[];
  passwordLogin: boolean;
  onResult: (payload: AuthPayload) => Promise<void> | void;
  onCancel: () => void;
  onExpired: () => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [method, setMethod] = useState<TwoFactorMethod>(methods[0] ?? "two_factor");
  const [code, setCode] = useState("");
  const [debugCode, setDebugCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [sending, setSending] = useState(false);
  const cooldown = useCooldown();
  const inFlight = useRef(false);

  // A new challenge starts from a clean slate.
  useEffect(() => {
    setMethod(methods[0] ?? "two_factor");
    setCode("");
    setDebugCode("");
    cooldown.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset only when the challenge itself changes
  }, [token]);

  const other = methods.find((m) => m !== method);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (inFlight.current) return;
    if (code.length < CODE_LENGTH) {
      toast.error(t("authFlow.verify.codeRequired"));
      return;
    }
    inFlight.current = true;
    setBusy(true);
    try {
      const payload = await verifySecondFactor({ challengeToken: token, verificationMethod: method, code });
      await onResult(payload);
    } catch (error) {
      if (isChallengeExpired(error)) {
        toast.error(t("authFlow.login.challengeExpired"));
        onExpired();
      } else {
        toast.error(errorMessage(error, t("authFlow.verify.failed")));
      }
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };

  const sendEmail = async () => {
    if (sending || cooldown.seconds > 0) return;
    setSending(true);
    try {
      const result = await startEmailSecondFactor(token);
      if (result.sent) {
        cooldown.start(RESEND_SECONDS);
        setDebugCode(result.debugCode);
      }
    } catch (error) {
      if (isChallengeExpired(error)) {
        toast.error(t("authFlow.login.challengeExpired"));
        onExpired();
      } else {
        toast.error(errorMessage(error, t("authFlow.verify.sendFailed")));
      }
    } finally {
      setSending(false);
    }
  };

  const switchTo = (next: TwoFactorMethod) => {
    setMethod(next);
    setCode("");
    setDebugCode("");
  };

  const email = method === "email";
  return (
    <form onSubmit={submit} className="grid gap-4" aria-label={t("authFlow.verify.formLabel")}>
      <Field required>
        <FieldLabel>{email ? t("authFlow.verify.emailTitle") : t("authFlow.verify.appTitle")}</FieldLabel>
        <div className="flex gap-2">
          <Input
            name="code"
            autoFocus
            autoComplete="one-time-code"
            inputMode={email ? "numeric" : "text"}
            spellCheck={false}
            maxLength={email ? CODE_LENGTH : 32}
            value={code}
            onChange={(e) => setCode(sanitizeCode(method, e.target.value))}
            className="tracking-widest"
          />
          {email ? (
            <Button type="button" variant="outline" onClick={sendEmail} disabled={sending || cooldown.seconds > 0} loading={sending}>
              {cooldown.seconds > 0 ? t("authFlow.verify.resendIn", { seconds: cooldown.seconds }) : t("authFlow.verify.send")}
            </Button>
          ) : null}
        </div>
        <FieldHint>{email ? t("authFlow.verify.emailHint") : t("authFlow.verify.appHint")}</FieldHint>
        {debugCode ? <p className="text-[13px] text-muted-foreground">{t("authFlow.verify.devCode", { code: debugCode })}</p> : null}
      </Field>

      <Button type="submit" loading={busy}>
        {busy ? t("authFlow.verify.verifying") : t("authFlow.verify.submit")}
      </Button>

      <div className="grid gap-1">
        {other ? (
          <Button type="button" variant="ghost" size="sm" onClick={() => switchTo(other)}>
            {other === "email" ? t("authFlow.verify.useEmail") : t("authFlow.verify.useApp")}
          </Button>
        ) : null}
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          <ArrowLeft />
          {passwordLogin ? t("authFlow.verify.backToPassword") : t("authFlow.verify.backToMethods")}
        </Button>
      </div>
    </form>
  );
}
