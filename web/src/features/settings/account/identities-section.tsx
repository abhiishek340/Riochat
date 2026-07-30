"use client";

import { Plus } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Table, TableBody, TableCell, TableEmptyRow, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { absolutizeApiPath } from "@/lib/api/base-url";
import type { LoginProvider } from "@/lib/api/types";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useI18n, useT } from "@/lib/i18n/provider";
import { SettingsSection } from "../parts";
import { formatDateTime } from "../lib/format";
import { unlinkIdentity, type IdentityRow } from "./api";
import { startBind } from "./bind-flow";

function Logo({ src, name }: { src: string; name: string }) {
  if (src) {
    // eslint-disable-next-line @next/next/no-img-element -- provider logos are operator-supplied URLs
    return <img src={absolutizeApiPath(src)} alt="" className="size-5 shrink-0 rounded-sm object-contain" />;
  }
  return (
    <span aria-hidden className="flex size-5 shrink-0 items-center justify-center rounded-sm bg-muted text-[11px] font-semibold text-muted-foreground">
      {Array.from(name)[0]?.toUpperCase() ?? "?"}
    </span>
  );
}

/** Providers still available to bind: login-enabled and not linked yet (matched by slug). */
export function bindableProviders(providers: readonly LoginProvider[], linked: readonly IdentityRow[]): LoginProvider[] {
  const slugs = new Set(linked.map((i) => i.providerSlug));
  return providers.filter((p) => p.loginEnabled && !slugs.has(p.slug));
}

export function IdentitiesSection({
  identities,
  providers,
  loading,
  hasPassword,
  onChanged,
}: {
  identities: IdentityRow[];
  providers: LoginProvider[];
  loading: boolean;
  hasPassword: boolean;
  onChanged: () => void;
}) {
  const t = useT();
  const { locale } = useI18n();
  const errorMessage = useErrorMessage();
  const [busyId, setBusyId] = useState<number | null>(null);
  const available = bindableProviders(providers, identities);
  // Removing the last way in would lock the user out.
  const lockedLast = !hasPassword && identities.length <= 1;

  const bind = async (p: LoginProvider) => {
    try {
      await startBind(p.slug, "/settings/account");
    } catch (e) {
      if ((e as { name?: string } | null)?.name === "AbortError") return;
      toast.error(t("settings.account.toasts.bindFailed"), { description: errorMessage(e) });
    }
  };

  const unlink = async (row: IdentityRow) => {
    setBusyId(row.id);
    try {
      await unlinkIdentity(row.id);
      onChanged();
      toast.success(t("settings.account.toasts.identityUnlinked"));
    } catch (e) {
      toast.error(t("settings.account.toasts.unlinkFailed"), { description: errorMessage(e) });
    } finally {
      setBusyId(null);
    }
  };

  return (
    <SettingsSection
      title={t("settings.account.identities.title")}
      description={t("settings.account.identities.description")}
      bare
      actions={
        available.length > 0 ? (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Plus />
                {t("settings.account.identities.bind")}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              {available.map((p) => (
                <DropdownMenuItem key={p.slug} onSelect={() => void bind(p)}>
                  <Logo src={p.logoURL} name={p.name || p.slug} />
                  {p.name || p.slug}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null
      }
    >
      <Table aria-label={t("settings.account.identities.title")}>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>{t("settings.account.identities.provider")}</TableHead>
            <TableHead>{t("settings.account.identities.username")}</TableHead>
            <TableHead>{t("settings.account.identities.email")}</TableHead>
            <TableHead>{t("settings.account.identities.linked")}</TableHead>
            <TableHead className="w-24">
              <span className="sr-only">{t("settings.account.sessions.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading ? (
            <TableEmptyRow colSpan={5}>{t("common.states.loading")}</TableEmptyRow>
          ) : identities.length === 0 ? (
            <TableEmptyRow colSpan={5}>{t("settings.account.identities.empty")}</TableEmptyRow>
          ) : (
            identities.map((row) => {
              const provider = providers.find((p) => p.slug === row.providerSlug);
              const name = row.providerName || row.providerSlug || row.providerType;
              return (
                <TableRow key={row.id}>
                  <TableCell>
                    <span className="flex items-center gap-2 font-medium">
                      <Logo src={provider?.logoURL || row.providerLogoURL} name={name} />
                      {name}
                    </span>
                  </TableCell>
                  <TableCell>{row.providerDisplayName || "-"}</TableCell>
                  <TableCell>{row.email || "-"}</TableCell>
                  <TableCell className="whitespace-nowrap">{formatDateTime(row.linkedAt, locale)}</TableCell>
                  <TableCell>
                    <Tooltip label={lockedLast ? t("settings.account.identities.lastTip") : ""}>
                      {/* A disabled button cannot take focus: the wrapper does, so the reason is reachable by keyboard. */}
                      <span role={lockedLast ? "group" : undefined} tabIndex={lockedLast ? 0 : undefined} aria-label={lockedLast ? t("settings.account.identities.lastTip") : undefined} className="inline-flex rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring">
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={lockedLast || busyId === row.id}
                          loading={busyId === row.id}
                          aria-label={t("settings.account.identities.unlinkNamed", { name })}
                          onClick={() => void unlink(row)}
                        >
                          {t("settings.account.identities.unlink")}
                        </Button>
                      </span>
                    </Tooltip>
                  </TableCell>
                </TableRow>
              );
            })
          )}
        </TableBody>
      </Table>
    </SettingsSection>
  );
}
