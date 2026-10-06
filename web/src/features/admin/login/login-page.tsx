"use client";

import { ChevronDown, Pencil, Plus, Trash2, TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { ApiError, isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { LoadError } from "../components/load-error";
import { AdminPage, Section } from "../components/page";
import { AdminSection } from "../components/section-guard";
import { SettingsGroup } from "../components/settings-group";
import { ModelIcon } from "../components/model-icon";
import { SortableList } from "../components/sortable-list";
import { problemText } from "../lib/problem";
import { type Draft, type FieldDef } from "../lib/settings-form";
import { useNotify } from "../lib/use-notify";
import { useSettingsForm } from "../lib/use-settings-form";
import { createProvider, deleteProvider, listProviders, loadBridge, reorderProviders, updateProvider, type LoginBridge } from "./api";
import { ProviderDialog } from "./provider-dialog";
import {
  A,
  applyFlag,
  blankProviderForm,
  formFromProvider,
  formFromTemplate,
  loginChangeProblem,
  normalizeAuth,
  PROVIDER_TEMPLATES,
  providerBody,
  validateLoginMethods,
  validateNextPath,
  validateTurnstile,
  validateVerification,
  type IdentityProvider,
  type ProviderForm,
} from "./rules";

const NS = "auth";
const f = (key: string, type: FieldDef["type"], field: string, extra: Partial<FieldDef> = {}): FieldDef => ({
  ns: NS,
  key,
  type,
  label: `adminLogin.fields.${field}.label` as FieldDef["label"],
  hint: `adminLogin.fields.${field}.hint` as FieldDef["hint"],
  ...extra,
});
const on = (id: string) => (d: Draft) => d[id] === "true";

const PAGE_FIELDS: FieldDef[] = [f("login_default_next_path", "string", "nextPath", { def: "/chat", blankDefault: true })];
const METHOD_FIELDS: FieldDef[] = [
  f("email_login_enabled", "bool", "emailLogin", { def: "true" }),
  f("email_registration_enabled", "bool", "emailRegistration", { def: "true" }),
  f("password_reset_enabled", "bool", "passwordReset", { def: "false" }),
  f("username_login_enabled", "bool", "usernameLogin", { def: "true" }),
  f("third_party_login_enabled", "bool", "thirdParty", { def: "true" }),
];
const HUMAN_FIELDS: FieldDef[] = [
  f("turnstile_registration_enabled", "bool", "turnstileOn", { def: "false" }),
  f("turnstile_site_key", "string", "turnstileSite", { def: "", visible: on(A.turnstileOn) }),
  f("turnstile_secret_key", "password", "turnstileSecret", { secret: true, visible: on(A.turnstileOn) }),
];
const BINDING_FIELDS: FieldDef[] = [
  f("email_verification_enabled", "bool", "verification", { def: "false" }),
  f("smtp_host", "string", "smtpHost", { def: "", visible: on(A.verification) }),
  f("smtp_port", "int", "smtpPort", { def: "587", blankDefault: true, visible: on(A.verification) }),
  f("smtp_username", "string", "smtpUser", { def: "", visible: on(A.verification) }),
  f("smtp_password", "password", "smtpPassword", { secret: true, visible: on(A.verification) }),
  f("smtp_from", "string", "smtpFrom", { def: "", visible: on(A.verification) }),
  f("email_registration_allowed_domains", "textarea", "allowedDomains", { def: "" }),
  f("email_registration_block_plus_alias", "bool", "blockPlus", { def: "false" }),
  f("auto_link_verified_email", "bool", "autoLink", { def: "true" }),
];
const SECURITY_FIELDS: FieldDef[] = [
  f("token_ttl_hours", "int", "tokenTtl", { def: "24", blankDefault: true }),
  f("refresh_token_ttl_hours", "int", "refreshTtl", { def: "720", blankDefault: true }),
  f("login_max_failures", "int", "maxFailures", { def: "5", blankDefault: true }),
  f("login_lock_minutes", "int", "lockMinutes", { def: "15", blankDefault: true }),
  f("rate_limit_enabled", "bool", "rateLimit", { def: "false" }),
  f("rate_limit_rpm", "int", "rateRpm", { def: "60", blankDefault: true, visible: on(A.rateLimit) }),
  f("public_auth_rate_limit_rpm", "int", "publicRpm", { def: "30", blankDefault: true, visible: on(A.rateLimit) }),
];
const ALL_FIELDS = [...PAGE_FIELDS, ...METHOD_FIELDS, ...HUMAN_FIELDS, ...BINDING_FIELDS, ...SECURITY_FIELDS];

/** Switches that need their parent switch on. */
const NEEDS: Record<string, string> = {
  [A.emailRegistration]: A.emailLogin,
  [A.turnstileOn]: A.emailRegistration,
  [A.passwordReset]: A.verification,
};

export function AdminLoginPage() {
  return (
    <AdminSection feature="identityProviders">
      <LoginContent />
    </AdminSection>
  );
}

function LoginContent() {
  const t = useT();
  const form = useSettingsForm({ fields: ALL_FIELDS, normalize: (next) => normalizeAuth(next) });
  const smtpLabel = (name: "host" | "port" | "username" | "password") => t(`adminLogin.smtp.${name}`);

  const guarded = useCallback(
    (id: string, value: string) => {
      const found = loginChangeProblem(form.draft, id, value);
      if (found) {
        toast.error(t("adminLogin.groups.methods.title"), { description: problemText(t, found) });
        return;
      }
      form.set(id, value);
    },
    [form, t],
  );
  const parentOff = (field: FieldDef, draft: Draft) => {
    const parent = NEEDS[`${field.ns}.${field.key}`];
    return parent !== undefined && draft[parent] !== "true";
  };
  const group = (key: "page" | "methods" | "human" | "binding" | "security") => ({ title: t(`adminLogin.groups.${key}.title`), description: t(`adminLogin.groups.${key}.description`) });

  return (
    <AdminPage title={t("adminLogin.title")} description={t("adminLogin.description")}>
      <SettingsGroup form={form} fields={PAGE_FIELDS} {...group("page")} save={{ validate: (d) => validateNextPath(d[A.nextPath] ?? "") }} />
      <SettingsGroup
        form={form}
        fields={METHOD_FIELDS}
        {...group("methods")}
        onChange={guarded}
        fieldDisabled={parentOff}
        save={{ validate: (d) => validateLoginMethods(d) }}
      />
      <SettingsGroup form={form} fields={HUMAN_FIELDS} {...group("human")} fieldDisabled={() => form.draft[A.emailRegistration] !== "true"} save={{ validate: (d, configured) => validateTurnstile(d, configured) }} />
      <SettingsGroup form={form} fields={BINDING_FIELDS} {...group("binding")} save={{ validate: (d, configured) => validateVerification(d, configured, smtpLabel) }} />
      <SettingsGroup form={form} fields={SECURITY_FIELDS} {...group("security")} />
      <ProvidersSection />
    </AdminPage>
  );
}

function ProvidersSection() {
  const t = useT();
  const message = useErrorMessage();
  const notify = useNotify();
  const [providers, setProviders] = useState<IdentityProvider[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [bridge, setBridge] = useState<LoginBridge | null>(null);
  const [editing, setEditing] = useState<{ provider: IdentityProvider | null; form: ProviderForm } | null>(null);
  const [removing, setRemoving] = useState<IdentityProvider | null>(null);
  const [forcing, setForcing] = useState<{ provider: IdentityProvider; message: string } | null>(null);
  const [busyIds, setBusyIds] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setFailed(false);
    Promise.all([listProviders(controller.signal), loadBridge(controller.signal).catch(() => null)]).then(
      ([rows, info]) => {
        setProviders(rows);
        setBridge(info);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
        notify.error("adminLogin.providers.loadFailed", error);
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt]);

  const bridgeMissing = providers.length > 0 && bridge !== null && !bridge.enabled;

  const reorder = async (next: IdentityProvider[]) => {
    const before = providers;
    setProviders(next);
    try {
      await reorderProviders(next.map((p) => p.publicID));
    } catch (error) {
      setProviders(before);
      notify.error("adminLogin.providers.reorderFailed", error);
    }
  };

  const setFlag = async (provider: IdentityProvider, flag: "login" | "registration", value: boolean) => {
    if (busyIds.has(provider.publicID)) return;
    const result = applyFlag(formFromProvider(provider), flag, value);
    if (!result.ok) {
      toast.error(t("adminLogin.providers.flagFailed"), { description: t("adminLogin.providers.loginFirst") });
      return;
    }
    const before = provider;
    setBusyIds((s) => new Set(s).add(provider.publicID));
    setProviders((list) => list.map((p) => (p.publicID === provider.publicID ? { ...p, loginEnabled: result.form.loginEnabled, registrationEnabled: result.form.registrationEnabled } : p)));
    try {
      const saved = await updateProvider(provider.publicID, providerBody(result.form));
      setProviders((list) => list.map((p) => (p.publicID === saved.publicID ? saved : p)));
    } catch (error) {
      setProviders((list) => list.map((p) => (p.publicID === before.publicID ? before : p)));
      notify.error("adminLogin.providers.flagFailed", error);
    } finally {
      setBusyIds((s) => {
        const next = new Set(s);
        next.delete(provider.publicID);
        return next;
      });
    }
  };

  const remove = async (provider: IdentityProvider, force: boolean) => {
    try {
      await deleteProvider(provider.publicID, force);
    } catch (error) {
      if (!force && error instanceof ApiError && error.status === 409) {
        setForcing({ provider, message: message(error) });
        return;
      }
      notify.error("adminLogin.delete.failed", error);
      throw error;
    }
    setProviders((list) => list.filter((p) => p.publicID !== provider.publicID));
    notify.success("adminLogin.delete.done");
  };

  const templateItems = useMemo(() => PROVIDER_TEMPLATES, []);

  return (
    <Section
      title={t("adminLogin.providers.title")}
      description={t("adminLogin.providers.description")}
      actions={
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm">
              <Plus />
              {t("adminLogin.providers.add")}
              <ChevronDown />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent className="max-h-80 overflow-auto">
            <DropdownMenuItem onSelect={() => setEditing({ provider: null, form: blankProviderForm("oidc") })}>{t("adminLogin.providers.customOidc")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setEditing({ provider: null, form: blankProviderForm("oauth2") })}>{t("adminLogin.providers.customOauth2")}</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>{t("adminLogin.providers.fromTemplate")}</DropdownMenuLabel>
            {templateItems.map((tpl) => (
              <DropdownMenuItem key={tpl.id} onSelect={() => setEditing({ provider: null, form: formFromTemplate(tpl) })}>
                {tpl.name}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      }
    >
      {bridgeMissing ? (
        <div role="alert" className="mb-4 flex items-start gap-2 rounded-lg border border-warning/50 bg-warning/10 px-3 py-2 text-sm">
          <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" />
          {t("adminLogin.providers.bridgeMissing")}
        </div>
      ) : null}
      {loading ? (
        <div className="space-y-2" aria-busy>
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : failed ? (
        <LoadError message={t("adminLogin.providers.loadFailed")} onRetry={() => setAttempt((n) => n + 1)} />
      ) : providers.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">{t("adminLogin.providers.empty")}</p>
      ) : (
        <SortableList
          items={providers}
          itemKey={(p) => p.publicID}
          onChange={reorder}
          disabled={providers.length < 2}
          label={t("adminLogin.providers.title")}
          itemLabel={(p) => p.name}
          render={(p) => (
            <div className="flex flex-wrap items-center gap-3">
              <ModelIcon icon={p.logoURL} name={p.name} size={28} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{p.name}</span>
                  <Badge tone="outline">{t(`adminLogin.providers.types.${p.type === "oauth2" ? "oauth2" : "oidc"}`)}</Badge>
                </div>
                <div className="truncate font-mono text-xs text-muted-foreground">{p.slug}</div>
              </div>
              <label className="flex items-center gap-2 text-[13px]" onClick={(e) => e.stopPropagation()}>
                {t("adminLogin.providers.columns.login")}
                <Switch checked={p.loginEnabled} disabled={busyIds.has(p.publicID)} aria-label={`${t("adminLogin.providers.columns.login")}: ${p.name}`} onCheckedChange={(v) => void setFlag(p, "login", v)} />
              </label>
              <label className="flex items-center gap-2 text-[13px]" onClick={(e) => e.stopPropagation()}>
                {t("adminLogin.providers.columns.registration")}
                <Switch checked={p.registrationEnabled} disabled={busyIds.has(p.publicID)} aria-label={`${t("adminLogin.providers.columns.registration")}: ${p.name}`} onCheckedChange={(v) => void setFlag(p, "registration", v)} />
              </label>
              <span className="flex items-center" onClick={(e) => e.stopPropagation()}>
                <Tooltip label={t("adminLogin.providers.actions.edit")}>
                  <Button variant="ghost" size="icon-sm" aria-label={`${t("adminLogin.providers.actions.edit")}: ${p.name}`} onClick={() => setEditing({ provider: p, form: formFromProvider(p) })}>
                    <Pencil />
                  </Button>
                </Tooltip>
                <Tooltip label={t("adminLogin.providers.actions.delete")}>
                  <Button variant="ghost" size="icon-sm" aria-label={`${t("adminLogin.providers.actions.delete")}: ${p.name}`} onClick={() => setRemoving(p)}>
                    <Trash2 className="text-destructive" />
                  </Button>
                </Tooltip>
              </span>
            </div>
          )}
        />
      )}

      <ProviderDialog
        target={editing}
        callbackBase={bridge?.callbackBaseURL ?? ""}
        onOpenChange={(open) => !open && setEditing(null)}
        onSubmit={async (provider, form) => {
          if (provider) {
            const saved = await updateProvider(provider.publicID, providerBody(form));
            setProviders((list) => list.map((p) => (p.publicID === saved.publicID ? saved : p)));
            notify.success("adminLogin.provider.updated");
          } else {
            const saved = await createProvider(providerBody(form));
            setProviders((list) => [...list, saved]);
            notify.success("adminLogin.provider.created");
          }
        }}
      />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminLogin.delete.title", { name: removing?.name ?? "" })}
        description={t("adminLogin.delete.body")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          if (removing) await remove(removing, false);
        }}
      />
      <ConfirmDialog
        open={forcing !== null}
        onOpenChange={(open) => !open && setForcing(null)}
        title={t("adminLogin.delete.forceTitle", { name: forcing?.provider.name ?? "" })}
        description={t("adminLogin.delete.forceBody", { message: forcing?.message ?? "" })}
        confirmLabel={t("adminLogin.delete.forceAction")}
        destructive
        onConfirm={async () => {
          if (forcing) await remove(forcing.provider, true);
        }}
      />
    </Section>
  );
}
