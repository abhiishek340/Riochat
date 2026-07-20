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

export function ModelControls({
  model,
  prefs,
  media,
  policy,
  extension,
}: {
  model: ModelState;
  prefs: { placements: string };
  media: boolean;
  policy: ModelOptionPolicyDTO | null;
  extension: boolean;
}) {
  const t = useT();
  const [overrides, setOverrides] = useState<PlacementOverrides | null>(null);
  const saved = useMemo(() => parsePlacementOverrides(prefs.placements), [prefs.placements]);
  const effective = overrides ?? saved;

  const items = useMemo<Item[]>(() => {
    if (media) return [];
    const list: Item[] = model.controls.map((c) => ({ kind: "control", id: `control:${c.id}`, control: c }));
    for (const tool of model.caps?.nativeTools ?? []) list.push({ kind: "tool", id: `tool:${tool.id}`, tool });
    return list;
  }, [media, model.controls, model.caps]);

  const placeable: PlaceableItem[] = items.map((i) => ({ id: i.id, defaultPlacement: i.kind === "control" && i.control.placement === "toolbar" ? "toolbar" : "menu" }));
  const placed = resolvePlacements(placeable, effective);
  const toolbar = items.filter((i) => placed[i.id] === "toolbar");

  const pin = async (item: Item, target: "toolbar" | "menu") => {
    const res = setPlacement(placeable, effective, item.id, target);
    if (!res.ok) {
      toast.error(t("chatModel.controls.pinLimit", { max: MAX_TOOLBAR_PINS }));
      return;
    }
    const before = effective;
    setOverrides(res.overrides);
    try {
      await saveSettings({ "chat.model_control_placements": JSON.stringify(res.overrides) });
      setOverrides(null);
    } catch {
      setOverrides(before);
      setTimeout(() => setOverrides(null), 0);
      toast.error(t("chatModel.controls.pinFailed"));
    }
  };

  const mediaList = useMemo(() => (media && model.caps ? mediaControls(model.caps, policy, extension ? "extension" : "chat") : []), [media, model.caps, policy, extension]);

  if (media) return mediaList.length > 0 ? <MediaControls controls={mediaList} model={model} /> : null;
  if (items.length === 0) return null;

  const changedAny = model.controls.some((c) => isChanged(c, model.choices));
  return (
    <div className="flex min-w-0 items-center gap-1" data-composer-controls>
      {toolbar.map((item) => (
        <ToolbarControl key={item.id} item={item} model={model} onUnpin={() => void pin(item, "menu")} />
      ))}
      <Popover>
        <Tooltip label={t("chatModel.controls.more")}>
          <PopoverTrigger asChild>
            <button type="button" aria-label={t("chatModel.controls.more")} className={cn("inline-flex size-8 items-center justify-center rounded-lg text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-accent", changedAny && "text-primary")}>
              <SlidersHorizontal className="size-4" />
            </button>
          </PopoverTrigger>
        </Tooltip>
        <PopoverContent side="top" align="start" className="w-80 p-2">
          <div className="flex items-center justify-between px-2 pb-1.5 pt-1">
            <span className="text-sm font-medium">{t("chatModel.controls.title")}</span>
            <button type="button" onClick={model.resetChoices} disabled={!changedAny} className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground disabled:opacity-40">
              <RotateCcw className="size-3" /> {t("chatModel.controls.resetAll")}
            </button>
          </div>
          <div className="grid max-h-[50dvh] gap-1 overflow-auto">
            {items.map((item) => {
              const isTool = item.kind === "tool";
              const label = isTool ? item.tool.label : item.control.label;
              const description = isTool ? item.tool.description : item.control.description;
              const pinned = placed[item.id] === "toolbar";
              return (
                <div key={item.id} className="grid gap-1.5 rounded-lg px-2 py-1.5 hover:bg-accent/40">
                  <div className="flex items-center gap-2">
                    {isTool ? <Wrench className="size-3.5 text-muted-foreground" /> : item.control.id === REASONING_CONTROL_ID ? <Brain className="size-3.5 text-muted-foreground" /> : null}
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">{label}</span>
                    {!isTool && item.control.locked ? (
                      <Tooltip label={t("chatModel.controls.lockedTip", { label, value: optionLabel(t, item.control, String(item.control.default ?? "")) })}>
                        <Lock className="size-3.5 text-muted-foreground" />
                      </Tooltip>
                    ) : null}
                    {isTool ? <ToolToggle tool={item.tool} options={model.options} setOptions={model.setOptions} /> : item.control.type === "toggle" ? <ControlEditor control={item.control} model={model} /> : null}
                    <Tooltip label={pinned ? t("chatModel.controls.unpin") : t("chatModel.controls.pin")}>
                      <button type="button" onClick={() => void pin(item, pinned ? "menu" : "toolbar")} aria-label={pinned ? t("chatModel.controls.unpin") : t("chatModel.controls.pin")} className="rounded p-1 text-muted-foreground hover:text-foreground">
                        {pinned ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
                      </button>
                    </Tooltip>
                  </div>
                  {description ? <p className="text-xs text-muted-foreground">{description}</p> : null}
                  {!isTool && item.control.type !== "toggle" ? <ControlEditor control={item.control} model={model} /> : null}
                </div>
              );
            })}
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}

function ToolbarControl({ item, model, onUnpin }: { item: Item; model: ModelState; onUnpin: () => void }) {
  const t = useT();
  if (item.kind === "tool") {
    const on = item.tool.payloads.length > 0 && item.tool.payloads.every((p) => isToolPayloadEnabled(model.options, p));
    return (
      <Tooltip label={item.tool.description || item.tool.label}>
        <button
          type="button"
          aria-pressed={on}
          onClick={() => model.setOptions((prev) => item.tool.payloads.reduce((acc, p) => (on ? disableToolPayload(acc, p) : enableToolPayload(acc, p)), prev))}
          className={cn("inline-flex h-8 items-center gap-1.5 rounded-lg px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring", on ? "bg-primary/12 text-primary" : "text-muted-foreground hover:bg-accent hover:text-accent-foreground")}
        >
          <Wrench className="size-3.5" />
          <span className="max-w-28 truncate">{item.tool.label}</span>
        </button>
      </Tooltip>
    );
  }
  const c = item.control;
  const value = displayValue(c, model.choices);
  const changed = isChanged(c, model.choices);
  const text = c.type === "toggle" ? (value === "on" ? t("chatModel.controls.on") : t("chatModel.controls.off")) : c.type === "number" ? (value === null || value === "" ? t("chatModel.controls.default") : String(value)) : optionLabel(t, c, String(value ?? ""));
  return (
    <Popover>
      <Tooltip label={c.description || (c.locked ? t("chatModel.controls.lockedTip", { label: c.label, value: text }) : t("chatModel.controls.valueTip", { label: c.label, value: text }))}>
        <PopoverTrigger asChild>
          <button
            type="button"
            className={cn("inline-flex h-8 max-w-44 items-center gap-1.5 rounded-lg px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-accent", changed ? "bg-primary/12 text-primary" : "text-muted-foreground hover:bg-accent hover:text-accent-foreground")}
          >
            {c.id === REASONING_CONTROL_ID ? <Brain className="size-3.5 shrink-0" /> : null}
            {c.locked ? <Lock className="size-3 shrink-0" /> : null}
            <span className="truncate">{c.id === REASONING_CONTROL_ID ? text : `${c.label}: ${text}`}</span>
            <ChevronDown className="size-3 shrink-0 opacity-60" />
          </button>
        </PopoverTrigger>
      </Tooltip>
      <PopoverContent side="top" align="start" className="w-56 p-2">
        <div className="flex items-center justify-between px-1 pb-1">
          <span className="text-sm font-medium">{c.label}</span>
          <button type="button" onClick={onUnpin} className="rounded p-1 text-muted-foreground hover:text-foreground" aria-label={t("chatModel.controls.unpin")}>
            <PinOff className="size-3.5" />
          </button>
        </div>
        <ControlEditor control={c} model={model} />
      </PopoverContent>
    </Popover>
  );
}

/** Controls of image and video models write straight into the request options. */
function MediaControls({ controls, model }: { controls: OptionControl[]; model: ModelState }) {
  const t = useT();
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1" data-media-controls>
      {controls.map((c) => {
        const current = getAtPath(model.options, c.path);
        const dflt = getAtPath(model.defaults, c.path);
        const text = current === undefined ? t("chatModel.controls.default") : String(current);
        return (
          <Popover key={c.path}>
            <PopoverTrigger asChild>
              <button type="button" className="inline-flex h-8 max-w-44 items-center gap-1 rounded-lg px-2 text-sm text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-accent">
                <span className="truncate">{c.label}: {c.type === "boolean" ? (current === true ? t("chatModel.controls.on") : t("chatModel.controls.off")) : text}</span>
                <ChevronDown className="size-3 shrink-0 opacity-60" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" className="w-56 p-2">
              <div className="px-1 pb-1 text-sm font-medium">{c.label}</div>
              {c.description ? <p className="px-1 pb-1.5 text-xs text-muted-foreground">{c.description}</p> : null}
              {c.type === "boolean" ? (
                <Switch checked={current === true} onCheckedChange={(v) => model.setOptions((o) => setAtPath(o, c.path, v))} aria-label={c.label} />
              ) : c.type === "select" ? (
                <div className="grid gap-0.5">
                  {c.options.map((o) => (
                    <button key={o.value} type="button" onClick={() => model.setOptions((prev) => setAtPath(prev, c.path, coerceControlValue(c, o.value, dflt)))} className={cn("flex items-center gap-2 rounded-md px-2 py-1 text-left text-sm hover:bg-accent", String(current ?? "") === o.value && "bg-accent/60")}>
                      <span className="flex-1">{o.label}</span>
                      {String(current ?? "") === o.value ? <Check className="size-3.5 text-primary" /> : null}
                    </button>
                  ))}
                </div>
              ) : (
                <input
                  defaultValue={current === undefined ? "" : String(current)}
                  placeholder={c.placeholder}
                  inputMode={c.type === "number" ? "decimal" : undefined}
                  aria-label={c.label}
                  onBlur={(e) => {
                    const raw = e.target.value.trim();
                    model.setOptions((prev) => (raw === "" ? removeAtPath(prev, c.path) : setAtPath(prev, c.path, coerceControlValue(c, raw, dflt))));
                  }}
                  className="h-8 w-full rounded-md border bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
              )}
              {current !== undefined ? (
                <button
                  type="button"
                  onClick={() => model.setOptions((prev) => (dflt === undefined ? removeAtPath(prev, c.path) : setAtPath(prev, c.path, dflt)))}
                  className="mt-1.5 inline-flex items-center gap-1 px-1 text-xs text-muted-foreground hover:text-foreground"
                >
                  <RotateCcw className="size-3" /> {t("chatModel.controls.default")}
                </button>
              ) : null}
            </PopoverContent>
          </Popover>
        );
      })}
    </div>
  );
}
