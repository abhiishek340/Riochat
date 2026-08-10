"use client";

import { Plus, RefreshCw, Settings2, Wand2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, NativeSelect, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { loadOfficial } from "../billing/api";
import { openRouterWindow } from "./context";
import { MultiSelectMenu, SelectMenu } from "../components/menu-select";
import { Sheet } from "../components/sheet";
import { formatDateTime } from "../lib/format";
import { problemText, type Problem } from "../lib/problem";
import { KINDS, parseKinds, parseStringList } from "../lib/protocols";
import { invalidateModels, loadPermissionGroups } from "../lib/reference";
import { useSnapshot } from "../lib/use-snapshot";
import type { PermissionGroup, PlatformModel } from "../lib/wire";
import { bindSource, createModel, getModelGroups, putModelGroups, resolveCatalog, updateModel, type CatalogResolve, type CatalogStatus, type DisplayGroup, type Vendor } from "../llm-api";
import { loadFallbackWindow, syncCatalogs, touchCatalog } from "./api";
import { CapsEditor, ModalitiesField } from "./caps-editor";
import { useCapsDoc } from "./caps-doc";
import { parseCaps, stringifyCaps } from "./capabilities";
import { finalizeCaps, needsOpenRouter } from "./finalize";
import { IconField } from "./icon-picker";
import { PresetsDialog } from "./presets-dialog";
import { ReasoningField } from "./reasoning-field";
import { blankModelForm, blankSource, createModelBody, formOfModel, hasSelection, sourceBody, sourcesToBind, syncOutcome, updateModelBody, validateModelForm, validateSources, type ModelForm, type SourceDraft } from "./rules";
import { SourcePicker, useUpstreamChoices } from "./source-picker";
import { Labeled } from "./ui";
import { WindowField } from "./window-field";

export interface ModelSheetProps {
  target: PlatformModel | "new" | null;
  onOpenChange: (open: boolean) => void;
  vendors: Vendor[];
  groups: DisplayGroup[];
  onSaved: (model: PlatformModel, created: boolean) => void;
  onManageSources: (model: PlatformModel) => void;
  onManagePresentation: () => void;
}

let draftSeq = 0;
const nextDraft = () => blankSource(`d${++draftSeq}`);

export function ModelSheet({ target, onOpenChange, vendors, groups, onSaved, onManageSources, onManagePresentation }: ModelSheetProps) {
  const t = useT();
  const { locale } = useI18n();
  const message = useErrorMessage();
  const { flags } = useCapabilities();
  const multiUser = flags.multiUser;
  const shown = useSnapshot(target);
  const existing = shown && shown !== "new" ? shown : null;
  const open = target !== null;

  const [form, setForm] = useState<ModelForm>(blankModelForm());
  const [capsKey, setCapsKey] = useState(0);
  const [capsProblem, setCapsProblem] = useState<Problem | null>(null);
  const [drafts, setDrafts] = useState<SourceDraft[]>([]);
  const [groupIds, setGroupIds] = useState<string[]>([]);
  const [initialGroups, setInitialGroups] = useState<string[]>([]);
  // Groups that reach this model through a rule (shown with an "auto" badge).
  const [matchedGroups, setMatchedGroups] = useState<string[]>([]);
  // The model's groups could not be read: editing them would replace what is stored.
  const [groupsUnread, setGroupsUnread] = useState(false);
  const [allGroups, setAllGroups] = useState<PermissionGroup[]>([]);
  const [resolved, setResolved] = useState<CatalogResolve | null>(null);
  const [resolveNonce, setResolveNonce] = useState(0);
  const [fallback, setFallback] = useState(128_000);
  const [catalog, setCatalog] = useState<CatalogStatus | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [presets, setPresets] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const upstreams = useUpstreamChoices(open && existing === null);

  const set = <K extends keyof ModelForm>(key: K, value: ModelForm[K]) => setForm((f) => ({ ...f, [key]: value }));
  const setCaps = useCallback((text: string) => setForm((f) => ({ ...f, capabilities: text })), []);
  const doc = useCapsDoc(form.capabilities, setCaps);

  // --- opening ---------------------------------------------------------------------------------
  useEffect(() => {
    if (target === null) return;
    const model = target === "new" ? null : target;
    setForm(model ? formOfModel(model, parseKinds(model.kindsJSON)) : blankModelForm());
    setCapsKey((k) => k + 1);
    setCapsProblem(null);
    setDrafts(model ? [] : [nextDraft()]);
    setGroupIds([]);
    setInitialGroups([]);
    setMatchedGroups([]);
    setResolved(null);
    setError(null);
  }, [target]);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    loadFallbackWindow(controller.signal).then(setFallback);
    touchCatalog(controller.signal).then(setCatalog, () => undefined);
    return () => controller.abort();
  }, [open]);

  useEffect(() => {
    if (!open || !multiUser) return;
    let live = true;
    setGroupsUnread(false);
    loadPermissionGroups().then(
      (list) => live && setAllGroups(list),
      () => undefined,
    );
    if (existing) {
      getModelGroups(existing.id).then(
        (reach) => {
          if (!live) return;
          const ids = reach.manualGroupIDs.map(String);
          setGroupIds(ids);
          setInitialGroups(ids);
          setMatchedGroups((reach.matchedGroupIDs ?? []).map(String));
        },
        (e: unknown) => {
          if (!live || isAbortError(e)) return;
          setGroupsUnread(true);
          toast.error(t("adminModels.toast.loadFailed"), { description: message(e) });
        },
      );
    }
    return () => {
      live = false;
    };
    // The translator and error mapper only format the failure text.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, multiUser, existing]);

  // --- protocols of the model's routes ---------------------------------------------------------
  const routeProtocols = useMemo(() => {
    if (existing) return parseStringList(existing.protocolsJSON);
    return [...new Set(drafts.map((d) => d.protocol).filter(Boolean))];
  }, [existing, drafts]);

  // --- catalog resolve, 300 ms after the last change ----------------------------------------------------
  const resolveKey = `${form.name.trim()}|${form.vendor}|${routeProtocols.join(",")}|${form.capabilities}|${resolveNonce}`;
  const latestForm = useRef(form);
  latestForm.current = form;
  useEffect(() => {
    if (!open || form.name.trim() === "") return;
    const controller = new AbortController();
    const id = setTimeout(() => {
      const f = latestForm.current;
      resolveCatalog({ name: f.name.trim(), vendor: f.vendor, protocols: routeProtocols, capabilitiesJSON: f.capabilities }, controller.signal).then(setResolved, () => undefined);
    }, 300);
    return () => {
      clearTimeout(id);
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, resolveKey]);

  const sync = async () => {
    setSyncing(true);
    try {
      const out = await syncCatalogs();
      const verdict = syncOutcome(out.catalog, out.openrouter);
      if (verdict.kind === "success") toast.success(t("adminModels.catalog.synced", { catalog: out.catalog.count ?? 0, openrouter: out.openrouter.count ?? 0 }));
      else if (verdict.kind === "warning") toast.warning(t(verdict.failed === "catalog" ? "adminModels.catalog.catalogFailed" : "adminModels.catalog.openrouterFailed"), { description: verdict.error });
      else toast.error(t("adminModels.catalog.bothFailed"), { description: verdict.errors.join("; ") });
      if (out.status) setCatalog(out.status);
      if (verdict.kind !== "error") setResolveNonce((n) => n + 1);
    } finally {
      setSyncing(false);
    }
  };

  // --- save ---------------------------------------------------------------------------------------
  const finalCaps = async (): Promise<string | null> => {
    const parsed = parseCaps(form.capabilities);
    if (!parsed.ok) return null;
    let catalogWindow: number | null = null;
    let consulted = false;
    try {
      const fresh = await resolveCatalog({ name: form.name.trim(), vendor: form.vendor, protocols: routeProtocols, capabilitiesJSON: form.capabilities });
      consulted = true;
      catalogWindow = fresh.contextWindow;
    } catch {
      consulted = false;
    }
    let openRouter: number | null = null;
    if (needsOpenRouter(parsed.value, catalogWindow)) {
      try {
        const items = await loadOfficial(false);
        openRouter = openRouterWindow(form.name, form.vendor, items.items);
      } catch {
        openRouter = null;
      }
    }
    const next = finalizeCaps({
      caps: parsed.value,
      catalog: { consulted, window: catalogWindow },
      openRouter,
      existing: existing ? { name: existing.platformModelName, vendor: existing.vendor } : null,
      form: { name: form.name, vendor: form.vendor },
    });
    return stringifyCaps(next);
  };

  const submit = async () => {
    if (busy) return;
    const invalid = validateModelForm(form);
    if (invalid) return setError(problemText(t, invalid));
    if (!doc.valid) return setError(t("adminModels.caps.invalidJson"));
    if (capsProblem) return setError(problemText(t, capsProblem));
    if (!existing) {
      const issue = validateSources(drafts);
      if (issue) return setError(t(`adminModels.sources.rowProblem`, { row: issue.index + 1, problem: t(`adminModels.sources.problems.${issue.issue}`) }));
    }
    setBusy(true);
    setError(null);
    try {
      const caps = await finalCaps();
      if (caps === null) throw new Error(t("adminModels.caps.invalidJson"));
      const wantedGroups = groupIds.map(Number);
      const groupsChanged = multiUser && !groupsUnread && JSON.stringify([...groupIds].sort()) !== JSON.stringify([...initialGroups].sort());
      let saved: PlatformModel;
      if (existing) {
        saved = await updateModel(existing.id, updateModelBody(form, caps));
        if (groupsChanged) {
          try {
            await putModelGroups(existing.id, wantedGroups);
          } catch (e) {
            // The model itself is saved: refresh the list behind the sheet, keep the sheet for a retry.
            invalidateModels();
            onSaved(saved, false);
            toast.error(t("adminModels.toast.groupsFailedAfterSave"), { description: message(e) });
            setError(message(e));
            return;
          }
        }
        invalidateModels();
        toast.success(t("adminModels.toast.updated"));
        onSaved(saved, false);
      } else {
        saved = await createModel(createModelBody(form, caps));
        // The model exists from here on: later failures are reported, never a reason to keep the sheet.
        if (multiUser && wantedGroups.length > 0) {
          try {
            await putModelGroups(saved.id, wantedGroups);
          } catch (e) {
            toast.warning(t("adminModels.toast.createdGroupsFailed"), { description: message(e) });
          }
        }
        let failed = 0;
        let lastError: unknown = null;
        for (const row of sourcesToBind(drafts)) {
          try {
            await bindSource(saved.id, sourceBody(row));
          } catch (e) {
            failed++;
            lastError = e;
          }
        }
        invalidateModels();
        if (failed > 0) toast.warning(t("adminModels.toast.createdPartial", { count: failed }), { description: message(lastError) });
        else toast.success(t("adminModels.toast.created"));
        onSaved(saved, true);
      }
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
      toast.error(t("adminModels.toast.saveFailed"), { description: message(e) });
    } finally {
      setBusy(false);
    }
  };

  // --- options ------------------------------------------------------------------------------------------
  const vendorOptions = useMemo(() => {
    const list = vendors.map((v) => ({ value: v.key, label: v.name, text: `${v.name} ${v.key}`, hint: v.key }));
    if (form.vendor && !list.some((o) => o.value === form.vendor)) list.unshift({ value: form.vendor, label: form.vendor, text: form.vendor, hint: "" });
    return list;
  }, [vendors, form.vendor]);
  const groupOptions = useMemo(() => [{ value: "0", label: t("adminModels.form.followVendor"), text: t("adminModels.form.followVendor") }, ...groups.map((g) => ({ value: String(g.id), label: g.name, text: g.name }))], [groups, t]);
  const kindOptions = KINDS.map((k) => ({ value: k, label: t(`adminModels.kinds.${k}`), text: t(`adminModels.kinds.${k}`) }));
  const permissionOptions = allGroups.map((g) => ({ value: String(g.id), label: g.name, text: g.name, hint: matchedGroups.includes(String(g.id)) ? t("adminModels.form.autoGroup") : undefined }));
  const vendorIcon = vendors.find((v) => v.key === form.vendor)?.icon ?? "";
  const reasoningResolved = resolved?.reasoning ?? null;

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      busy={busy}
      size="xl"
      title={existing ? t("adminModels.form.editTitle", { name: existing.platformModelName }) : t("adminModels.form.createTitle")}
      footer={
        <>
          {error ? <p role="alert" className="mr-auto max-w-[60%] text-[13px] text-destructive">{error}</p> : null}
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={submit}>
            {existing ? t("common.actions.save") : t("common.actions.create")}
          </Button>
        </>
      }
    >
      <div className="grid gap-6">
        <section className="grid gap-4 sm:grid-cols-2">
          <Field required className="sm:col-span-2">
            <FieldLabel>{t("adminModels.form.name")}</FieldLabel>
            <Input value={form.name} onChange={(e) => set("name", e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
          <Labeled
            group
            label={
              <span className="flex items-center justify-between gap-2">
                {t("adminModels.form.vendor")}
                <button type="button" onClick={onManagePresentation} className="inline-flex items-center gap-1 text-xs font-normal text-muted-foreground underline-offset-2 hover:underline">
                  <Settings2 className="size-3" />
                  {t("adminModels.presentation.manage")}
                </button>
              </span>
            }
          >
            <SelectMenu value={form.vendor} options={vendorOptions} searchable label={t("adminModels.form.vendor")} onChange={(v) => set("vendor", v)} />
          </Labeled>
          <Labeled label={t("adminModels.form.displayGroup")}>
            <SelectMenu value={String(form.displayGroupID)} options={groupOptions} searchable label={t("adminModels.form.displayGroup")} onChange={(v) => set("displayGroupID", Number(v))} />
          </Labeled>
          <Labeled label={t("adminModels.form.kinds")} hint={t("adminModels.form.kindsHint")}>
            <MultiSelectMenu values={form.kinds} options={kindOptions} label={t("adminModels.form.kinds")} placeholder={t("adminModels.form.noKinds")} onChange={(v) => set("kinds", v)} />
          </Labeled>
          <IconField value={form.icon} onChange={(v) => set("icon", v)} name={form.name} fallback={vendorIcon} label={t("adminModels.form.icon")} />
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={form.status === "active"} onCheckedChange={(on) => set("status", on ? "active" : "inactive")} />
            {t("adminModels.form.active")}
          </label>
          <Labeled label={t("adminModels.form.accessScope")} hint={t("adminModels.form.accessScopeHint")}>
            <NativeSelect value={form.accessScope} aria-label={t("adminModels.form.accessScope")} onChange={(e) => set("accessScope", e.target.value === "internal" ? "internal" : "public")}>
              <option value="public">{t("adminModels.scope.public")}</option>
              <option value="internal">{t("adminModels.scope.internal")}</option>
            </NativeSelect>
          </Labeled>
        </section>

        <section className="grid gap-4 rounded-lg border p-4">
          <WindowField doc={doc} detected={resolved?.contextWindow ?? null} fallback={fallback} disabled={busy} />
          <ReasoningField doc={doc} resolved={reasoningResolved} template={resolved?.reasoningTemplate ?? null} disabled={busy} />
          <ModalitiesField doc={doc} detected={resolved?.inputModalities ?? []} disabled={busy} />
        </section>

        <CapsEditor
          text={form.capabilities}
          setText={setCaps}
          resetKey={capsKey}
          routeProtocols={routeProtocols}
          onProblem={setCapsProblem}
          disabled={busy}
          headerActions={
            <>
              <Button type="button" size="sm" variant="outline" onClick={() => setPresets(true)}>
                <Wand2 />
                {t("adminModels.presets.open")}
              </Button>
              <Button type="button" size="sm" variant="outline" loading={syncing} onClick={sync} title={catalog?.fetchedAt ? t("adminModels.catalog.status", { count: catalog.modelCount, time: formatDateTime(catalog.fetchedAt, locale) }) : undefined}>
                <RefreshCw />
                {t("adminModels.catalog.sync")}
              </Button>
            </>
          }
        />

        <section className="grid gap-3 rounded-lg border p-4">
          <h3 className="text-sm font-semibold">{t("adminModels.breaker.title")}</h3>
          <div className="grid gap-3 sm:grid-cols-4">
            <Labeled label={t("adminModels.breaker.policy")}>
              <NativeSelect value={form.cbPolicyMode} aria-label={t("adminModels.breaker.policy")} onChange={(e) => set("cbPolicyMode", e.target.value === "enforced" ? "enforced" : "default")}>
                <option value="default">{t("adminModels.breaker.modes.default")}</option>
                <option value="enforced">{t("adminModels.breaker.modes.enforced")}</option>
              </NativeSelect>
            </Labeled>
            <Field>
              <FieldLabel>{t("adminModels.breaker.failure")}</FieldLabel>
              <Input inputMode="numeric" value={form.cbFailureThreshold} onChange={(e) => set("cbFailureThreshold", e.target.value)} className="tabular-nums" />
            </Field>
            <Field>
              <FieldLabel>{t("adminModels.breaker.duration")}</FieldLabel>
              <Input inputMode="numeric" value={form.cbDurationMin} onChange={(e) => set("cbDurationMin", e.target.value)} className="tabular-nums" />
            </Field>
            <Field>
              <FieldLabel>{t("adminModels.breaker.window")}</FieldLabel>
              <Input inputMode="numeric" value={form.cbWindowMin} onChange={(e) => set("cbWindowMin", e.target.value)} className="tabular-nums" />
            </Field>
          </div>
          <p className="text-[13px] text-muted-foreground">{t(form.cbPolicyMode === "enforced" ? "adminModels.breaker.enforcedHint" : "adminModels.breaker.defaultHint")}</p>
        </section>

        {multiUser ? (
          <Labeled label={t("adminModels.form.permissionGroups")} hint={matchedGroups.length > 0 ? `${t("adminModels.form.permissionGroupsHint")} ${t("adminModels.form.autoGroupHint")}` : t("adminModels.form.permissionGroupsHint")}>
            <MultiSelectMenu values={groupIds} disabled={groupsUnread} options={permissionOptions} searchable label={t("adminModels.form.permissionGroups")} placeholder={t("adminModels.form.noPermissionGroups")} onChange={setGroupIds} />
          </Labeled>
        ) : null}

        <Field>
          <FieldLabel>{t("adminModels.form.description")}</FieldLabel>
          <Textarea rows={3} value={form.description} onChange={(e) => set("description", e.target.value)} />
        </Field>
        <Field>
          <FieldLabel>{t("adminModels.form.systemPrompt")}</FieldLabel>
          <Textarea rows={6} value={form.systemPrompt} onChange={(e) => set("systemPrompt", e.target.value)} className="font-mono text-[12.5px]" />
          <FieldHint>{t("adminModels.form.systemPromptHint")}</FieldHint>
        </Field>

        {existing ? (
          <section className="space-y-1 rounded-lg border p-4 text-sm">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span>{t("adminModels.form.sourcesLine", { active: existing.activeSourceCount, total: existing.sourceCount })}</span>
              <Button size="sm" variant="outline" onClick={() => onManageSources(existing)}>
                {t("adminModels.actions.sources")}
              </Button>
            </div>
            <p className="text-[13px] text-muted-foreground">{t("adminModels.form.meta", { created: formatDateTime(existing.createdAt, locale), updated: formatDateTime(existing.updatedAt, locale) })}</p>
          </section>
        ) : (
          <section className="space-y-3">
            <div className="flex items-center justify-between gap-2">
              <div>
                <h3 className="text-sm font-semibold">{t("adminModels.sources.initialTitle")}</h3>
                <p className="text-[13px] text-muted-foreground">{t("adminModels.sources.initialHint")}</p>
              </div>
              <Button type="button" size="sm" variant="outline" onClick={() => setDrafts((d) => [...d, nextDraft()])}>
                <Plus />
                {t("adminModels.sources.addRow")}
              </Button>
            </div>
            {drafts.map((row) => (
              <SourcePicker
                key={row.key}
                row={row}
                kinds={form.kinds}
                upstreams={upstreams}
                disabled={busy}
                onChange={(patch) => setDrafts((list) => list.map((r) => (r.key === row.key ? { ...r, ...patch } : r)))}
                onRemove={drafts.length > 1 || hasSelection(row) ? () => setDrafts((list) => (list.length > 1 ? list.filter((r) => r.key !== row.key) : [nextDraft()])) : undefined}
              />
            ))}
          </section>
        )}
      </div>
      <PresetsDialog
        open={presets}
        onOpenChange={setPresets}
        selfId={existing?.id ?? null}
        routeProtocols={routeProtocols}
        onApply={(text) => {
          setCaps(text);
          setCapsKey((k) => k + 1);
        }}
      />
    </Sheet>
  );
}
