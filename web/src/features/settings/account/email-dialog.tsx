"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import type { AuthUser } from "@/lib/api/types";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { isCodeReady, type VerificationMethod } from "../lib/policy";
import {
  emailBootstrapComplete,
  emailBootstrapStart,
  emailChangeComplete,
  emailChangeStartCurrent,
  emailChangeStartNew,
  emailVerifyCurrentComplete,
  emailVerifyCurrentStart,
} from "./api";
import { SendCodeButton, VerificationField } from "./verification-field";
import { useCodeSender } from "./use-send-code";

export type EmailMode = "verify" | "set" | "change";

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/**
 * The three mail flows in one dialog: confirm the address on file, give an
 * account its first address, or move to a new one. With server-side
 * verification off, setting and changing need only the address.
 */
export function EmailDialog({
  mode,
  onOpenChange,
  user,
  currentMethods,
  verificationEnabled,
  onDone,
}: {
  /** `null` keeps the dialog closed. */
  mode: EmailMode | null;
  onOpenChange: (open: boolean) => void;
  user: AuthUser;
  /** Methods that can prove the current identity before a change. */
  currentMethods: VerificationMethod[];
  verificationEnabled: boolean;
  onDone: (user: AuthUser) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const open = mode !== null;
  const lastMode = useRef<EmailMode>("verify");
  if (mode) lastMode.current = mode;
  const active = mode ?? lastMode.current;

  const [step, setStep] = useState<"current" | "new">("new");
  const [email, setEmail] = useState("");
  const [method, setMethod] = useState<VerificationMethod>("none");
  const [currentCode, setCurrentCode] = useState("");
  const [newCode, setNewCode] = useState("");
  const [busy, setBusy] = useState(false);

  const currentMethod = currentMethods[0] ?? "none";
  const needsCurrent = active === "change" && currentMethod !== "none";
  const staged = active === "verify" || (verificationEnabled ? true : needsCurrent);
  const emailValid = EMAIL_RE.test(email.trim());

  const verifySender = useCodeSender(() => emailVerifyCurrentStart(), "settings.account.toasts.codeSent");
  const currentSender = useCodeSender(() => emailChangeStartCurrent(method === "none" ? undefined : method), "settings.account.toasts.currentCodeSent");
  const newSender = useCodeSender(
    () => (active === "set" ? emailBootstrapStart(email.trim()) : emailChangeStartNew(email.trim())),
    "settings.account.toasts.newCodeSent",
  );

  useEffect(() => {
    if (!open) return;
    setEmail("");
    setCurrentCode("");
    setNewCode("");
    setMethod(currentMethod);
    setStep(active === "change" && currentMethod !== "none" ? "current" : "new");
    verifySender.reset();
    currentSender.reset();
    newSender.reset();
    // Re-seed each time the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const finish = (updated: AuthUser, toastKey: "emailVerified" | "emailSet" | "emailChanged") => {
    toast.success(t(`settings.account.toasts.${toastKey}`));
    onDone(updated);
    onOpenChange(false);
  };

  const run = async (action: () => Promise<AuthUser>, failKey: "verifyEmailFailed" | "setEmailFailed" | "changeEmailFailed", okKey: "emailVerified" | "emailSet" | "emailChanged") => {
    setBusy(true);
    try {
      finish(await action(), okKey);
    } catch (e) {
      toast.error(t(`settings.account.toasts.${failKey}`), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    if (active === "verify") {
      if (newCode.length === 6) void run(() => emailVerifyCurrentComplete(newCode), "verifyEmailFailed", "emailVerified");
      return;
    }
    if (active === "change" && staged && step === "current") {
      if (isCodeReady(method, currentCode)) setStep("new");
      return;
    }
    if (!emailValid) return;
    const code = verificationEnabled ? newCode : "";
    if (active === "set") void run(() => emailBootstrapComplete(email.trim(), code), "setEmailFailed", "emailSet");
    else
      void run(
        () =>
          emailChangeComplete({
            email: email.trim(),
            currentVerificationMethod: needsCurrent ? method : "none",
            currentCode: needsCurrent ? currentCode : "",
            newCode: code,
          }),
        "changeEmailFailed",
        "emailChanged",
      );
  };

  const canSubmit =
    active === "verify"
      ? newCode.length === 6
      : active === "change" && staged && step === "current"
        ? isCodeReady(method, currentCode)
        : emailValid && (!verificationEnabled || newCode.length === 6);

  const title = active === "verify" ? t("settings.account.email.verifyTitle") : active === "set" ? t("settings.account.email.setTitle") : t("settings.account.email.changeTitle");
  const description =
    active === "verify"
      ? t("settings.account.email.verifyBody", { email: user.email })
      : step === "current" && active === "change"
        ? t("settings.account.email.currentBody")
        : active === "set"
          ? t("settings.account.email.setBody")
          : t("settings.account.email.changeBody");

  return (
    <Dialog open={open} onOpenChange={(next) => (busy ? undefined : onOpenChange(next))}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>

          {active === "verify" ? (
            <Field>
              <FieldLabel>{t("settings.account.verify.emailCode")}</FieldLabel>
              <div className="flex gap-2">
                <Input value={newCode} inputMode="numeric" autoComplete="one-time-code" className="font-mono tracking-widest" placeholder="000000" onChange={(e) => setNewCode(e.target.value.replace(/\D/g, "").slice(0, 6))} />
                <SendCodeButton sender={verifySender} sentOnce={verifySender.sent} />
              </div>
              {verifySender.debugCode ? <p className="font-mono text-xs text-muted-foreground">{t("settings.account.verify.debugCode", { code: verifySender.debugCode })}</p> : null}
            </Field>
          ) : null}

          {active !== "verify" && step === "current" && active === "change" ? (
            <VerificationField
              methods={currentMethods}
              method={method}
              onMethod={(m) => {
                setMethod(m);
                setCurrentCode("");
              }}
              code={currentCode}
              onCode={setCurrentCode}
              sender={currentSender}
              autoFocus
            />
          ) : null}

          {active !== "verify" && step === "new" ? (
            <>
              <Field invalid={email !== "" && !emailValid}>
                <FieldLabel>{t("settings.account.email.newAddress")}</FieldLabel>
                <Input type="email" autoComplete="email" value={email} maxLength={128} onChange={(e) => setEmail(e.target.value)} />
              </Field>
              {verificationEnabled ? (
                <Field>
                  <FieldLabel>{t("settings.account.verify.emailCode")}</FieldLabel>
                  <div className="flex gap-2">
                    <Input value={newCode} inputMode="numeric" autoComplete="one-time-code" className="font-mono tracking-widest" placeholder="000000" onChange={(e) => setNewCode(e.target.value.replace(/\D/g, "").slice(0, 6))} />
                    <SendCodeButton sender={newSender} sentOnce={newSender.sent} disabled={!emailValid} />
                  </div>
                  {newSender.debugCode ? <p className="font-mono text-xs text-muted-foreground">{t("settings.account.verify.debugCode", { code: newSender.debugCode })}</p> : null}
                </Field>
              ) : null}
            </>
          ) : null}

          <DialogFooter>
            {active === "change" && staged && step === "new" && needsCurrent ? (
              <Button type="button" variant="outline" disabled={busy} onClick={() => setStep("current")}>
                {t("common.actions.back")}
              </Button>
            ) : (
              <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
                {t("common.actions.cancel")}
              </Button>
            )}
            <Button type="submit" loading={busy} disabled={!canSubmit}>
              {active === "change" && step === "current" ? t("common.actions.continue") : t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
