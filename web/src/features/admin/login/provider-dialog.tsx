"use client";

import { ChevronRight } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { CopyButton } from "../components/copy-button";
import { ModelIcon } from "../components/model-icon";
import { Mono } from "../components/page";
import { OptionSelect } from "../components/toolbar";
import { useSnapshot } from "../lib/use-snapshot";
import { callbackUrl, providerSlug, switchType, switchUrlMode, validateProviderForm, withLoginEnabled, type IdentityProvider, type ProviderForm, type ProviderProblem } from "./rules";

type Target = { provider: IdentityProvider | null; form: ProviderForm } | null;

export function ProviderDialog({ target, callbackBase, onOpenChange, onSubmit }: { target: Target; callbackBase: string; onOpenChange: (open: boolean) => void; onSubmit: (provider: IdentityProvider | null, form: ProviderForm) => Promise<void> }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(target);
  const provider = shown?.provider ?? null;
  const [form, setForm] = useState<ProviderForm | null>(null);
  const [problem, setProblem] = useState<ProviderProblem | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [advanced, setAdvanced] = useState(false);

  useEffect(() => {
    if (target) {
      setForm(target.form);
      setProblem(null);
      setError(null);
      setAdvanced(false);
    }
  }, [target]);

  if (!shown || !form) return <Dialog open={false} onOpenChange={() => undefined} />;
  const set = <K extends keyof ProviderForm>(key: K, value: ProviderForm[K]) => setForm((f) => (f ? { ...f, [key]: value } : f));
  const callback = callbackUrl(callbackBase, form);
  const slug = providerSlug(form);

  const submit = async () => {
    if (busy) return;
    const found = validateProviderForm(form, provider !== null);
    setProblem(found);
    if (found) return;
    setBusy(true);
    setError(null);
    try {
      await onSubmit(provider, form);
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={target !== null} onOpenChange={(open) => !busy && onOpenChange(open)}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{provider ? t("adminLogin.provider.editTitle", { name: provider.name }) : t("adminLogin.provider.createTitle")}</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("adminLogin.provider.type")}</FieldLabel>
              <OptionSelect
                value={form.type}
                onChange={(v) => setForm(switchType(form, v === "oauth2" ? "oauth2" : "oidc"))}
                options={[
                  { value: "oidc", label: t("adminLogin.providers.types.oidc") },
                  { value: "oauth2", label: t("adminLogin.providers.types.oauth2") },
                ]}
                className="w-full"
              />
            </Field>
            <Field required invalid={problem === "name"}>
              <FieldLabel>{t("adminLogin.provider.name")}</FieldLabel>
              <Input value={form.name} onChange={(e) => set("name", e.target.value)} autoComplete="off" />
              <FieldError>{problem === "name" ? t("adminLogin.provider.problems.name") : null}</FieldError>
            </Field>
            <Field>
              <FieldLabel>{t("adminLogin.provider.slug")}</FieldLabel>
              <Input value={form.slug} onChange={(e) => set("slug", e.target.value)} placeholder={providerSlug({ slug: "", name: form.name })} autoComplete="off" spellCheck={false} />
              <FieldHint>{t("adminLogin.provider.slugHint")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("adminLogin.provider.logo")}</FieldLabel>
              <div className="flex items-center gap-2">
                <ModelIcon icon={form.logoURL} name={form.name} size={32} />
                <Input value={form.logoURL} onChange={(e) => set("logoURL", e.target.value)} autoComplete="off" spellCheck={false} />
              </div>
              <FieldHint>{t("adminLogin.provider.logoHint")}</FieldHint>
            </Field>
          </div>

          <div className="flex flex-wrap gap-6">
            <label className="flex items-center gap-3 text-sm font-medium">
              <Switch checked={form.loginEnabled} onCheckedChange={(on) => setForm(withLoginEnabled(form, on))} />
              {t("adminLogin.provider.loginEnabled")}
            </label>
            <label className="flex items-center gap-3 text-sm font-medium">
              <Switch checked={form.registrationEnabled} disabled={!form.loginEnabled} onCheckedChange={(on) => set("registrationEnabled", on)} />
              {t("adminLogin.provider.registrationEnabled")}
            </label>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <Field required invalid={problem === "clientId"}>
              <FieldLabel>{t("adminLogin.provider.clientId")}</FieldLabel>
              <Input value={form.clientID} onChange={(e) => set("clientID", e.target.value)} autoComplete="off" spellCheck={false} />
              <FieldError>{problem === "clientId" ? t("adminLogin.provider.problems.clientId") : null}</FieldError>
            </Field>
            <Field required={provider === null} invalid={problem === "clientSecret"}>
              <FieldLabel>{t("adminLogin.provider.clientSecret")}</FieldLabel>
              <Input type="password" value={form.clientSecret} onChange={(e) => set("clientSecret", e.target.value)} autoComplete="new-password" placeholder={provider ? t("adminCommon.settings.configured") : undefined} />
              {provider ? <FieldHint>{t("adminLogin.provider.secretKeep")}</FieldHint> : null}
              <FieldError>{problem === "clientSecret" ? t("adminLogin.provider.problems.clientSecret") : null}</FieldError>
            </Field>
          </div>

          {form.type === "oidc" ? (
            <Field invalid={problem === "issuer"}>
              <FieldLabel>{t("adminLogin.provider.urlMode")}</FieldLabel>
              <div className="flex flex-col gap-2 sm:flex-row">
                <OptionSelect
                  value={form.urlMode}
                  onChange={(v) => setForm(switchUrlMode(form, v === "issuer" ? "issuer" : "discovery"))}
                  options={[
                    { value: "discovery", label: t("adminLogin.provider.discovery") },
                    { value: "issuer", label: t("adminLogin.provider.issuer") },
                  ]}
                  className="sm:w-48"
                />
                <Input
                  className="flex-1"
                  value={form.urlMode === "issuer" ? form.issuerURL : form.discoveryURL}
                  onChange={(e) => set(form.urlMode === "issuer" ? "issuerURL" : "discoveryURL", e.target.value)}
                  placeholder="https://"
                  spellCheck={false}
                  aria-label={form.urlMode === "issuer" ? t("adminLogin.provider.issuer") : t("adminLogin.provider.discovery")}
                />
              </div>
              <FieldError>{problem === "issuer" ? t("adminLogin.provider.problems.issuer") : null}</FieldError>
            </Field>
          ) : (
            <div className="grid gap-4">
              <Field invalid={problem === "oauthUrls"}>
                <FieldLabel>{t("adminLogin.provider.authUrl")}</FieldLabel>
                <Input value={form.authURL} onChange={(e) => set("authURL", e.target.value)} placeholder="https://" spellCheck={false} />
              </Field>
              <Field invalid={problem === "oauthUrls"}>
                <FieldLabel>{t("adminLogin.provider.tokenUrl")}</FieldLabel>
                <Input value={form.tokenURL} onChange={(e) => set("tokenURL", e.target.value)} placeholder="https://" spellCheck={false} />
              </Field>
              <Field invalid={problem === "oauthUrls"}>
                <FieldLabel>{t("adminLogin.provider.userinfoUrl")}</FieldLabel>
                <Input value={form.userinfoURL} onChange={(e) => set("userinfoURL", e.target.value)} placeholder="https://" spellCheck={false} />
                <FieldError>{problem === "oauthUrls" ? t("adminLogin.provider.problems.oauthUrls") : null}</FieldError>
              </Field>
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("adminLogin.provider.scopes")}</FieldLabel>
              <Input value={form.scopes} onChange={(e) => set("scopes", e.target.value)} spellCheck={false} />
            </Field>
            <Field>
              <FieldLabel>{t("adminLogin.provider.defaultRole")}</FieldLabel>
              <OptionSelect
                value={form.defaultRole}
                onChange={(v) => set("defaultRole", v)}
                options={[
                  { value: "user", label: t("adminLogin.provider.roles.user") },
                  { value: "admin", label: t("adminLogin.provider.roles.admin") },
                ]}
                className="w-full"
              />
            </Field>
          </div>

          <div className="space-y-1.5 rounded-lg border bg-muted/40 p-3">
            <div className="text-sm font-medium">{t("adminLogin.provider.callback")}</div>
            {callback ? (
              <div className="flex items-center gap-1">
                <Mono className="min-w-0 flex-1 break-all">{callback}</Mono>
                <CopyButton text={callback} />
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">{t("adminLogin.provider.callbackUnavailable")}</p>
            )}
            <p className="text-[13px] text-muted-foreground">
              {t("adminLogin.provider.callbackHint")} <span className="font-mono">{slug}</span>
            </p>
          </div>

          <div>
            <button type="button" aria-expanded={advanced} onClick={() => setAdvanced((v) => !v)} className="flex items-center gap-1 text-sm font-medium">
              <ChevronRight className={`size-4 transition-transform ${advanced ? "rotate-90" : ""}`} />
              {t("adminLogin.provider.advanced")}
            </button>
            {advanced ? (
              <div className="mt-3 space-y-3">
                <p className="text-[13px] text-muted-foreground">{t("adminLogin.provider.advancedHint")}</p>
                <div className="grid gap-3 sm:grid-cols-2">
                  {(
                    [
                      ["subjectField", "adminLogin.provider.subjectField"],
                      ["emailField", "adminLogin.provider.emailField"],
                      ["emailVerifiedField", "adminLogin.provider.emailVerifiedField"],
                      ["nameField", "adminLogin.provider.nameField"],
                      ["avatarField", "adminLogin.provider.avatarField"],
                    ] as const
                  ).map(([key, label]) => (
                    <Field key={key}>
                      <FieldLabel>{t(label)}</FieldLabel>
                      <Input value={form[key]} onChange={(e) => set(key, e.target.value)} spellCheck={false} />
                    </Field>
                  ))}
                  {form.type === "oidc" ? (
                    <Field>
                      <FieldLabel>{t("adminLogin.provider.jwksUrl")}</FieldLabel>
                      <Input value={form.jwksURL} onChange={(e) => set("jwksURL", e.target.value)} spellCheck={false} />
                    </Field>
                  ) : null}
                </div>
              </div>
            ) : null}
          </div>

          <FieldError>{error}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
