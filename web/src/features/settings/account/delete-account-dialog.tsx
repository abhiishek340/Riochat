"use client";

import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { toast } from "@/components/ui/toast-store";
import { endSession } from "@/lib/api/session-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { isCodeReady, type VerificationMethod } from "../lib/policy";
import { deleteAccount, deleteStart } from "./api";
import { useCodeSender } from "./use-send-code";
import { VerificationField } from "./verification-field";

/** Two steps: an explicit warning, then proof of identity. Success signs out. */
export function DeleteAccountDialog({
  open,
  onOpenChange,
  methods,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  methods: VerificationMethod[];
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const available = methods.some((m) => m !== "none");
  const [step, setStep] = useState<"warn" | "verify">("warn");
  const [method, setMethod] = useState<VerificationMethod>(methods[0] ?? "none");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const sender = useCodeSender(() => deleteStart(method), "settings.account.toasts.deleteCodeSent");

  useEffect(() => {
    if (open) {
      setStep("warn");
      setMethod(methods[0] ?? "none");
      setCode("");
      sender.reset();
    }
    // Reset on open only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy || !isCodeReady(method, code)) return;
    setBusy(true);
    try {
      await deleteAccount(method, code);
      onOpenChange(false);
      await endSession();
    } catch (err) {
      toast.error(t("settings.account.toasts.deleteFailed"), { description: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (busy ? undefined : onOpenChange(next))}>
      <DialogContent>
        {step === "warn" ? (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.account.delete.title")}</DialogTitle>
              <DialogDescription>{t("settings.account.delete.body")}</DialogDescription>
            </DialogHeader>
            {available ? null : (
              <p role="note" className="rounded-lg bg-warning/15 p-3 text-[13px] text-warning-foreground dark:text-warning">
                {t("settings.account.delete.noMethod")}
              </p>
            )}
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                {t("common.actions.cancel")}
              </Button>
              <Button variant="destructive" disabled={!available} onClick={() => setStep("verify")}>
                {t("common.actions.continue")}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={(e) => void submit(e)} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.account.verify.title")}</DialogTitle>
              <DialogDescription>{t("settings.account.delete.verifyBody")}</DialogDescription>
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
              <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
                {t("common.actions.cancel")}
              </Button>
              <Button type="submit" variant="destructive" loading={busy} disabled={!isCodeReady(method, code)}>
                {t("settings.account.delete.confirm")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
