"use client";

import { Trash2, Undo2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { Collapsible } from "../components/collapsible";
import { Sheet } from "../components/sheet";
import { OptionSelect } from "../components/toolbar";
import { fillHeaders, HEADER_TEMPLATES, type HeaderFill } from "../lib/headers";
import { COMPATIBLE, COMPATIBLE_LABELS, KINDS, protocolLabel } from "../lib/protocols";
import { problemText } from "../lib/problem";
import { useSnapshot } from "../lib/use-snapshot";
import { createUpstream, updateUpstream, type Upstream } from "../llm-api";
import { blankForm, createBody, defaultCount, formOf, keyLines, keyProblem, protocolsForKind, updateBody, validateForm, withCompatible, type UpstreamForm } from "./rules";

export interface UpstreamSheetProps {
  target: Upstream | "new" | null;
  onOpenChange: (open: boolean) => void;
  /** After a create the model bindings of the new upstream open; after an edit the row is replaced. */
  onSaved: (upstream: Upstream, created: boolean) => void;
  onManageModels: (upstream: Upstream) => void;
}

export function UpstreamSheet({ target, onOpenChange, onSaved, onManageModels }: UpstreamSheetProps) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(target);
  const existing = shown && shown !== "new" ? shown : null;
  const [form, setForm] = useState<UpstreamForm>(blankForm());
  const [deleteIds, setDeleteIds] = useState<Set<string>>(new Set());
  const [confirmKey, setConfirmKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (target === null) return;
    setForm(target === "new" ? blankForm() : formOf(target));
    setDeleteIds(new Set());
    setError(null);
  }, [target]);

  const set = <K extends keyof UpstreamForm>(key: K, value: UpstreamForm[K]) => setForm((f) => ({ ...f, [key]: value }));
  const editing = existing !== null;

  const fillHeader = (entries: Record<string, string>, mode: "skip" | "strict") => {
    const out: HeaderFill = fillHeaders(form.headers, entries, mode);
    if (out.kind === "invalid") toast.error(t("adminUpstreams.headers.invalid"));
    else if (out.kind === "conflict") toast.error(t("adminUpstreams.headers.conflict", { names: out.names.join(", ") }));
    else if (out.kind === "unchanged") toast.info(t("adminUpstreams.headers.exists"));
    else set("headers", out.text);
  };

  const submit = async () => {
    if (busy) return;
    const invalid = validateForm(form, editing);
    if (invalid) {
      setError(t(`adminUpstreams.problems.${invalid}`));
      return;
    }
    if (existing) {
      const keys = keyProblem(existing, deleteIds, keyLines(form.keys));
      if (keys) {
        setError(problemText(t, keys));
        return;
      }
    }
    setBusy(true);
    setError(null);
    try {
      if (existing) {
        const body = updateBody(existing, form, deleteIds);
        if (Object.keys(body).length === 0) {
          onOpenChange(false);
          return;
        }
        const saved = await updateUpstream(existing.id, body);
        toast.success(t("adminUpstreams.toast.updated"));
        onSaved(saved, false);
      } else {
        const saved = await createUpstream(createBody(form));
        toast.success(t("adminUpstreams.toast.created"));
        onSaved(saved, true);
      }
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
      toast.error(t("adminUpstreams.toast.saveFailed"), { description: message(e) });
    } finally {
      setBusy(false);
    }
  };

  const numberField = (key: "connectTimeout" | "readTimeout" | "streamIdle" | "cbFailure" | "cbModel" | "cbDuration" | "cbWindow", label: string, placeholder?: string, hint?: string) => (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <Input inputMode="numeric" value={form[key]} placeholder={placeholder} onChange={(e) => set(key, e.target.value)} className="tabular-nums" />
      {hint ? <FieldHint>{hint}</FieldHint> : null}
    </Field>
  );

  const compatOptions = useMemo(() => COMPATIBLE.map((c) => ({ value: c, label: COMPATIBLE_LABELS[c] })), []);

  return (
    <Sheet
      open={target !== null}
      onOpenChange={onOpenChange}
      busy={busy}
      size="lg"
      title={existing ? t("adminUpstreams.form.editTitle", { name: existing.name }) : t("adminUpstreams.form.createTitle")}
      footer={
        <>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={submit}>
            {existing ? t("common.actions.save") : t("common.actions.create")}
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field required>
            <FieldLabel>{t("adminUpstreams.form.name")}</FieldLabel>
            <Input value={form.name} onChange={(e) => set("name", e.target.value)} autoComplete="off" />
          </Field>
          <Field required>
            <FieldLabel>{t("adminUpstreams.form.baseUrl")}</FieldLabel>
            <Input value={form.baseURL} onChange={(e) => set("baseURL", e.target.value)} placeholder="https://api.example.com/v1" autoComplete="off" spellCheck={false} />
          </Field>
        </div>

        <div className="grid gap-3">
          {existing && existing.apiKeyItems.length > 0 ? (
            <div className="space-y-2">
              <div className="text-sm font-medium">{t("adminUpstreams.keys.current")}</div>
              <ul className="space-y-1.5">
                {existing.apiKeyItems.map((k) => {
                  const staged = deleteIds.has(k.id);
                  return (
                    <li key={k.id} className="flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm">
                      <span className={`font-mono text-[12.5px] ${staged ? "text-muted-foreground line-through" : ""}`}>{k.keyMasked}</span>
                      {k.note ? <span className="truncate text-xs text-muted-foreground">{k.note}</span> : null}
                      {k.status && k.status !== "active" ? <Badge tone="neutral">{k.status}</Badge> : null}
                      {staged ? <span className="text-xs text-destructive">{t("adminUpstreams.keys.willDelete")}</span> : null}
                      <span className="ml-auto">
                        {staged ? (
                          <Button variant="ghost" size="icon-xs" aria-label={`${t("adminUpstreams.keys.restore")}: ${k.keyMasked}`} onClick={() => setDeleteIds((s) => new Set([...s].filter((x) => x !== k.id)))}>
                            <Undo2 />
                          </Button>
                        ) : (
                          <Button variant="ghost" size="icon-xs" aria-label={`${t("adminUpstreams.keys.delete")}: ${k.keyMasked}`} onClick={() => setConfirmKey(k.id)}>
                            <Trash2 className="text-destructive" />
                          </Button>
                        )}
                      </span>
                    </li>
                  );
                })}
              </ul>
            </div>
          ) : null}
          <Field required={!editing}>
            <FieldLabel>{editing ? t("adminUpstreams.keys.add") : t("adminUpstreams.keys.label")}</FieldLabel>
            <Textarea rows={3} value={form.keys} onChange={(e) => set("keys", e.target.value)} spellCheck={false} autoComplete="off" className="font-mono text-[12.5px]" />
            <FieldHint>{t("adminUpstreams.keys.hint")}</FieldHint>
          </Field>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel>{t("adminUpstreams.form.compatible")}</FieldLabel>
            <OptionSelect value={form.compatible} onChange={(v) => setForm((f) => withCompatible(f, v))} options={compatOptions} className="w-full" />
          </Field>
          <label className="flex items-center gap-3 self-end pb-2 text-sm font-medium">
            <Switch checked={form.status === "active"} onCheckedChange={(on) => set("status", on ? "active" : "inactive")} />
            {t("adminUpstreams.form.active")}
          </label>
        </div>

        {existing ? (
          <p className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
            {t("adminUpstreams.form.modelsLine", { active: existing.activeModelsCount, total: existing.modelsCount })}
            <Button variant="link" size="sm" onClick={() => onManageModels(existing)}>
              {t("adminUpstreams.actions.manageModels")}
            </Button>
          </p>
        ) : null}

        <Collapsible title={t("adminUpstreams.defaults.title")} summary={defaultCount(JSON.stringify(form.defaults)) > 0 ? t("adminUpstreams.defaults.count", { count: defaultCount(JSON.stringify(form.defaults)) }) : t("adminUpstreams.defaults.none")}>
          <p className="mb-3 text-[13px] text-muted-foreground">{t("adminUpstreams.defaults.hint")}</p>
          <div className="grid gap-3 sm:grid-cols-2">
            {KINDS.map((kind) => (
              <Field key={kind}>
                <FieldLabel>{t(`adminUpstreams.kinds.${kind}`)}</FieldLabel>
                <OptionSelect
                  value={form.defaults[kind] ?? ""}
                  onChange={(v) => setForm((f) => ({ ...f, defaults: { ...f.defaults, [kind]: v || undefined } }))}
                  options={[{ value: "", label: t("adminUpstreams.defaults.system") }, ...protocolsForKind(kind).map((p) => ({ value: p, label: protocolLabel(p) }))]}
                  className="w-full"
                />
              </Field>
            ))}
          </div>
        </Collapsible>

        <Collapsible title={t("adminUpstreams.timeouts.title")}>
          <div className="grid gap-3 sm:grid-cols-3">
            {numberField("connectTimeout", t("adminUpstreams.timeouts.connect"), "10000")}
            {numberField("readTimeout", t("adminUpstreams.timeouts.read"), "120000")}
            {numberField("streamIdle", t("adminUpstreams.timeouts.streamIdle"), "60000")}
          </div>
          <p className="mt-2 text-[13px] text-muted-foreground">{t("adminUpstreams.timeouts.hint")}</p>
        </Collapsible>

        <Collapsible title={t("adminUpstreams.breaker.title")}>
          <div className="grid gap-3 sm:grid-cols-2">
            {numberField("cbFailure", t("adminUpstreams.breaker.failure"))}
            {numberField("cbModel", t("adminUpstreams.breaker.model"), "0")}
            <Field>
              <FieldLabel>{t("adminUpstreams.breaker.logic")}</FieldLabel>
              <OptionSelect value={form.cbLogic} onChange={(v) => set("cbLogic", v)} options={[{ value: "or", label: t("adminUpstreams.breaker.or") }, { value: "and", label: t("adminUpstreams.breaker.and") }]} className="w-full" />
            </Field>
            {numberField("cbDuration", t("adminUpstreams.breaker.duration"))}
            {numberField("cbWindow", t("adminUpstreams.breaker.window"))}
          </div>
          <p className="mt-2 text-[13px] text-muted-foreground">{t("adminUpstreams.breaker.hint")}</p>
        </Collapsible>

        <Collapsible title={t("adminUpstreams.headers.title")}>
          <div className="space-y-2">
            <div className="flex justify-end">
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button type="button" variant="outline" size="sm">
                    {t("adminUpstreams.headers.quickFill")}
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuItem onSelect={() => fillHeader(HEADER_TEMPLATES.conversationId, "skip")}>{t("adminUpstreams.headers.conversationId")}</DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => fillHeader(HEADER_TEMPLATES.sessionId, "skip")}>{t("adminUpstreams.headers.sessionId")}</DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => fillHeader(HEADER_TEMPLATES.requestId, "skip")}>{t("adminUpstreams.headers.requestId")}</DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => fillHeader(HEADER_TEMPLATES.providerRequestId, "skip")}>{t("adminUpstreams.headers.providerRequestId")}</DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => fillHeader(HEADER_TEMPLATES.codexAffinity, "strict")}>{t("adminUpstreams.headers.codex")}</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            <Textarea rows={6} aria-label={t("adminUpstreams.headers.title")} value={form.headers} onChange={(e) => set("headers", e.target.value)} spellCheck={false} placeholder='{"X-Example": "value"}' className="font-mono text-[12.5px]" />
            <p className="text-[13px] text-muted-foreground">{t("adminUpstreams.headers.hint")}</p>
          </div>
        </Collapsible>
        <FieldError>{error}</FieldError>
      </div>

      <ConfirmDialog
        open={confirmKey !== null}
        onOpenChange={(open) => !open && setConfirmKey(null)}
        title={t("adminUpstreams.keys.confirmTitle")}
        description={t("adminUpstreams.keys.confirmBody")}
        confirmLabel={t("adminUpstreams.keys.delete")}
        destructive
        onConfirm={() => {
          if (confirmKey) setDeleteIds((s) => new Set(s).add(confirmKey));
        }}
      />
    </Sheet>
  );
}
