"use client";

import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import type { AuthUser } from "@/lib/api/types";
import { endSession } from "@/lib/api/session-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { isCodeReady, isValidPassword, PASSWORD_MAX, type VerificationMethod } from "../lib/policy";
import { hasPassword } from "../lib/user";
import { passwordChangeComplete, passwordChangeStart } from "./api";
import { useCodeSender } from "./use-send-code";
import { VerificationField } from "./verification-field";

/**
 * Set or change the password. Forced resets cannot be dismissed. Changing
 * ends every session, so success signs the user out.
 */
export function PasswordDialog({
  open,
  onOpenChange,
  user,
  methods,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  user: AuthUser;
  methods: VerificationMethod[];
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const forced = user.mustResetPassword === true;
  const hasPw = hasPassword(user);
  const needsCurrent = hasPw && !forced;
  const [step, setStep] = useState<"form" | "verify">("form");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [method, setMethod] = useState<VerificationMethod>(methods[0] ?? "none");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const sender = useCodeSender(() => passwordChangeStart(method), "settings.account.toasts.codeSent");

  useEffect(() => {
    if (!open) {
      setStep("form");
      setCurrent("");
      setNext("");
      setCode("");
      sender.reset();
    } else {
      setMethod(methods[0] ?? "none");
    }
    // Reset on open/close only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const valid = isValidPassword(next) && (!needsCurrent || current !== "");

  const submit = async (m: VerificationMethod, c: string) => {
    setBusy(true);
    try {
      await passwordChangeComplete({
        currentPassword: needsCurrent && current ? current : undefined,
        newPassword: next.trim(),
        verificationMethod: m,
        code: c,
      });
      toast.success(t("settings.account.toasts.passwordChanged"), { description: t("settings.account.toasts.passwordChangedBody") });
      onOpenChange(false);
      await endSession();
    } catch (e) {
      toast.error(t("settings.account.toasts.passwordFailed"), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  const onForm = (e: FormEvent) => {
    e.preventDefault();
    if (!valid || busy) return;
    if (forced) void submit("none", "");
    else if ((methods[0] ?? "none") === "none") void submit("none", "");
    else setStep("verify");
  };

  const onVerify = (e: FormEvent) => {
    e.preventDefault();
    if (busy || !isCodeReady(method, code)) return;
    void submit(method, code);
  };

  const title = forced ? t("settings.account.password.titleRequired") : hasPw ? t("settings.account.password.titleChange") : t("settings.account.password.titleSet");
  const description = forced ? t("settings.account.password.descRequired") : hasPw ? t("settings.account.password.descChange") : t("settings.account.password.descSet");

  return (
    <Dialog open={open} onOpenChange={(next) => (busy || (forced && !next) ? undefined : onOpenChange(next))}>
      <DialogContent hideClose={forced}>
        {step === "form" ? (
          <form onSubmit={onForm} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{title}</DialogTitle>
              <DialogDescription>{description}</DialogDescription>
            </DialogHeader>
            {needsCurrent ? (
              <Field>
                <FieldLabel>{t("settings.account.password.current")}</FieldLabel>
                <Input type="password" autoComplete="current-password" value={current} maxLength={PASSWORD_MAX} onChange={(e) => setCurrent(e.target.value)} />
              </Field>
            ) : null}
            <Field invalid={next !== "" && !isValidPassword(next)}>
              <FieldLabel>{t("settings.account.password.new")}</FieldLabel>
              <Input type="password" autoComplete="new-password" value={next} maxLength={PASSWORD_MAX} onChange={(e) => setNext(e.target.value)} />
              <FieldHint>{t("settings.account.password.rule")}</FieldHint>
            </Field>
            <DialogFooter>
              {forced ? null : (
                <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
                  {t("common.actions.cancel")}
                </Button>
              )}
              <Button type="submit" loading={busy && step === "form"} disabled={!valid}>
                {forced || (methods[0] ?? "none") === "none" ? t("common.actions.save") : t("common.actions.continue")}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <form onSubmit={onVerify} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.account.verify.title")}</DialogTitle>
              <DialogDescription>{t("settings.account.verify.description")}</DialogDescription>
            </DialogHeader>
            <VerificationField
              methods={methods}
              method={method}
              onMethod={(m) => {
                setMethod(m);
                setCode("");
              }}
              code={code}
              onCode={setCode}
              sender={sender}
              autoFocus
            />
            <DialogFooter>
              <Button type="button" variant="outline" disabled={busy} onClick={() => setStep("form")}>
                {t("common.actions.back")}
              </Button>
              <Button type="submit" loading={busy} disabled={!isCodeReady(method, code)}>
                {t("common.actions.confirm")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
