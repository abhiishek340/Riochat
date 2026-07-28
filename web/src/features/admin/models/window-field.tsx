"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { groupDigits, parseWindowInput, removeWindow, WINDOW_PRESETS, windowOf, writeCustomWindow } from "./context";
import type { CapsDoc } from "./caps-doc";
import { Labeled } from "./ui";

const presetLabel = (n: number) => (n >= 1_000_000 ? `${n / 1_000_000}M` : `${n / 1000}K`);

/**
 * The context window: a custom value (digits, shown with grouping, committed
 * on Enter or blur) or automatic, where the detected value is only a hint
 * until the model is saved.
 */
export function WindowField({ doc, detected, fallback, disabled }: { doc: CapsDoc; detected: number | null; fallback: number; disabled?: boolean }) {
  const t = useT();
  const state = windowOf(doc.caps);
  const shownValue = state.mode === "custom" && state.value !== null ? groupDigits(state.value) : "";
  const [text, setText] = useState(shownValue);
  useEffect(() => setText(shownValue), [shownValue]);

  const commit = (raw: string) => {
    const input = parseWindowInput(raw);
    if (input.kind === "invalid") {
      toast.error(t("adminModels.window.invalid"));
      setText(shownValue);
      return;
    }
    if (input.kind === "auto") {
      if (state.mode === "custom") doc.update(removeWindow);
      setText("");
      return;
    }
    doc.update((c) => writeCustomWindow(c, input.value));
    setText(groupDigits(input.value));
  };

  const auto = state.mode !== "custom";
  const autoValue = detected ?? (state.mode === "auto" ? state.value : null);
  return (
    <Labeled group label={t("adminModels.window.label")} hint={auto ? t("adminModels.window.autoHint", { value: groupDigits(autoValue ?? fallback), source: t(autoValue === null ? "adminModels.window.fromFallback" : "adminModels.window.fromCatalog") }) : t("adminModels.window.customHint")}>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          inputMode="numeric"
          value={text}
          disabled={disabled || !doc.valid}
          placeholder={groupDigits(autoValue ?? fallback)}
          aria-label={t("adminModels.window.label")}
          onChange={(e) => setText(groupDigits(e.target.value))}
          onBlur={() => text !== shownValue && commit(text)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              commit(text);
            }
          }}
          className="w-44 tabular-nums"
        />
        <div className="flex flex-wrap gap-1" role="group" aria-label={t("adminModels.window.presets")}>
          {WINDOW_PRESETS.map((n) => (
            <Button key={n} type="button" size="sm" variant={state.value === n && !auto ? "default" : "outline"} disabled={disabled || !doc.valid} onClick={() => commit(String(n))} className={cn("px-2.5")}>
              {presetLabel(n)}
            </Button>
          ))}
          <Button type="button" size="sm" variant={auto ? "default" : "outline"} disabled={disabled || !doc.valid} onClick={() => commit("")}>
            {t("adminModels.window.auto")}
          </Button>
        </div>
      </div>
    </Labeled>
  );
}
