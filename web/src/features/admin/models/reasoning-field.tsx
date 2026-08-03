"use client";

import { useT } from "@/lib/i18n/provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input, NativeSelect } from "@/components/ui/input";
import { Checkbox } from "../components/checkbox";
import type { ReasoningTemplate, ResolvedReasoning } from "../llm-api";
import type { CapsDoc } from "./caps-doc";
import { writeReasoning } from "./capabilities";
import { declToValue, defaultLevel, isValidDecl, LEVELS, readDecl, REASONING_FORMATS, sortLevels, translateLevel, usesBudgets, type ReasoningDecl } from "./reasoning";
import { Labeled } from "./ui";

const STARTER: ReasoningDecl = { format: "openai", levels: ["low", "medium", "high"], default: "medium" };

/** Reasoning: automatic follows the catalog; custom edits the declaration (format, levels, default, budgets). */
export function ReasoningField({ doc, resolved, template, disabled }: { doc: CapsDoc; resolved: ResolvedReasoning | null; template: ReasoningTemplate | null; disabled?: boolean }) {
  const t = useT();
  const raw = doc.caps.reasoning;
  const custom = raw !== undefined;
  const decl = readDecl(raw);
  const invalid = custom && (decl === null || !isValidDecl(decl));
  const off = disabled || !doc.valid;

  const write = (next: ReasoningDecl) => doc.update((c) => writeReasoning(c, declToValue(next)));
  const startFrom = (): ReasoningDecl => (template ? { format: template.format, levels: sortLevels(template.levels), default: defaultLevel(template.levels), ...(template.budgets ? { budgets: template.budgets } : {}) } : STARTER);

  const setLevels = (levels: string[]) => {
    if (!decl) return;
    const sorted = sortLevels(levels);
    write({ ...decl, levels: sorted, default: sorted.includes(decl.default) ? decl.default : defaultLevel(sorted) });
  };

  return (
    <Labeled group label={t("adminModels.reasoning.label")} hint={custom ? undefined : t("adminModels.reasoning.autoHint")}>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" variant={custom ? "outline" : "default"} disabled={off} onClick={() => custom && doc.update((c) => writeReasoning(c, null))}>
          {t("adminModels.reasoning.auto")}
        </Button>
        <Button type="button" size="sm" variant={custom ? "default" : "outline"} disabled={off} onClick={() => !custom && write(startFrom())}>
          {t("adminModels.reasoning.custom")}
        </Button>
        {invalid ? <Badge tone="danger">{t("adminModels.reasoning.invalid")}</Badge> : null}
        {!custom && resolved ? <Badge tone="outline">{t("adminModels.reasoning.detected", { format: resolved.format, levels: resolved.levels.join(", ") })}</Badge> : null}
      </div>
      {custom && decl ? (
        <div className="space-y-3 rounded-lg border p-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <Labeled label={t("adminModels.reasoning.format")}>
              <NativeSelect value={decl.format} disabled={off} aria-label={t("adminModels.reasoning.format")} onChange={(e) => write({ ...decl, format: e.target.value })}>
                {!(REASONING_FORMATS as readonly string[]).includes(decl.format) ? <option value={decl.format}>{decl.format || "-"}</option> : null}
                {REASONING_FORMATS.map((f) => (
                  <option key={f} value={f}>
                    {t(`adminModels.reasoning.formats.${f}`)}
                  </option>
                ))}
              </NativeSelect>
            </Labeled>
            <Labeled label={t("adminModels.reasoning.default")}>
              <NativeSelect value={decl.default} disabled={off} aria-label={t("adminModels.reasoning.default")} onChange={(e) => write({ ...decl, default: e.target.value })}>
                {!decl.levels.includes(decl.default) ? <option value={decl.default}>{decl.default || "-"}</option> : null}
                {sortLevels(decl.levels).map((l) => (
                  <option key={l} value={l}>
                    {l}
                  </option>
                ))}
              </NativeSelect>
            </Labeled>
          </div>
          <Labeled group label={t("adminModels.reasoning.levels")}>
            <div className="flex flex-wrap gap-x-4 gap-y-2" role="group" aria-label={t("adminModels.reasoning.levels")}>
              {LEVELS.map((l) => (
                <label key={l} className="flex items-center gap-1.5 text-sm">
                  <Checkbox checked={decl.levels.includes(l)} disabled={off} onCheckedChange={(on) => setLevels(on ? [...decl.levels, l] : decl.levels.filter((x) => x !== l))} />
                  {l}
                </label>
              ))}
            </div>
          </Labeled>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-muted-foreground">
                  <th className="py-1 pr-3 font-medium">{t("adminModels.reasoning.level")}</th>
                  {usesBudgets(decl.format) ? <th className="py-1 pr-3 font-medium">{t("adminModels.reasoning.budget")}</th> : null}
                  <th className="py-1 font-medium">{t("adminModels.reasoning.sent")}</th>
                </tr>
              </thead>
              <tbody>
                {sortLevels(decl.levels).map((l) => (
                  <tr key={l} className="border-t">
                    <td className="py-1 pr-3 font-mono text-[12.5px]">{l}</td>
                    {usesBudgets(decl.format) ? (
                      <td className="py-1 pr-3">
                        {l === "none" ? (
                          <span className="text-muted-foreground">-</span>
                        ) : (
                          <Input
                            inputMode="numeric"
                            value={decl.budgets?.[l] !== undefined ? String(decl.budgets[l]) : ""}
                            disabled={off}
                            aria-label={`${t("adminModels.reasoning.budget")} ${l}`}
                            onChange={(e) => {
                              const digits = e.target.value.replace(/\D/g, "");
                              const budgets = { ...(decl.budgets ?? {}) };
                              if (digits === "") delete budgets[l];
                              else budgets[l] = Number(digits);
                              write({ ...decl, budgets });
                            }}
                            className="h-8 w-28 tabular-nums"
                          />
                        )}
                      </td>
                    ) : null}
                    <td className="py-1 font-mono text-[12.5px] text-muted-foreground">{translateLevel(decl.format, l, decl.budgets) || "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : null}
      {custom && !decl ? <p className="text-[13px] text-destructive">{t("adminModels.reasoning.unreadable")}</p> : null}
    </Labeled>
  );
}
