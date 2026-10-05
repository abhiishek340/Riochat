"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { normalizeServerUrl, resolveApiBase, setApiBaseOverride } from "@/lib/api/base-url";
import { isDesktop } from "@/lib/api/platform";
import { useT } from "@/lib/i18n/provider";
import { SettingsPage, SettingsSection } from "../parts";

/**
 * Desktop only: the server address the app talks to. In a browser the page
 * sends the visitor back to General.
 */
export function NetworkSettingsPage() {
  const t = useT();
  const router = useRouter();
  usePageTitle(t("settings.title"));
  const [hydrated, setHydrated] = useState(false);
  const [desktop, setDesktop] = useState(false);
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    const d = isDesktop();
    setDesktop(d);
    setAddress(resolveApiBase());
    setHydrated(true);
    if (!d) router.replace("/settings/general");
  }, [router]);

  if (!hydrated || !desktop) return null;

  const save = (e: FormEvent) => {
    e.preventDefault();
    const normalized = normalizeServerUrl(address);
    if (!normalized) {
      setError(t("settings.network.invalid"));
      return;
    }
    setError("");
    setApiBaseOverride(() => normalized);
    setAddress(normalized);
    toast.success(t("settings.network.saved"));
  };

  return (
    <SettingsPage title={t("settings.nav.network")} description={t("settings.network.description")}>
      <SettingsSection title={t("settings.network.server")} bare>
        <form onSubmit={save} className="grid gap-4 rounded-xl border bg-card p-5 shadow-sm">
          <Field invalid={error !== ""}>
            <FieldLabel>{t("settings.network.address")}</FieldLabel>
            <Input value={address} inputMode="url" autoComplete="off" spellCheck={false} placeholder="https://chat.example.com" onChange={(e) => setAddress(e.target.value)} />
            <FieldHint>{t("settings.network.hint")}</FieldHint>
            <FieldError>{error}</FieldError>
          </Field>
          <div className="flex justify-end">
            <Button type="submit">{t("common.actions.save")}</Button>
          </div>
        </form>
      </SettingsSection>
    </SettingsPage>
  );
}
