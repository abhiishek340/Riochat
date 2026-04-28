"use client";

import { useEffect, useState } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { BrandMark } from "@/components/brand/logo";
import { useT, type MessageKey } from "@/lib/i18n/provider";
import { useSession } from "@/lib/session/provider";
import type { AuthUser } from "@/lib/api/types";
import { cn } from "@/lib/utils/cn";
import { isAdminUser, STEPS } from "./model";
import { AccountStep, PersonalizeStep, ReadyStep, ThemeStep, TwoFactorStep, WelcomeStep } from "./steps";
import { useOnboardingFlow } from "./use-onboarding-flow";

/**
 * Full-screen first-run wizard. It appears whenever the account still has
 * `initialSecurityRequired`, and stays while freshly issued recovery codes are
 * on screen. Signing out (no user) discards all wizard state.
 */
export function OnboardingOverlay() {
  const { user } = useSession();
  const [holding, setHolding] = useState(false);
  if (!user || (!user.initialSecurityRequired && !holding)) return null;
  return <Wizard key={user.publicID} user={user} onHold={setHolding} />;
}

function Wizard({ user, onHold }: { user: AuthUser; onHold: (holding: boolean) => void }) {
  const t = useT();
  const flow = useOnboardingFlow(user, onHold);
  const admin = isAdminUser(user);
  const name = STEPS[flow.step - 1] ?? "welcome";

  return (
    <DialogPrimitive.Root open modal>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Content
          aria-describedby={undefined}
          onEscapeKeyDown={(e) => e.preventDefault()}
          onPointerDownOutside={(e) => e.preventDefault()}
          onInteractOutside={(e) => e.preventDefault()}
          className="fixed inset-0 z-[100] flex bg-background outline-none"
        >
          <DialogPrimitive.Title className="sr-only">{t("onboarding.dialogTitle")}</DialogPrimitive.Title>
          <SidePanel admin={admin} step={flow.step} />
          <div className="flex min-w-0 flex-1 flex-col overflow-y-auto">
            <div className="mx-auto flex w-full max-w-lg flex-1 flex-col justify-center gap-8 px-5 py-8 sm:px-8">
              <StepDots step={flow.step} total={flow.last} />
              <div key={name} className="animate-[rio-pop-in_220ms_cubic-bezier(0.22,1,0.36,1)] motion-reduce:animate-none">
                {name === "welcome" ? <WelcomeStep flow={flow} admin={admin} /> : null}
                {name === "account" ? <AccountStep flow={flow} usernameEditable={user.initialUsernameRequired} /> : null}
                {name === "twoFactor" ? (
                  <TwoFactorStep flow={flow} unavailable={!user.twoFactorAvailable} alreadyEnabled={user.twoFactorEnabled} />
                ) : null}
                {name === "theme" ? <ThemeStep flow={flow} /> : null}
                {name === "personalize" ? <PersonalizeStep flow={flow} profileTimezone={user.timezone} /> : null}
                {name === "ready" ? <ReadyStep flow={flow} admin={admin} /> : null}
              </div>
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

function StepDots({ step, total }: { step: number; total: number }) {
  const t = useT();
  return (
    <ol className="flex items-center gap-2" aria-label={t("onboarding.progress", { step, total })}>
      {Array.from({ length: total }, (_, i) => {
        const n = i + 1;
        return (
          <li
            key={n}
            aria-current={n === step ? "step" : undefined}
            className={cn("h-1.5 rounded-full transition-all duration-300", n === step ? "w-8 bg-primary" : n < step ? "w-4 bg-primary/50" : "w-4 bg-border")}
          />
        );
      })}
    </ol>
  );
}

const TIP_ROTATE_MS = 4200;

const ADMIN_TIPS = [
  "onboarding.tips.admin1",
  "onboarding.tips.admin2",
  "onboarding.tips.admin3",
  "onboarding.tips.admin4",
  "onboarding.tips.admin5",
  "onboarding.tips.admin6",
  "onboarding.tips.admin7",
  "onboarding.tips.admin8",
] as const satisfies readonly MessageKey[];

const USER_TIPS = [
  "onboarding.tips.user1",
  "onboarding.tips.user2",
  "onboarding.tips.user3",
  "onboarding.tips.user4",
  "onboarding.tips.user5",
] as const satisfies readonly MessageKey[];

function SidePanel({ admin, step }: { admin: boolean; step: number }) {
  const t = useT();
  const tips: readonly MessageKey[] = admin ? ADMIN_TIPS : USER_TIPS;
  const count = tips.length;
  const [tip, setTip] = useState(0);

  useEffect(() => {
    const id = setInterval(() => setTip((n) => (n + 1) % count), TIP_ROTATE_MS);
    return () => clearInterval(id);
  }, [count]);

  return (
    <aside
      aria-hidden
      className="relative hidden w-[26rem] shrink-0 flex-col justify-between overflow-hidden bg-primary p-10 text-primary-foreground lg:flex"
    >
      <div
        className="pointer-events-none absolute inset-0 opacity-30 [background:radial-gradient(28rem_20rem_at_20%_10%,white,transparent),radial-gradient(24rem_18rem_at_90%_100%,white,transparent)]"
      />
      <div className="relative flex items-center gap-3">
        <BrandMark className="size-10" />
        <span className="text-lg font-semibold tracking-tight">{t("onboarding.side.brand")}</span>
      </div>
      <div className="relative grid gap-3">
        <p className="text-xs font-semibold uppercase tracking-widest opacity-70">{t("onboarding.side.step", { step })}</p>
        <p key={tip} className="min-h-24 animate-[rio-fade-in_400ms_ease-out] text-xl font-medium leading-snug motion-reduce:animate-none">
          {t(tips[tip % count] ?? tips[0]!)}
        </p>
      </div>
    </aside>
  );
}
