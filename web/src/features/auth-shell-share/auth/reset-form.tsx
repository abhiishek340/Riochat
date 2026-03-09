"use client";

import { useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { CODE_LENGTH, digitsOnly, isValidPassword } from "../lib/password-policy";
import { completePasswordReset, startPasswordReset } from "./api";
import { RESEND_SECONDS, useCooldown } from "./use-cooldown";

export function ResetForm({ initialEmail, onDone }: { initialEmail: string; onDone: (email: string) => void }) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [email, setEmail] = useState(initialEmail);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [sentOnce, setSentOnce] = useState(false);
  const [sending, setSending] = useState(false);
  const [busy, setBusy] = useState(false);
  const cooldown = useCooldown();
  const inFlight = useRef(false);

  const changeEmail = (value: string) => {
    setEmail(value);
    setSentOnce(false);
    cooldown.reset();
  };

  const sendCode = async () => {
    if (sending || cooldown.seconds > 0 || !email.trim()) return;
    setSending(true);
    try {
      const result = await startPasswordReset(email.trim());
      if (result.sent) {
        setSentOnce(true);
        cooldown.start(RESEND_SECONDS);
      }
    } catch (error) {
      toast.error(errorMessage(error, t("authFlow.reset.sendFailed")));
    } finally {
      setSending(false);
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (inFlight.current) return;
    if (!isValidPassword(password)) {
      toast.error(t("authFlow.validation.passwordInvalid"));
      return;
    }
    if (code.length !== CODE_LENGTH) {
      toast.error(t("authFlow.verify.codeRequired"));
      return;
    }
    inFlight.current = true;
    setBusy(true);
    try {
      await completePasswordReset({ email: email.trim(), code, newPassword: password });
      toast.success(t("authFlow.reset.success"));
      onDone(email.trim());
    } catch (error) {
      toast.error(errorMessage(error, t("authFlow.reset.failed")));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="grid gap-4" aria-label={t("authFlow.reset.formLabel")}>
      <Field required>
        <FieldLabel>{t("authFlow.register.email")}</FieldLabel>
        <Input name="email" type="email" autoComplete="email" value={email} onChange={(e) => changeEmail(e.target.value)} />
      </Field>
      <Field required>
        <FieldLabel>{t("authFlow.reset.newPassword")}</FieldLabel>
        <Input
          name="new-password"
          type="password"
          autoComplete="new-password"
          placeholder={t("authFlow.register.passwordHint")}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
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
          <Button type="button" variant="outline" onClick={sendCode} disabled={sending || cooldown.seconds > 0 || !email.trim()} loading={sending}>
            {cooldown.seconds > 0
              ? t("authFlow.verify.resendIn", { seconds: cooldown.seconds })
              : sentOnce
                ? t("authFlow.verify.resend")
                : t("authFlow.verify.send")}
          </Button>
        </div>
      </Field>
      <Button type="submit" loading={busy} disabled={code.length !== CODE_LENGTH}>
        {busy ? t("authFlow.reset.submitting") : t("authFlow.reset.submit")}
      </Button>
    </form>
  );
}
