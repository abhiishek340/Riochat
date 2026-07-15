"use client";

import { RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useI18n, useT } from "@/lib/i18n/provider";
import { CopyButton } from "../parts";
import { sanitizeCode } from "../lib/policy";
import { mfaDisable, mfaRegenerate, mfaSetupCancel, mfaSetupConfirm, type TotpSetup } from "./api";
import { QrCode } from "./qr-code";

export type TwoFactorMode = "setup" | "manage";

const MIN_CODE = 6;

/** Whole seconds until an ISO time; negative once passed. */
export function secondsUntil(iso: string, now: number): number {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? 0 : Math.ceil((t - now) / 1000);
}

/**
 * Authenticator setup, management and recovery-code display. Recovery codes
 * are only ever held in this component's state and vanish on close.
 */
export function TwoFactorDialog({
  mode,
  setup,
  onOpenChange,
  onStartAgain,
  onChanged,
}: {
  /** `null` keeps the dialog closed. */
  mode: TwoFactorMode | null;
  /** The pending TOTP setup when `mode` is "setup". */
  setup: TotpSetup | null;
  onOpenChange: (open: boolean) => void;
  /** Regenerates an expired setup; resolves to the new one. */
  onStartAgain: () => Promise<TotpSetup | null>;
  /** The two-factor status changed (enabled/disabled): refresh it. */
  onChanged: () => void;
}) {
  const t = useT();
  const { locale } = useI18n();
  const errorMessage = useErrorMessage();
  const open = mode !== null;
  const [codes, setCodes] = useState<string[]>([]);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState<"" | "enable" | "regenerate" | "disable" | "restart">("");
  const [now, setNow] = useState(() => Date.now());
  const input = useRef<HTMLInputElement>(null);
  // Keep showing the last content while the close animation runs.
  const snapshot = useRef<{ mode: TwoFactorMode; setup: TotpSetup | null }>({ mode: "manage", setup: null });
  if (mode) snapshot.current = { mode, setup };
  const view = mode ? { mode, setup } : snapshot.current;
  const recovery = codes.length > 0;

  useEffect(() => {
    if (open) {
      setCodes([]);
      setCode("");
    }
  }, [open]);

  useEffect(() => {
    if (!open || view.mode !== "setup") return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [open, view.mode]);

  const remaining = view.setup ? secondsUntil(view.setup.expiresAt, now) : 0;
  const expired = view.mode === "setup" && view.setup !== null && remaining <= 0;
  const untilText = view.setup
    ? new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }).format(new Date(view.setup.expiresAt))
    : "";

  const close = async () => {
    if (busy) return;
    if (view.mode === "setup" && !recovery) {
      try {
        await mfaSetupCancel();
      } catch {
        /* closing never blocks */
      }
    }
    setCodes([]);
    setCode("");
    onOpenChange(false);
  };

  const needCode = (): boolean => {
    if (code.length < MIN_CODE) {
      toast.error(t("settings.account.twoFactor.codeRequired"));
      input.current?.focus();
      return false;
    }
    return true;
  };

  const enable = async () => {
    if (view.mode !== "setup" || expired) return;
    if (code.length < MIN_CODE) {
      toast.error(t("settings.account.twoFactor.codeRequired"));
      input.current?.focus();
      return;
    }
    setBusy("enable");
    try {
      const recoveryCodes = await mfaSetupConfirm(code);
      setCodes(recoveryCodes);
      setCode("");
      onChanged();
      toast.success(t("settings.account.toasts.twoFactorEnabled"));
    } catch (e) {
      toast.error(t("settings.account.toasts.enableTwoFactorFailed"), { description: errorMessage(e) });
    } finally {
      setBusy("");
    }
  };

  const regenerate = async () => {
    if (!needCode()) return;
    setBusy("regenerate");
    try {
      setCodes(await mfaRegenerate(code));
      setCode("");
      toast.success(t("settings.account.toasts.recoveryRegenerated"));
    } catch (e) {
      toast.error(t("settings.account.toasts.regenerateFailed"), { description: errorMessage(e) });
    } finally {
      setBusy("");
    }
  };

  const disable = async () => {
    if (!needCode()) return;
    setBusy("disable");
    try {
      await mfaDisable(code);
      onChanged();
      setCodes([]);
      setCode("");
      onOpenChange(false);
      toast.success(t("settings.account.toasts.twoFactorDisabled"));
    } catch (e) {
      toast.error(t("settings.account.toasts.disableTwoFactorFailed"), { description: errorMessage(e) });
    } finally {
      setBusy("");
    }
  };

  const restart = async () => {
    setBusy("restart");
    try {
      await onStartAgain();
      setCode("");
    } finally {
      setBusy("");
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? undefined : void close())}>
      <DialogContent className="max-w-md">
        {recovery ? (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.account.twoFactor.recoveryTitle")}</DialogTitle>
              <DialogDescription>{t("settings.account.twoFactor.recoveryBody")}</DialogDescription>
            </DialogHeader>
            <ul className="grid grid-cols-2 gap-2 rounded-lg bg-muted p-3 font-mono text-sm">
              {codes.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
            <DialogFooter className="sm:justify-between">
              <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
                <CopyButton value={codes.join("\n")} variant="outline" size="icon" label={t("settings.account.twoFactor.copyAll")} />
                {t("settings.account.twoFactor.copyAll")}
              </span>
              <Button onClick={() => void close()}>{t("settings.account.twoFactor.done")}</Button>
            </DialogFooter>
          </div>
        ) : view.mode === "setup" ? (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.account.twoFactor.setupTitle")}</DialogTitle>
              <DialogDescription>{t("settings.account.twoFactor.setupBody")}</DialogDescription>
            </DialogHeader>
            <Tabs defaultValue="qr">
              <TabsList className="w-full">
                <TabsTrigger value="qr" className="flex-1">
                  {t("settings.account.twoFactor.tabQr")}
                </TabsTrigger>
                <TabsTrigger value="manual" className="flex-1">
                  {t("settings.account.twoFactor.tabManual")}
                </TabsTrigger>
              </TabsList>
              <TabsContent value="qr">
                <QrCode value={view.setup?.otpauthURL ?? ""} label={t("settings.account.twoFactor.qrLabel")} />
              </TabsContent>
              <TabsContent value="manual" className="grid gap-3">
                <ReadOnlyCopy label={t("settings.account.twoFactor.secret")} value={view.setup?.secret ?? ""} />
                <ReadOnlyCopy label={t("settings.account.twoFactor.uri")} value={view.setup?.otpauthURL ?? ""} />
              </TabsContent>
            </Tabs>
            <p className="text-[13px] text-muted-foreground">
              {expired ? t("settings.account.twoFactor.expired") : t("settings.account.twoFactor.validUntil", { time: untilText })}
            </p>
            <Field>
              <FieldLabel>{t("settings.account.twoFactor.enterCode")}</FieldLabel>
              <Input
                ref={input}
                value={code}
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                className="font-mono tracking-[0.4em]"
                placeholder="000000"
                onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    void enable();
                  }
                }}
              />
            </Field>
            <DialogFooter>
              <Button variant="outline" disabled={busy !== ""} onClick={() => void close()}>
                {t("common.actions.cancel")}
              </Button>
              {expired ? (
                <Button loading={busy === "restart"} onClick={() => void restart()}>
                  <RefreshCw />
                  {t("settings.account.twoFactor.regenerate")}
                </Button>
              ) : null}
              <Button loading={busy === "enable"} disabled={expired || busy !== ""} onClick={() => void enable()}>
                {t("settings.account.twoFactor.enable")}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.account.twoFactor.manageTitle")}</DialogTitle>
              <DialogDescription>{t("settings.account.twoFactor.manageBody")}</DialogDescription>
            </DialogHeader>
            <Field>
              <FieldLabel>{t("settings.account.twoFactor.manageCode")}</FieldLabel>
              <Input
                ref={input}
                value={code}
                autoComplete="one-time-code"
                spellCheck={false}
                className="font-mono"
                onChange={(e) => setCode(sanitizeCode("two_factor", e.target.value))}
              />
            </Field>
            <DialogFooter className="sm:justify-between">
              <Button variant="destructive" loading={busy === "disable"} disabled={busy !== ""} onClick={() => void disable()}>
                {t("settings.account.twoFactor.disable")}
              </Button>
              <span className="flex gap-2">
                <Button variant="outline" disabled={busy !== ""} onClick={() => void close()}>
                  {t("common.actions.close")}
                </Button>
                <Button variant="outline" loading={busy === "regenerate"} disabled={busy !== ""} onClick={() => void regenerate()}>
                  {t("settings.account.twoFactor.regenerateRecovery")}
                </Button>
              </span>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ReadOnlyCopy({ label, value }: { label: string; value: string }) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <div className="flex items-center gap-1.5">
        <Input readOnly value={value} className="font-mono text-[13px]" onFocus={(e) => e.currentTarget.select()} />
        <CopyButton value={value} variant="outline" size="icon" label={label} />
      </div>
    </Field>
  );
}
