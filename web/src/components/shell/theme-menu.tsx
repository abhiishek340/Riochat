"use client";

import { Check, Monitor, Moon, Palette, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";
import { Tooltip } from "@/components/ui/tooltip";
import { useT, type MessageKey } from "@/lib/i18n/provider";
import { ACCENTS, THEME_MODES, isAccent, isMode } from "@/lib/theme/config";
import { useTheme } from "@/lib/theme/provider";

const ACCENT_SWATCH: Record<(typeof ACCENTS)[number], string> = {
  tide: "oklch(0.55 0.1 200)",
  ember: "oklch(0.58 0.15 45)",
  moss: "oklch(0.55 0.11 150)",
  dusk: "oklch(0.55 0.14 290)",
};

export function ThemeMenu() {
  const t = useT();
  const { mode, accent, resolved, setMode, setAccent } = useTheme();
  const Icon = resolved === "dark" ? Moon : Sun;
  return (
    <DropdownMenu>
      <Tooltip label={t("shell.theme.label")}>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label={t("shell.theme.label")}>
            <Icon />
          </Button>
        </DropdownMenuTrigger>
      </Tooltip>
      <DropdownMenuContent>
        <DropdownMenuLabel>{t("shell.theme.label")}</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={mode} onValueChange={(v) => isMode(v) && setMode(v)}>
          {THEME_MODES.map((m) => (
            <DropdownMenuRadioItem key={m} value={m}>
              {m === "light" ? <Sun className="mr-2 size-4" /> : m === "dark" ? <Moon className="mr-2 size-4" /> : <Monitor className="mr-2 size-4" />}
              {t(`shell.theme.${m}` as MessageKey)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="flex items-center gap-1.5">
          <Palette className="size-3.5" />
          {t("shell.theme.accent")}
        </DropdownMenuLabel>
        {ACCENTS.map((a) => (
          <DropdownMenuItem key={a} onSelect={(e) => { e.preventDefault(); if (isAccent(a)) setAccent(a); }}>
            <span aria-hidden className="size-4 rounded-full border" style={{ background: ACCENT_SWATCH[a] }} />
            <span className="flex-1">{t(`shell.theme.accents.${a}` as MessageKey)}</span>
            {accent === a ? <Check className="text-foreground" /> : null}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
