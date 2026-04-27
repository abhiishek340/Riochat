"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { Check, Copy, Monitor, Moon, RefreshCw, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, NativeSelect } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { toast } from "@/components/ui/toast-store";
import { LOCALES, LOCALE_LABELS } from "@/lib/i18n/config";
import { useT } from "@/lib/i18n/provider";
import type { ThemeMode } from "@/lib/theme/config";
import { cn } from "@/lib/utils/cn";
import { copyText } from "../lib/clipboard";
import { QrCode } from "./qr-code";
import { PRESET_CHOICES, timezoneChoices } from "./model";
import type { OnboardingFlow } from "./use-onboarding-flow";

export function StepFrame({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  const heading = useRef<HTMLHeadingElement>(null);
  // The button that moved us here is gone with the previous step: put the reader at the new heading.
  useEffect(() => heading.current?.focus({ preventScroll: true }), []);
  return (
    <section className="grid gap-5" aria-labelledby="onboarding-step-title">
      <header className="grid gap-1.5">
        <h2 ref={heading} id="onboarding-step-title" tabIndex={-1} className="text-2xl font-semibold tracking-tight outline-none">
          {title}
        </h2>
        {description ? <p className="text-sm leading-relaxed text-muted-foreground">{description}</p> : null}
      </header>
      {children}
    </section>
  );
}

function Actions({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center justify-between gap-2 pt-2">{children}</div>;
}

export function WelcomeStep({ flow, admin }: { flow: OnboardingFlow; admin: boolean }) {
  const t = useT();
  return (
    <StepFrame title={admin ? t("onboarding.welcome.adminTitle") : t("onboarding.welcome.userTitle")} description={admin ? t("onboarding.welcome.adminBody") : t("onboarding.welcome.userBody")}>
      <Field>
        <FieldLabel>{t("onboarding.welcome.language")}</FieldLabel>
        <NativeSelect value={flow.locale} onChange={(e) => flow.chooseLocale(e.target.value as (typeof LOCALES)[number])}>
          {LOCALES.map((l) => (
            <option key={l} value={l} lang={l}>
              {LOCALE_LABELS[l]}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <Actions>
        <span />
        <Button onClick={() => void flow.start()} loading={flow.busy}>
          {t("onboarding.welcome.start")}
        </Button>
      </Actions>
    </StepFrame>
  );
}

export function AccountStep({ flow, usernameEditable }: { flow: OnboardingFlow; usernameEditable: boolean }) {
  const t = useT();
  return (
    <StepFrame
      title={flow.bootstrap ? t("onboarding.account.bootstrapTitle") : t("onboarding.account.title")}
      description={flow.bootstrap ? t("onboarding.account.bootstrapBody") : t("onboarding.account.body")}
    >
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void flow.saveAccount();
        }}
      >
        <Field>
          <FieldLabel>{t("onboarding.account.username")}</FieldLabel>
          <Input
            name="username"
            autoComplete="username"
            maxLength={16}
            readOnly={!usernameEditable}
            value={flow.username}
            onChange={(e) => flow.setUsername(e.target.value)}
          />
          <FieldHint>{usernameEditable ? t("onboarding.account.usernameHint") : t("onboarding.account.usernameLocked")}</FieldHint>
        </Field>
        {flow.bootstrap ? (
          <Field>
            <FieldLabel>{t("onboarding.account.newPassword")}</FieldLabel>
            <Input
              name="new-password"
              type="password"
              autoComplete="new-password"
              value={flow.password}
              onChange={(e) => flow.setPassword(e.target.value)}
            />
            <FieldHint>{t("onboarding.account.passwordHint")}</FieldHint>
          </Field>
        ) : (
          <Field>
            <FieldLabel>{t("onboarding.account.displayName")}</FieldLabel>
            <Input name="display-name" maxLength={16} value={flow.displayName} onChange={(e) => flow.setDisplayName(e.target.value)} />
            <FieldHint>{t("onboarding.account.displayNameHint")}</FieldHint>
          </Field>
        )}
        <Actions>
          <Button type="button" variant="ghost" onClick={flow.back} disabled={flow.busy}>
            {t("common.actions.back")}
          </Button>
          <Button type="submit" loading={flow.busy}>
            {t("common.actions.continue")}
          </Button>
        </Actions>
      </form>
    </StepFrame>
  );
}

export function TwoFactorStep({ flow, alreadyEnabled, unavailable }: { flow: OnboardingFlow; alreadyEnabled: boolean; unavailable: boolean }) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  const copySecret = async () => {
    if (!flow.setup) return;
    const ok = await copyText(flow.setup.secret);
    if (ok) {
      setCopied(true);
      toast.success(t("onboarding.twoFactor.copied"));
      setTimeout(() => setCopied(false), 1800);
    } else {
      toast.error(t("onboarding.twoFactor.copyFailed"));
    }
  };

  let body: ReactNode;
  if (unavailable) {
    body = <p className="rounded-lg border bg-muted/40 p-4 text-sm text-muted-foreground">{t("onboarding.twoFactor.unavailable")}</p>;
  } else if (alreadyEnabled) {
    body = (
      <p className="flex items-center gap-2 rounded-lg border bg-success/10 p-4 text-sm text-success">
        <Check className="size-4" />
        {t("onboarding.twoFactor.alreadyEnabled")}
      </p>
    );
  } else if (flow.setupState === "starting" || (flow.setupState === "ready" && !flow.setup)) {
    body = (
      <div role="status" className="flex items-center gap-3 rounded-lg border p-4 text-sm text-muted-foreground">
        <Spinner />
        {t("onboarding.twoFactor.preparing")}
      </div>
    );
  } else if (flow.setupState === "failed" || !flow.setup) {
    body = (
      <div className="grid justify-items-start gap-3 rounded-lg border p-4">
        <p className="text-sm text-muted-foreground">{t("onboarding.twoFactor.startFailed")}</p>
        <Button variant="outline" size="sm" onClick={flow.retrySetup}>
          <RefreshCw />
          {t("common.actions.retry")}
        </Button>
      </div>
    );
  } else {
    body = (
      <div className="grid gap-4 sm:grid-cols-[auto_1fr] sm:items-start">
        <QrCode text={flow.setup.otpauthURL} className="size-40 rounded-lg border bg-white p-1" />
        <div className="grid gap-3">
          <Field>
            <FieldLabel>{t("onboarding.twoFactor.manualKey")}</FieldLabel>
            <div className="flex gap-2">
              <Input readOnly value={flow.setup.secret} className="font-mono text-[13px]" aria-label={t("onboarding.twoFactor.manualKey")} />
              <Button type="button" variant="outline" size="icon" onClick={() => void copySecret()} aria-label={t("common.actions.copy")}>
                {copied ? <Check /> : <Copy />}
              </Button>
            </div>
          </Field>
          <Field>
            <FieldLabel>{t("onboarding.twoFactor.code")}</FieldLabel>
            <Input
              name="otp"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={flow.code}
              onChange={(e) => flow.setCode(e.target.value)}
              className="tracking-widest"
            />
          </Field>
        </div>
      </div>
    );
  }

  const canEnable = !unavailable && !alreadyEnabled && flow.setupState === "ready" && flow.setup !== null;
  return (
    <StepFrame title={t("onboarding.twoFactor.title")} description={t("onboarding.twoFactor.body")}>
      {body}
      <Actions>
        <span />
        <div className="flex gap-2">
          {unavailable || alreadyEnabled ? (
            <Button onClick={() => void flow.skipTwoFactor()} loading={flow.busy}>
              {t("common.actions.continue")}
            </Button>
          ) : (
            <>
              <Button variant="ghost" onClick={() => void flow.skipTwoFactor()} disabled={flow.busy}>
                {t("onboarding.twoFactor.skip")}
              </Button>
              <Button onClick={() => void flow.enableTwoFactor()} loading={flow.busy} disabled={!canEnable}>
                {t("onboarding.twoFactor.enable")}
              </Button>
            </>
          )}
        </div>
      </Actions>
    </StepFrame>
  );
}
