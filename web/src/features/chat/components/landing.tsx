"use client";

import { useMemo, useState } from "react";
import { FolderClosed, VenetianMask } from "lucide-react";
import { Badge, Tooltip } from "@/components/ui";
import { AppLogo } from "@/components/brand/logo";
import { useSession } from "@/lib/session/provider";
import { useT } from "@/lib/i18n/provider";
import { displayNameOf, periodOf, TITLE_USES, TITLE_VARIANTS } from "../lib/greeting";
import type { ProjectDTO } from "../types";

/** Greeting (or the project name) above the centred composer. */
export function LandingHeading({ project, temporary }: { project: ProjectDTO | null; temporary: boolean }) {
  const t = useT();
  const { user } = useSession();
  // The wording is picked once per page load.
  const [variant] = useState(() => Math.floor(Math.random() * TITLE_VARIANTS));
  const now = useMemo(() => new Date(), []);
  const period = periodOf(user, now);
  const name = displayNameOf(user) || t("chatMisc.greeting.fallbackName");
  const greeting = t(`chatMisc.greeting.word.${period}` as "chatMisc.greeting.word.morning");
  const use = TITLE_USES[variant] ?? "greeting";
  const title = t(`chatMisc.greeting.title.${period}.${use}${use === "both" ? "" : variant}` as "chatMisc.greeting.title.morning.both", { greeting, name });

  return (
    <div className="mb-6 flex flex-col items-center gap-3 text-center">
      <div className="flex items-center gap-3">
        <AppLogo className="size-10 animate-[rio-pulse_4s_ease-in-out_infinite] motion-reduce:animate-none" />
        {temporary ? <VenetianMask className="size-7 text-muted-foreground" aria-hidden /> : null}
      </div>
      {temporary ? (
        <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">{t("chatMisc.temp.title")}</h2>
      ) : project ? (
        <div className="grid justify-items-center gap-2">
          <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">{project.name}</h2>
          <Tooltip label={t("chatNav.project.modeHint")}>
            <Badge tone="primary" tabIndex={0}>
              <FolderClosed className="size-3" /> {t("chatNav.project.mode")}
            </Badge>
          </Tooltip>
        </div>
      ) : (
        <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">{title}</h2>
      )}
    </div>
  );
}
