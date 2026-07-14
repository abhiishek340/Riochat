"use client";

import { Plus, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { api } from "@/lib/api/client";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { IdPicker } from "../components/id-picker";
import { ModelIcon } from "../components/model-icon";
import { LoadError } from "../components/load-error";
import { Sheet } from "../components/sheet";
import { OptionSelect } from "../components/toolbar";
import { listAllUpstreams, listModels, listVendors, createGroup, getGroupModels, getGroupUsers, patchGroup, putGroupModels, putGroupUsers, type GroupRule, type Upstream, type Vendor } from "../llm-api";
import { loadAllPages, readPage } from "../lib/paging";
import { protocolLabel, PROTOCOLS } from "../lib/protocols";
import { invalidateModels } from "../lib/reference";
import { useSnapshot } from "../lib/use-snapshot";
import type { PermissionGroup, PlatformModel, UserRecord } from "../lib/wire";
import { listProviders } from "../login/api";
import type { IdentityProvider } from "../login/rules";
import { SUBSCRIPTION_STATUSES } from "../users/rules";
import { addRule, canAddRule, factorFromPercent, percentFromFactor, refreshAfter, removeRule, RULE_TYPES, ruleKey } from "./rules";

export interface GroupSheetProps {
  target: PermissionGroup | "new" | null;
  onOpenChange: (open: boolean) => void;
  /** Called after a save attempt with what must be refreshed. */
  onChanged: (reload: boolean) => void;
}

interface ModelFilters {
  upstream: string;
  vendor: string;
  protocol: string;
}
interface UserFilters {
  subscriptionStatus: string;
  provider: string;
}

export function GroupSheet({ target, onOpenChange, onChanged }: GroupSheetProps) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(target);
  const group = shown && shown !== "new" ? shown : null;
  const isDefault = group?.isDefault ?? false;
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [factor, setFactor] = useState("1");
  const [modelIds, setModelIds] = useState<number[]>([]);
  const [rules, setRules] = useState<GroupRule[]>([]);
  const [userIds, setUserIds] = useState<number[]>([]);
  const [upstreams, setUpstreams] = useState<Upstream[]>([]);
  const [vendors, setVendors] = useState<Vendor[]>([]);
  const [providers, setProviders] = useState<IdentityProvider[]>([]);
  const [loading, setLoading] = useState(false);
  const [assignFailed, setAssignFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [needName, setNeedName] = useState(false);
  const [pickModels, setPickModels] = useState(false);
  const [pickUsers, setPickUsers] = useState(false);
  const [modelFilters, setModelFilters] = useState<ModelFilters>({ upstream: "", vendor: "", protocol: "" });
  const [userFilters, setUserFilters] = useState<UserFilters>({ subscriptionStatus: "", provider: "" });
  const [ruleDraft, setRuleDraft] = useState<GroupRule>({ type: "vendor", value: "" });

  // Opening another group resets every picker and reloads its data in parallel.
  useEffect(() => {
    if (target === null) return;
    const controller = new AbortController();
    const g = target === "new" ? null : target;
    setName(g?.name ?? "");
    setDescription(g?.description ?? "");
    setFactor(g ? factorFromPercent(g.rateMultiplierPercent) : "1");
    setModelIds([]);
    setRules([]);
    setUserIds([]);
    setError(null);
    setNeedName(false);
    setPickModels(false);
    setPickUsers(false);
    setModelFilters({ upstream: "", vendor: "", protocol: "" });
    setUserFilters({ subscriptionStatus: "", provider: "" });
    setRuleDraft({ type: "vendor", value: "" });
    setLoading(true);
    setAssignFailed(false);
    Promise.all([
      g ? getGroupModels(g.id, controller.signal) : Promise.resolve({ modelIDs: [] as number[], rules: [] as GroupRule[] }),
      g && !g.isDefault ? getGroupUsers(g.id, controller.signal) : Promise.resolve({ userIDs: [] as number[] }),
      listAllUpstreams(controller.signal).catch(() => [] as Upstream[]),
      listVendors(controller.signal).catch(() => [] as Vendor[]),
      listProviders(controller.signal).catch(() => [] as IdentityProvider[]),
    ]).then(
      ([models, users, ups, vens, provs]) => {
        setModelIds(models.modelIDs ?? []);
        setRules(models.rules ?? []);
        setUserIds(users.userIDs ?? []);
        setUpstreams([...ups].sort((a, b) => a.name.localeCompare(b.name)));
        setVendors(vens);
        setProviders(provs);
        setLoading(false);
      },
      (e: unknown) => {
        if (isAbortError(e)) return;
        setLoading(false);
        // Saving without the loaded assignments would replace them with empty lists.
        setAssignFailed(true);
        setError(message(e));
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, attempt]);

  const save = async () => {
    if (busy || (group && (loading || assignFailed))) return;
    if (!name.trim()) {
      setNeedName(true);
      return;
    }
    setBusy(true);
    setError(null);
    const done = { group: false, models: false, users: false };
    try {
      const body = { name: name.trim(), description: description.trim(), rateMultiplierPercent: percentFromFactor(factor) };
      if (!group) {
        await createGroup(body);
        toast.success(t("adminGroups.toast.created"));
        onChanged(true);
        onOpenChange(false);
        return;
      }
      await patchGroup(group.id, body);
      done.group = true;
      await putGroupModels(group.id, modelIds, rules);
      done.models = true;
      if (!isDefault) await putGroupUsers(group.id, userIds);
      done.users = true;
      invalidateModels();
      toast.success(t("adminGroups.toast.saved"));
      onChanged(true);
      onOpenChange(false);
    } catch (e) {
      const after = refreshAfter(done);
      if (after.invalidateCache) invalidateModels();
      if (after.reloadList) onChanged(true);
      setError(message(e));
      toast.error(t("adminGroups.toast.saveFailed"), { description: message(e) });
    } finally {
      setBusy(false);
    }
  };

  const ruleLabel = (r: GroupRule): string => {
    if (r.type === "all") return t("adminGroups.rules.allLabel");
    if (r.type === "vendor") return `${t("adminGroups.rules.vendor")}: ${vendors.find((v) => v.key === r.value)?.name ?? r.value}`;
    if (r.type === "protocol") return `${t("adminGroups.rules.protocol")}: ${protocolLabel(r.value)}`;
    return `${t("adminGroups.rules.upstream")}: ${upstreams.find((u) => String(u.id) === r.value)?.name ?? r.value}`;
  };

  const ruleValueInput =
    ruleDraft.type === "vendor" ? (
      <OptionSelect value={ruleDraft.value} onChange={(v) => setRuleDraft({ ...ruleDraft, value: v })} label={t("adminGroups.rules.vendor")} placeholder={t("adminGroups.rules.choose")} options={vendors.map((v) => ({ value: v.key, label: v.name }))} className="w-full sm:w-56" />
    ) : ruleDraft.type === "protocol" ? (
      <OptionSelect value={ruleDraft.value} onChange={(v) => setRuleDraft({ ...ruleDraft, value: v })} label={t("adminGroups.rules.protocol")} placeholder={t("adminGroups.rules.choose")} options={PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p) }))} className="w-full sm:w-64" />
    ) : ruleDraft.type === "upstream" ? (
      <OptionSelect value={ruleDraft.value} onChange={(v) => setRuleDraft({ ...ruleDraft, value: v })} label={t("adminGroups.rules.upstream")} placeholder={t("adminGroups.rules.choose")} options={upstreams.map((u) => ({ value: String(u.id), label: u.name }))} className="w-full sm:w-56" />
    ) : null;

  const modelFetch = useMemo(
    () => async (args: { page: number; pageSize: number; q: string; filters: ModelFilters; signal: AbortSignal }) => listModels({ page: args.page, pageSize: args.pageSize, q: args.q, status: "", sort: "sortOrder_asc", ...args.filters }, args.signal),
    [],
  );
  const allModelIds = useMemo(
    () => async (args: { q: string; filters: ModelFilters }) => (await loadAllPages<PlatformModel>((page, size) => listModels({ page, pageSize: size, q: args.q, status: "", sort: "sortOrder_asc", ...args.filters }))).map((m) => m.id),
    [],
  );
  const userFetch = useMemo(
    () => async (args: { page: number; pageSize: number; q: string; filters: UserFilters; signal: AbortSignal }) =>
      readPage<UserRecord>(await api.get<unknown>("/admin/users", { signal: args.signal, query: { page: args.page, page_size: args.pageSize, q: args.q.trim(), subscription_status: args.filters.subscriptionStatus, identity_provider: args.filters.provider } })),
    [],
  );
  const allUserIds = useMemo(
    () => async (args: { q: string; filters: UserFilters }) =>
      (
        await loadAllPages<UserRecord>(async (page, size) => readPage<UserRecord>(await api.get<unknown>("/admin/users", { query: { page, page_size: size, q: args.q.trim(), subscription_status: args.filters.subscriptionStatus, identity_provider: args.filters.provider } })))
      ).map((u) => u.id),
    [],
  );

  return (
    <>
      <Sheet
        open={target !== null}
        onOpenChange={onOpenChange}
        busy={busy}
        size="lg"
        title={group ? t("adminGroups.form.editTitle", { name: group.name }) : t("adminGroups.form.createTitle")}
        footer={
          <>
            <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button loading={busy} disabled={loading || (group !== null && assignFailed)} onClick={save}>
              {t("common.actions.save")}
            </Button>
          </>
        }
      >
        <div className="grid gap-6">
          <div className="grid gap-4">
            <Field required invalid={needName}>
              <FieldLabel>{t("adminGroups.form.name")}</FieldLabel>
              <Input value={name} maxLength={64} onChange={(e) => setName(e.target.value)} autoComplete="off" />
              <FieldError>{needName ? t("adminGroups.form.nameRequired") : null}</FieldError>
            </Field>
            <Field>
              <FieldLabel>{t("adminGroups.form.description")}</FieldLabel>
              <Textarea rows={2} value={description} maxLength={255} onChange={(e) => setDescription(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("adminGroups.form.multiplier")}</FieldLabel>
              <Input value={factor} inputMode="decimal" onChange={(e) => setFactor(e.target.value)} className="w-32" />
              <FieldHint>{t("adminGroups.form.multiplierHint")}</FieldHint>
            </Field>
          </div>

          {group ? (
            assignFailed && !loading ? (
              <LoadError message={t("adminGroups.toast.loadFailed")} onRetry={() => setAttempt((n) => n + 1)} />
            ) : loading ? (
              <div className="space-y-2">
                <Skeleton className="h-24 w-full" />
                <Skeleton className="h-24 w-full" />
              </div>
            ) : (
              <>
                <section className="space-y-3 rounded-lg border p-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div>
                      <h3 className="text-sm font-semibold">{t("adminGroups.models.title")}</h3>
                      <p className="text-[13px] text-muted-foreground">{t("adminGroups.models.chosen", { count: modelIds.length })}</p>
                    </div>
                    <Button size="sm" variant="outline" onClick={() => setPickModels(true)}>
                      {t("adminGroups.models.choose")}
                    </Button>
                  </div>
                  <div className="space-y-2">
                    <h4 className="text-[13px] font-medium">{t("adminGroups.rules.title")}</h4>
                    <p className="text-[13px] text-muted-foreground">{t("adminGroups.rules.hint")}</p>
                    <ul className="flex flex-wrap gap-2">
                      {rules.map((r) => (
                        <li key={ruleKey(r)}>
                          <Badge tone="primary" className="gap-1 py-1 pl-2.5 pr-1">
                            {ruleLabel(r)}
                            <button type="button" aria-label={`${t("adminGroups.rules.remove")}: ${ruleLabel(r)}`} onClick={() => setRules((list) => removeRule(list, r))} className="rounded-full p-0.5 hover:bg-primary/20">
                              <X className="size-3" />
                            </button>
                          </Badge>
                        </li>
                      ))}
                    </ul>
                    <div className="flex flex-wrap items-center gap-2">
                      <OptionSelect
                        value={ruleDraft.type}
                        onChange={(v) => setRuleDraft({ type: v, value: "" })}
                        label={t("adminGroups.rules.type")}
                        options={RULE_TYPES.map((x) => ({ value: x, label: x === "all" ? t("adminGroups.rules.all") : t(`adminGroups.rules.${x}`) }))}
                        className="w-full sm:w-40"
                      />
                      {ruleValueInput}
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={!canAddRule(rules, ruleDraft)}
                        onClick={() => {
                          setRules((list) => addRule(list, ruleDraft));
                          setRuleDraft({ type: ruleDraft.type, value: "" });
                        }}
                      >
                        <Plus />
                        {t("adminGroups.rules.add")}
                      </Button>
                    </div>
                  </div>
                </section>

                <section className="space-y-2 rounded-lg border p-4">
                  <h3 className="text-sm font-semibold">{t("adminGroups.users.title")}</h3>
                  {isDefault ? (
                    <p className="text-[13px] text-muted-foreground">{t("adminGroups.users.defaultNote")}</p>
                  ) : (
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <p className="text-[13px] text-muted-foreground">{t("adminGroups.users.chosen", { count: userIds.length })}</p>
                      <Button size="sm" variant="outline" onClick={() => setPickUsers(true)}>
                        {t("adminGroups.users.choose")}
                      </Button>
                    </div>
                  )}
                </section>
              </>
            )
          ) : null}
          <FieldError>{error}</FieldError>
        </div>
      </Sheet>

      <IdPicker<PlatformModel, ModelFilters>
        open={pickModels}
        onOpenChange={setPickModels}
        title={t("adminGroups.models.pickerTitle")}
        description={t("adminGroups.models.pickerBody")}
        value={modelIds}
        onApply={setModelIds}
        filters={modelFilters}
        onFilters={setModelFilters}
        searchPlaceholder={t("adminGroups.models.search")}
        filterBar={
          <>
            <OptionSelect value={modelFilters.upstream} onChange={(v) => setModelFilters({ ...modelFilters, upstream: v })} label={t("adminGroups.rules.upstream")} options={[{ value: "", label: t("adminGroups.models.anyUpstream") }, ...upstreams.map((u) => ({ value: String(u.id), label: u.name }))]} />
            <OptionSelect value={modelFilters.vendor} onChange={(v) => setModelFilters({ ...modelFilters, vendor: v })} label={t("adminGroups.rules.vendor")} options={[{ value: "", label: t("adminGroups.models.anyVendor") }, ...vendors.map((v) => ({ value: v.key, label: v.name }))]} />
            <OptionSelect value={modelFilters.protocol} onChange={(v) => setModelFilters({ ...modelFilters, protocol: v })} label={t("adminGroups.rules.protocol")} options={[{ value: "", label: t("adminGroups.models.anyProtocol") }, ...PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p) }))]} />
          </>
        }
        fetchPage={modelFetch}
        fetchAllIds={allModelIds}
        rowId={(m) => m.id}
        renderRow={(m) => (
          <span className="flex items-center gap-2">
            <ModelIcon icon={m.icon || m.vendorIcon} name={m.platformModelName} size={20} />
            <span className="truncate font-medium">{m.platformModelName}</span>
            <span className="truncate text-xs text-muted-foreground">{m.vendorName}</span>
          </span>
        )}
      />
      <IdPicker<UserRecord, UserFilters>
        open={pickUsers}
        onOpenChange={setPickUsers}
        title={t("adminGroups.users.pickerTitle")}
        description={t("adminGroups.users.pickerBody")}
        value={userIds}
        onApply={setUserIds}
        filters={userFilters}
        onFilters={setUserFilters}
        searchPlaceholder={t("adminGroups.users.search")}
        filterBar={
          <>
            <OptionSelect value={userFilters.subscriptionStatus} onChange={(v) => setUserFilters({ ...userFilters, subscriptionStatus: v })} label={t("adminUsers.filters.subscriptionStatus")} options={[{ value: "", label: t("adminUsers.filters.anySubscription") }, ...SUBSCRIPTION_STATUSES.map((s) => ({ value: s, label: t(`adminUsers.subscriptionStatuses.${s}`) }))]} />
            {providers.length > 0 ? <OptionSelect value={userFilters.provider} onChange={(v) => setUserFilters({ ...userFilters, provider: v })} label={t("adminUsers.filters.provider")} options={[{ value: "", label: t("adminUsers.filters.anyProvider") }, ...providers.map((p) => ({ value: p.slug, label: p.name }))]} /> : null}
          </>
        }
        fetchPage={userFetch}
        fetchAllIds={allUserIds}
        rowId={(u) => u.id}
        renderRow={(u) => (
          <span className="flex items-center gap-2">
            <span className="truncate font-medium">{u.displayName || u.username}</span>
            <span className="truncate text-xs text-muted-foreground">{u.email || `@${u.username}`}</span>
          </span>
        )}
      />
    </>
  );
}
