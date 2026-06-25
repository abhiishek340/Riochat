"use client";

import { MoreHorizontal } from "lucide-react";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Table, TableBody, TableCell, TableEmptyRow, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/toast-store";
import { endSession } from "@/lib/api/session-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useI18n, useT } from "@/lib/i18n/provider";
import { SettingsSection } from "../parts";
import { formatDateTime } from "../lib/format";
import { revokeSession, type SessionRow } from "./api";

/** "<browser> (<OS>)" when both are known, else the device label or name. */
export function deviceTitle(s: Pick<SessionRow, "browserName" | "osName" | "deviceLabel" | "deviceName">, unknown: string): string {
  if (s.browserName && s.osName) return `${s.browserName} (${s.osName})`;
  return s.deviceLabel || s.deviceName || unknown;
}

export function locationText(s: Pick<SessionRow, "cityName" | "regionName" | "countryCode">): string {
  return [s.cityName, s.regionName, s.countryCode].filter(Boolean).join(", ");
}

export function SessionsSection({ sessions, loading, onRemoved }: { sessions: SessionRow[]; loading: boolean; onRemoved: (id: string) => void }) {
  const t = useT();
  const { locale } = useI18n();
  const errorMessage = useErrorMessage();
  const [busyId, setBusyId] = useState<string | null>(null);

  const revoke = async (s: SessionRow) => {
    if (busyId) return;
    setBusyId(s.sessionID);
    try {
      await revokeSession(s.sessionID);
      if (s.current) {
        await endSession();
        return;
      }
      onRemoved(s.sessionID);
      toast.success(t("settings.account.toasts.sessionLoggedOut"));
    } catch (e) {
      toast.error(t("settings.account.toasts.logoutSessionFailed"), { description: errorMessage(e) });
    } finally {
      setBusyId(null);
    }
  };

  return (
    <SettingsSection title={t("settings.account.sessions.title")} description={t("settings.account.sessions.description")} bare>
      <Table className="min-w-[840px]" aria-label={t("settings.account.sessions.title")}>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>{t("settings.account.sessions.device")}</TableHead>
            <TableHead>{t("settings.account.sessions.location")}</TableHead>
            <TableHead>{t("settings.account.sessions.created")}</TableHead>
            <TableHead>{t("settings.account.sessions.updated")}</TableHead>
            <TableHead className="w-12">
              <span className="sr-only">{t("settings.account.sessions.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading ? (
            <TableEmptyRow colSpan={5}>{t("common.states.loading")}</TableEmptyRow>
          ) : sessions.length === 0 ? (
            <TableEmptyRow colSpan={5}>{t("settings.account.sessions.empty")}</TableEmptyRow>
          ) : (
            sessions.map((s) => (
              <TableRow key={s.sessionID}>
                <TableCell>
                  <span className="flex items-center gap-2 font-medium">
                    {deviceTitle(s, t("settings.account.sessions.unknownDevice"))}
                    {s.current ? <Badge tone="primary">{t("settings.account.sessions.current")}</Badge> : null}
                  </span>
                </TableCell>
                <TableCell>
                  <span className="grid">
                    <span>{locationText(s) || t("settings.account.sessions.unknownLocation")}</span>
                    <span className="text-xs text-muted-foreground">{s.clientIP || t("settings.account.sessions.unknownIP")}</span>
                  </span>
                </TableCell>
                <TableCell className="whitespace-nowrap">{formatDateTime(s.createdAt, locale)}</TableCell>
                <TableCell className="whitespace-nowrap">{formatDateTime(s.updatedAt, locale)}</TableCell>
                <TableCell>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon-sm" aria-label={t("settings.account.sessions.rowActions", { device: deviceTitle(s, t("settings.account.sessions.unknownDevice")) })} disabled={busyId === s.sessionID}>
                        <MoreHorizontal />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent>
                      <DropdownMenuItem destructive disabled={busyId !== null} onSelect={() => void revoke(s)}>
                        {t("settings.account.sessions.logoutThis")}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </SettingsSection>
  );
}
