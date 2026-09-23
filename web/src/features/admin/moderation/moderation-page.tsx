"use client";

import { Activity } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { Checkbox } from "../components/checkbox";
import { LoadError } from "../components/load-error";
import { AdminPage, Section, SettingRow } from "../components/page";
import { AdminSection } from "../components/section-guard";
import { useNotify } from "../lib/use-notify";
import { problemText } from "../lib/problem";
import { getConfig, probeService, putConfig } from "./api";
import {
  POLICY_LISTS,
  policyBody,
  policyDirty,
  probeHealthy,
  serviceBody,
  serviceDirty,
  serviceDraftOf,
  togglePolicyCategory,
  validatePolicy,
  validateService,
  type CategoryCatalog,
  type ModerationConfig,
  type Policy,
  type PolicyList,
  type ProbeResult,
  type ServiceDraft,
} from "./rules";

export function AdminContentModerationPage() {
  return (
    <AdminSection feature="contentModeration" superOnly>
      <ModerationContent />
    </AdminSection>
  );
}

function ModerationContent() {
  const t = useT();
  const notify = useNotify();
  const [config, setConfig] = useState<ModerationConfig | null>(null);
  const [catalog, setCatalog] = useState<CategoryCatalog>({ text: [], image: [] });
  const [service, setService] = useState<ServiceDraft | null>(null);
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [probe, setProbe] = useState<{ running: boolean; result: ProbeResult | null; failed: boolean }>({ running: false, result: null, failed: false });
  const [saving, setSaving] = useState<"service" | "policy" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);

  const apply = useCallback((next: ModerationConfig) => {
    setConfig(next);
    setService(serviceDraftOf(next));
    setPolicy(next.policy);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoadFailed(false);
    getConfig(controller.signal).then(
      ({ config: loaded, categories }) => {
        apply(loaded);
        setCatalog(categories);
      },
      (e: unknown) => {
        if (isAbortError(e)) return;
        setLoadFailed(true);
        notify.error("adminModeration.toast.loadFailed", e);
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt]);

  if (!config || !service || !policy) {
    return (
      <AdminPage title={t("adminModeration.title")} description={t("adminModeration.description")}>
        {loadFailed ? (
          <LoadError message={t("adminModeration.toast.loadFailed")} onRetry={() => setAttempt((n) => n + 1)} />
        ) : (
          <>
            <Skeleton className="h-64 w-full" />
            <Skeleton className="h-64 w-full" />
          </>
        )}
      </AdminPage>
    );
  }

  const editService = (patch: Partial<ServiceDraft>) => {
    setService({ ...service, ...patch });
    setProbe((p) => ({ ...p, result: null, failed: false }));
    setError(null);
  };

  const saveService = async () => {
    const found = validateService(service, config, policy);
    if (found) {
      setError(problemText(t, found));
      notify.invalid("adminModeration.toast.saveFailed", found);
      return;
    }
    setSaving("service");
    try {
      const saved = await putConfig(serviceBody(service, config, policy));
      // Keep unsaved policy edits only when the policy was not part of this save.
      const keepPolicy = config.enabled && policyDirty(policy, config.policy);
      setConfig(saved);
      setService(serviceDraftOf(saved));
      if (!keepPolicy) setPolicy(saved.policy);
      notify.success("adminModeration.toast.saved");
    } catch (e) {
      notify.error("adminModeration.toast.saveFailed", e);
    } finally {
      setSaving(null);
    }
  };

  const savePolicy = async () => {
    const found = validatePolicy(policy);
    if (found) {
      notify.invalid("adminModeration.toast.saveFailed", found);
      return;
    }
    setSaving("policy");
    try {
      const saved = await putConfig({ policy: policyBody(policy) });
      setConfig((c) => (c ? { ...c, policy: saved.policy } : saved));
      setPolicy(saved.policy);
      notify.success("adminModeration.toast.saved");
    } catch (e) {
      notify.error("adminModeration.toast.saveFailed", e);
    } finally {
      setSaving(null);
    }
  };

  const runProbe = async () => {
    setProbe({ running: true, result: null, failed: false });
    try {
      setProbe({ running: false, result: await probeService(), failed: false });
    } catch (e) {
      setProbe({ running: false, result: null, failed: true });
      notify.error("adminModeration.toast.probeFailed", e);
    }
  };

  const dirtyService = serviceDirty(service, config);
  const dirtyPolicy = policyDirty(policy, config.policy);
  const busy = saving !== null;
  const policyEditable = config.enabled;
  const numeric = (field: "timeoutSeconds" | "maxConcurrency" | "queueCapacity") => (
    <SettingRow label={t(`adminModeration.service.${field}`)} hint={t(`adminModeration.service.${field}Hint`)} htmlFor={`mod-${field}`}>
      <Input id={`mod-${field}`} inputMode="numeric" value={service[field]} disabled={busy} onChange={(e) => editService({ [field]: e.target.value })} className="w-28 text-right tabular-nums" />
      {field === "timeoutSeconds" ? <span className="text-sm text-muted-foreground">{t("adminModeration.service.seconds")}</span> : null}
    </SettingRow>
  );

  return (
    <AdminPage title={t("adminModeration.title")} description={t("adminModeration.description")}>
      <Section title={t("adminModeration.service.title")} description={t("adminModeration.service.description")}>
        <div className="divide-y">
          <SettingRow label={t("adminModeration.service.enabled")} hint={t("adminModeration.service.enabledHint")} htmlFor="mod-enabled">
            <Switch id="mod-enabled" checked={service.enabled} disabled={busy} onCheckedChange={(on) => editService({ enabled: on })} />
          </SettingRow>
          {service.enabled ? (
            <>
              <SettingRow label={t("adminModeration.service.baseUrl")} htmlFor="mod-base">
                <Input id="mod-base" value={service.baseUrl} disabled={busy} onChange={(e) => editService({ baseUrl: e.target.value })} className="sm:w-80" spellCheck={false} />
              </SettingRow>
              <SettingRow label={t("adminModeration.service.apiKey")} hint={t("adminModeration.service.apiKeyHint")} htmlFor="mod-key">
                <Input
                  id="mod-key"
                  type="password"
                  autoComplete="new-password"
                  value={service.apiKey}
                  disabled={busy}
                  placeholder={config.hasAPIKey ? (config.apiKeyMasked ?? t("adminCommon.settings.configured")) : undefined}
                  onChange={(e) => editService({ apiKey: e.target.value })}
                  className="sm:w-80"
                />
              </SettingRow>
              <SettingRow label={t("adminModeration.service.model")} htmlFor="mod-model">
                <Input id="mod-model" value={service.model} disabled={busy} onChange={(e) => editService({ model: e.target.value })} className="sm:w-80" spellCheck={false} />
              </SettingRow>
              {numeric("timeoutSeconds")}
              {numeric("maxConcurrency")}
              {numeric("queueCapacity")}
            </>
          ) : null}
        </div>
        {error ? (
          <p role="alert" className="mt-2 text-sm font-medium text-destructive">
            {error}
          </p>
        ) : null}
        <div className="mt-4 flex flex-wrap items-center gap-2 border-t pt-4">
          {config.enabled ? (
            <Button variant="outline" loading={probe.running} onClick={runProbe}>
              <Activity />
              {probe.running ? t("adminModeration.probe.running") : t("adminModeration.probe.button")}
            </Button>
          ) : null}
          {dirtyService ? (
            <div className="ml-auto flex items-center gap-2">
              <span className="text-sm text-muted-foreground">{t("adminModeration.service.unsaved")}</span>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => {
                  setService(serviceDraftOf(config));
                  setError(null);
                }}
              >
                {t("adminModeration.service.revert")}
              </Button>
              <Button loading={saving === "service"} disabled={busy} onClick={saveService}>
                {t("adminModeration.service.save")}
              </Button>
            </div>
          ) : null}
        </div>
        <ProbeSummary state={probe} />
      </Section>

      <Section
        title={t("adminModeration.policy.title")}
        description={t("adminModeration.policy.description")}
        actions={dirtyPolicy ? <Badge tone="warning">{t("adminModeration.service.unsaved")}</Badge> : null}
      >
        {!policyEditable ? <p className="mb-3 text-sm text-muted-foreground">{t("adminModeration.policy.needsService")}</p> : null}
        <div className="grid gap-4 lg:grid-cols-2">
          {POLICY_LISTS.map((list) => (
            <CategoryList
              key={list}
              list={list}
              options={list.endsWith("TextCategories") ? catalog.text : catalog.image}
              policy={policy}
              disabled={!policyEditable || busy}
              onChange={setPolicy}
            />
          ))}
        </div>
        {dirtyPolicy ? (
          <div className="mt-4 flex flex-wrap items-center justify-end gap-2 border-t pt-4">
            <Button variant="outline" disabled={busy} onClick={() => setPolicy(config.policy)}>
              {t("adminModeration.policy.revert")}
            </Button>
            <Button loading={saving === "policy"} disabled={busy || !policyEditable} onClick={savePolicy}>
              {t("adminModeration.policy.save")}
            </Button>
          </div>
        ) : null}
      </Section>
    </AdminPage>
  );
}

function CategoryList({ list, options, policy, disabled, onChange }: { list: PolicyList; options: string[]; policy: Policy; disabled: boolean; onChange: (policy: Policy) => void }) {
  const t = useT();
  const chosen = new Set(policy[list]);
  // Categories saved earlier but no longer in the catalog stay visible so they can be removed.
  const all = [...options, ...policy[list].filter((c) => !options.includes(c))];
  return (
    <fieldset className="rounded-lg border p-3" disabled={disabled}>
      <legend className="px-1 text-sm font-semibold">{t(`adminModeration.policy.lists.${list}`)}</legend>
      <div className="mb-2 flex items-center justify-between gap-2 text-[13px] text-muted-foreground">
        <span>{t("adminModeration.policy.enabledCount", { count: chosen.size })}</span>
        <span className="flex gap-1">
          <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => onChange({ ...policy, [list]: [...all] })}>
            {t("adminModeration.policy.selectAll")}
          </Button>
          <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => onChange({ ...policy, [list]: [] })}>
            {t("adminModeration.policy.clear")}
          </Button>
        </span>
      </div>
      <div className="grid max-h-64 gap-1.5 overflow-auto pr-1 sm:grid-cols-2">
        {all.map((category) => (
          <label key={category} className="flex items-center gap-2 text-sm">
            <Checkbox checked={chosen.has(category)} disabled={disabled} onCheckedChange={(on) => onChange(togglePolicyCategory(policy, list, category, on))} />
            <span className="truncate font-mono text-[12.5px]">{category}</span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function ProbeSummary({ state }: { state: { running: boolean; result: ProbeResult | null; failed: boolean } }) {
  const t = useT();
  if (state.failed) return <p className="mt-3 text-sm font-medium text-destructive">{t("adminModeration.probe.failed")}</p>;
  if (!state.result) return null;
  const r = state.result;
  const line = (label: string, target: ProbeResult["text"]) => (
    <li className="flex flex-wrap items-center gap-2">
      <span className="font-medium">{label}</span>
      <span className={target.valid ? "text-success" : "text-destructive"}>{target.valid ? t("adminModeration.probe.valid", { ms: target.latencyMS }) : t("adminModeration.probe.invalid", { ms: target.latencyMS })}</span>
      {target.error ? <span className="text-muted-foreground">{target.error}</span> : null}
    </li>
  );
  return (
    <div className="mt-3 space-y-1.5 rounded-lg border bg-muted/40 p-3 text-sm" role="status">
      <div className="flex items-center gap-2 font-medium">
        {t("adminModeration.probe.title")}
        <Badge tone={probeHealthy(r) ? "success" : "warning"}>{probeHealthy(r) ? t("adminModeration.probe.healthy") : t("adminModeration.probe.unhealthy")}</Badge>
      </div>
      <ul className="space-y-1">
        {line(t("adminModeration.probe.text"), r.text)}
        {line(t("adminModeration.probe.image"), r.image)}
      </ul>
    </div>
  );
}
