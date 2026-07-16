"use client";

import { useMemo, useState } from "react";
import { Brain, Check, ChevronDown, Lock, Minus, Pin, PinOff, Plus, RotateCcw, SlidersHorizontal, Wrench } from "lucide-react";
import { Button, Popover, PopoverContent, PopoverTrigger, Switch, Tooltip, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import {
  displayValue,
  isChanged,
  MAX_TOOLBAR_PINS,
  REASONING_CONTROL_ID,
  resolvePlacements,
  setPlacement,
  stepValue,
  validateNumber,
  type ControlValue,
  type PlacementOverrides,
  type PlaceableItem,
  parsePlacementOverrides,
} from "../../lib/controls";
import { coerceControlValue, mediaControls, type NativeToolOption, type OptionControl } from "../../lib/model-caps";
import { disableToolPayload, enableToolPayload, getAtPath, isToolPayloadEnabled, removeAtPath, setAtPath, type Options } from "../../lib/options";
import type { ControlDTO, ModelOptionPolicyDTO } from "../../types";
import { saveSettings } from "../../state/resources";
import type { ModelState } from "../../hooks/use-model-state";

type Item = { kind: "control"; id: string; control: ControlDTO } | { kind: "tool"; id: string; tool: NativeToolOption };

function levelLabel(t: ReturnType<typeof useT>, value: ControlValue | null | undefined): string {
  if (value === null || value === undefined || value === "") return t("chatModel.controls.default");
  const v = String(value);
  const known = ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "auto"];
  return known.includes(v) ? t(`chatModel.controls.level.${v}` as "chatModel.controls.level.low") : v;
}

function optionLabel(t: ReturnType<typeof useT>, control: ControlDTO, value: string): string {
  if (control.id === REASONING_CONTROL_ID) return levelLabel(t, value);
  return control.options?.find((o) => o.value === value)?.label ?? value;
}

function ControlEditor({ control, model }: { control: ControlDTO; model: ModelState }) {
  const t = useT();
  const value = displayValue(control, model.choices);
  const changed = isChanged(control, model.choices);
  const locked = control.locked;

  if (control.type === "toggle") {
    const on = value === "on";
    return (
      <div className="flex items-center justify-between gap-3">
        <Switch checked={on} disabled={locked} onCheckedChange={(v) => model.setChoice(control.id, v ? "on" : "off")} aria-label={control.label} />
      </div>
    );
  }
  if (control.type === "number") {
    const current = value === null || value === "" ? null : Number(value);
    return (
      <div className="flex items-center gap-1.5">
        <Button variant="outline" size="icon-xs" disabled={locked} aria-label={t("chatModel.controls.decrease")} onClick={() => {
          const next = stepValue(control, current ?? Number(control.default ?? control.min ?? 0), -1);
          if (next !== null) model.setChoice(control.id, next);
        }}>
          <Minus />
        </Button>
        <NumberField control={control} value={current} locked={locked} onCommit={(n) => model.setChoice(control.id, n)} />
        <Button variant="outline" size="icon-xs" disabled={locked} aria-label={t("chatModel.controls.increase")} onClick={() => {
          const next = stepValue(control, current ?? Number(control.default ?? control.min ?? 0), 1);
          if (next !== null) model.setChoice(control.id, next);
        }}>
          <Plus />
        </Button>
        {changed ? (
          <button type="button" className="text-xs text-muted-foreground hover:text-foreground" onClick={() => model.setChoice(control.id, null)}>
            {t("chatModel.controls.default")}
          </button>
        ) : null}
      </div>
    );
  }
  return (
    <div className="grid gap-0.5">
      {(control.options ?? []).map((o) => {
        const active = String(value ?? "") === o.value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={locked}
            onClick={() => model.setChoice(control.id, o.value === String(control.default ?? "") ? null : o.value)}
            className={cn("flex items-center gap-2 rounded-md px-2 py-1 text-left text-sm hover:bg-accent disabled:opacity-60", active && "bg-accent/60")}
          >
            <span className="flex-1">
              {optionLabel(t, control, o.value)}
              {String(control.default ?? "") === o.value ? <span className="ml-1 text-xs text-muted-foreground">({t("chatModel.controls.defaultMarker")})</span> : null}
            </span>
            {active ? <Check className="size-3.5 text-primary" /> : null}
          </button>
        );
      })}
    </div>
  );
}

function NumberField({ control, value, locked, onCommit }: { control: ControlDTO; value: number | null; locked: boolean; onCommit: (n: number | null) => void }) {
  const t = useT();
  const [text, setText] = useState<string | null>(null);
  const shown = text ?? (value === null ? "" : String(value));
  const invalid = text !== null && text.trim() !== "" && validateNumber(control, text) === null;
  return (
    <div className="grid">
      <input
        value={shown}
        disabled={locked}
        inputMode="decimal"
        aria-label={control.label}
        aria-invalid={invalid || undefined}
        placeholder={control.default ?? ""}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => {
          if (text === null) return;
          if (text.trim() === "") onCommit(null);
          else {
            const n = validateNumber(control, text);
            if (n !== null) onCommit(n);
          }
          setText(null);
        }}
        onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
        className={cn("h-6 w-16 rounded-md border bg-background px-1.5 text-center text-xs tabular-nums outline-none focus-visible:ring-2 focus-visible:ring-ring", invalid && "border-destructive")}
      />
      {invalid ? (
        <span className="mt-0.5 text-[10px] text-destructive">
          {typeof control.min === "number" && typeof control.max === "number" ? t("chatModel.controls.invalidRange", { min: control.min, max: control.max }) : t("chatModel.controls.invalidNumber")}
        </span>
      ) : null}
    </div>
  );
}

function ToolToggle({ tool, options, setOptions, disabled }: { tool: NativeToolOption; options: Options; setOptions: ModelState["setOptions"]; disabled?: boolean }) {
  const on = tool.payloads.length > 0 && tool.payloads.every((p) => isToolPayloadEnabled(options, p));
  return (
    <Switch
      checked={on}
      disabled={disabled}
      aria-label={tool.label}
      onCheckedChange={(v) => setOptions((prev) => tool.payloads.reduce((acc, p) => (v ? enableToolPayload(acc, p) : disableToolPayload(acc, p)), prev))}
    />
  );
}
