"use client";

import { useEffect, useMemo, useState } from "react";
import { Lock } from "lucide-react";
import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Switch, Tabs, TabsContent, TabsList, TabsTrigger, Textarea, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { KNOWN_CHOICES, pathPolicyStatus } from "../../lib/model-caps";
import { getAtPath, leafPaths, removeAtPath, setAtPath, type Options } from "../../lib/options";
import { isRecord } from "@/lib/utils/guards";
import type { ModelState } from "../../hooks/use-model-state";
import type { ModelOptionPolicyDTO } from "../../types";

type Status = "notEnabled" | "ignored" | "passes" | "locked" | "route";

function statusOf(path: string, options: Options, locked: string[], protocols: string[], policy: ModelOptionPolicyDTO | null): Status {
  if (locked.includes(path)) return "locked";
  if (getAtPath(options, path) === undefined) return "notEnabled";
  const s = pathPolicyStatus(policy, protocols, path);
  return s === "ignored" ? "ignored" : s === "route" ? "route" : "passes";
}

const TONE: Record<Status, "neutral" | "warning" | "success" | "outline" | "primary"> = { notEnabled: "outline", ignored: "warning", passes: "success", locked: "neutral", route: "primary" };

export function AdvancedOptionsDialog({ open, onOpenChange, model, policy }: { open: boolean; onOpenChange: (open: boolean) => void; model: ModelState; policy: ModelOptionPolicyDTO | null }) {
  const t = useT();
  const caps = model.caps;
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [working, setWorking] = useState<Options>({});
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) {
      setWorking(model.options);
      setText(JSON.stringify(model.options, null, 2));
      setError("");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const locked = caps?.lockedPaths ?? [];
  const protocols = caps?.protocols ?? [];

  const paths = useMemo(() => {
    const set = new Set<string>();
    for (const l of leafPaths(model.defaults)) set.add(l.path);
    for (const l of leafPaths(working)) set.add(l.path);
    for (const c of caps?.optionControls ?? []) set.add(c.path);
    set.delete("tools");
    return [...set].filter((p) => !p.startsWith("tools.")).sort();
  }, [model.defaults, working, caps]);

  const apply = (value: Options) => {
    const next: Options = { ...value };
    // Locked parameters always keep the model's own values.
    let out = next;
    for (const p of locked) {
      const dflt = getAtPath(model.defaults, p);
      out = dflt === undefined ? removeAtPath(out, p) : setAtPath(out, p, dflt);
    }
    model.setOptions(out);
  };

  const parseJson = (raw: string): Options | null => {
    try {
      const parsed: unknown = JSON.parse(raw);
      if (!isRecord(parsed)) {
        setError(t("chatComposer.advanced.mustBeObject"));
        return null;
      }
      setError("");
      return parsed;
    } catch (e) {
      setError(e instanceof Error ? e.message : t("chatComposer.advanced.invalidJson"));
      return null;
    }
  };

  const save = () => {
    const value = parseJson(text);
    if (!value) return;
    apply(value);
    onOpenChange(false);
  };

  const field = (path: string) => {
    const current = getAtPath(working, path);
    const dflt = getAtPath(model.defaults, path);
    const declared = caps?.optionControls.find((c) => c.path === path);
    const tail = path.split(".").pop() ?? path;
    const choices = declared?.options.map((o) => o.value) ?? KNOWN_CHOICES[tail];
    const isLocked = locked.includes(path);
    const set = (v: unknown) => {
      const next = v === undefined ? removeAtPath(working, path) : setAtPath(working, path, v);
      setWorking(next);
      setText(JSON.stringify(next, null, 2));
    };
    const kind = typeof (current ?? dflt) === "boolean" ? "boolean" : typeof (current ?? dflt) === "number" ? "number" : choices ? "select" : "text";
    return (
      <div key={path} className="grid gap-1 rounded-lg border p-2.5">
        <div className="flex flex-wrap items-center gap-2">
          <code className="min-w-0 flex-1 truncate text-xs font-medium">{path}</code>
          {isLocked ? <Lock className="size-3.5 text-muted-foreground" /> : null}
          <Badge tone={TONE[statusOf(path, working, locked, protocols, policy)]}>{t(`chatComposer.advanced.status.${statusOf(path, working, locked, protocols, policy)}` as "chatComposer.advanced.status.passes")}</Badge>
        </div>
        {kind === "boolean" ? (
          <Switch checked={current === true} disabled={isLocked} onCheckedChange={(v) => set(v)} aria-label={path} />
        ) : kind === "select" ? (
          <select value={current === undefined ? "" : String(current)} disabled={isLocked} onChange={(e) => set(e.target.value === "" ? undefined : typeof dflt === "number" ? Number(e.target.value) : e.target.value)} className="h-8 rounded-md border bg-background px-2 text-sm" aria-label={path}>
            <option value="">{t("chatComposer.advanced.unset")}</option>
            {(choices ?? []).map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        ) : (
          <input
            defaultValue={current === undefined ? "" : String(current)}
            disabled={isLocked}
            inputMode={kind === "number" ? "decimal" : undefined}
            aria-label={path}
            onBlur={(e) => {
              const raw = e.target.value.trim();
              if (raw === "") set(undefined);
              else if (kind === "number") {
                const n = Number(raw);
                if (Number.isFinite(n)) set(n);
              } else set(raw);
            }}
            className={cn("h-8 rounded-md border bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring")}
          />
        )}
      </div>
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("chatComposer.advanced.title")}</DialogTitle>
          <DialogDescription>{t("chatComposer.advanced.description", { model: model.model?.platformModelName ?? "" })}</DialogDescription>
        </DialogHeader>
        <Tabs defaultValue="json" onValueChange={(v) => v === "visual" && (() => { const parsed = parseJson(text); if (parsed) setWorking(parsed); })()}>
          <TabsList>
            <TabsTrigger value="json">{t("chatComposer.advanced.json")}</TabsTrigger>
            <TabsTrigger value="visual">{t("chatComposer.advanced.visual")}</TabsTrigger>
          </TabsList>
          <TabsContent value="json">
            <Textarea value={text} onChange={(e) => setText(e.target.value)} rows={14} spellCheck={false} className="font-mono text-xs" aria-label={t("chatComposer.advanced.json")} />
            {error ? <p className="mt-1.5 text-sm text-destructive">{error}</p> : null}
          </TabsContent>
          <TabsContent value="visual">
            <div className="grid max-h-[50dvh] gap-2 overflow-auto">
              {paths.length === 0 ? <p className="py-6 text-center text-sm text-muted-foreground">{t("chatComposer.advanced.noFields")}</p> : paths.map(field)}
            </div>
          </TabsContent>
        </Tabs>
        <DialogFooter className="sm:justify-between">
          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={() => {
                model.resetOptions();
                onOpenChange(false);
              }}
            >
              {t("chatComposer.advanced.reset")}
            </Button>
            <Button
              variant="outline"
              loading={busy}
              onClick={async () => {
                setBusy(true);
                try {
                  await model.restoreBackendDefaults();
                  onOpenChange(false);
                } catch {
                  toast.error(t("chatComposer.advanced.defaultsFailed"));
                } finally {
                  setBusy(false);
                }
              }}
            >
              {t("chatComposer.advanced.restoreBackend")}
            </Button>
          </div>
          <div className="flex gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button onClick={save}>{t("common.actions.save")}</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
