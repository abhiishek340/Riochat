"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { problemText } from "../lib/problem";
import { importOpenWebUi, resetPassword, type ImportReport } from "./api";
import { validatePassword } from "./rules";

export function ResetPasswordDialog({ user, onOpenChange }: { user: { id: number; name: string } | null; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const message = useErrorMessage();
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (user) {
      setPassword("");
      setError(null);
    }
  }, [user]);
  const submit = async () => {
    if (!user || busy) return;
    const found = validatePassword(password);
    if (found) {
      setError(problemText(t, found));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await resetPassword(user.id, password.trim());
      toast.success(t("adminUsers.toast.passwordReset"));
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={user !== null} onOpenChange={(open) => !busy && onOpenChange(open)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("adminUsers.security.resetPasswordTitle", { name: user?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("adminUsers.security.resetPasswordBody")}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <Field required invalid={error !== null}>
            <FieldLabel>{t("adminUsers.security.newPassword")}</FieldLabel>
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
            <FieldHint>{t("adminUsers.form.passwordHint")}</FieldHint>
            <FieldError>{error}</FieldError>
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("adminUsers.security.resetPassword")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const COUNTERS = ["scanned", "imported", "skippedExistingEmail", "skippedDuplicateSourceEmail", "skippedInvalidEmail", "skippedInvalidRow"] as const;

/** Two steps: a dry run shows the counts, a second submit imports for real. */
export function ImportDialog({ open, onOpenChange, onImported }: { open: boolean; onOpenChange: (open: boolean) => void; onImported: (report: ImportReport) => void }) {
  const t = useT();
  const message = useErrorMessage();
  const [dsn, setDsn] = useState("");
  const [multiplier, setMultiplier] = useState("1");
  const [preview, setPreview] = useState<ImportReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) {
      setDsn("");
      setMultiplier("1");
      setPreview(null);
      setError(null);
    }
  }, [open]);

  const factor = Number(multiplier);
  const validFactor = multiplier.trim() !== "" && Number.isFinite(factor) && factor >= 0;
  const edit = (setter: (v: string) => void) => (value: string) => {
    setter(value);
    setPreview(null); // editing either input discards the preview
  };

  const submit = async () => {
    if (busy || !dsn.trim() || !validFactor) return;
    setBusy(true);
    setError(null);
    try {
      const report = await importOpenWebUi({ dsn: dsn.trim(), creditMultiplier: factor, dryRun: preview === null });
      if (preview === null) setPreview(report);
      else {
        onImported(report);
        onOpenChange(false);
      }
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("adminUsers.import.title")}</DialogTitle>
          <DialogDescription>{t("adminUsers.import.body")}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <Field required>
            <FieldLabel>{t("adminUsers.import.dsn")}</FieldLabel>
            <Input type="password" value={dsn} onChange={(e) => edit(setDsn)(e.target.value)} autoComplete="off" spellCheck={false} placeholder="postgres://user:pass@host/db" />
          </Field>
          <Field required invalid={multiplier.trim() !== "" && !validFactor}>
            <FieldLabel>{t("adminUsers.import.multiplier")}</FieldLabel>
            <Input inputMode="decimal" value={multiplier} onChange={(e) => edit(setMultiplier)(e.target.value)} />
            <FieldHint>{t("adminUsers.import.multiplierHint")}</FieldHint>
          </Field>
          {preview ? (
            <dl className="grid grid-cols-[1fr_auto] gap-x-6 gap-y-1 rounded-lg border bg-muted/40 p-3 text-sm" aria-label={t("adminUsers.import.preview")}>
              {COUNTERS.map((key) => (
                <div key={key} className="contents">
                  <dt className="text-muted-foreground">{t(`adminUsers.import.counters.${key}`)}</dt>
                  <dd className="text-right font-medium tabular-nums">{preview[key]}</dd>
                </div>
              ))}
            </dl>
          ) : null}
          <FieldError>{error}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy} disabled={!dsn.trim() || !validFactor}>
              {preview ? t("adminUsers.import.run") : t("adminUsers.import.previewAction")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
