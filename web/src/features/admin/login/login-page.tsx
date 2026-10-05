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
