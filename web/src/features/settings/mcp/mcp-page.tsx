"use client";

import { Plug } from "lucide-react";
import { useMemo, useState } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { api } from "@/lib/api/client";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { isRecord, num } from "@/lib/utils/guards";
import { Checkbox, LoadErrorNotice, SettingsPage, SettingsSection } from "../parts";
import { parseToolIds, SETTING_KEYS } from "../lib/chat-settings";
import { useLoad } from "../lib/use-load";
import { useUserSettings } from "../lib/use-user-settings";
import { DEFAULT_MAX_TOOLS, groupByServer, matchesQuery, normalizeTools, pruneSelection, serializeToolIds, toggleTool, toolLabel, type McpTool } from "./tools-logic";

async function load(signal: AbortSignal): Promise<{ tools: McpTool[]; max: number }> {
  const [tools, policy] = await Promise.allSettled([api.get<unknown>("/mcp/tools", { signal }), api.get<unknown>("/settings/mcp-policy", { signal })]);
  if (tools.status === "rejected") throw tools.reason;
  const max = policy.status === "fulfilled" && isRecord(policy.value) ? num(policy.value.maxSelectedToolsPerMessage, DEFAULT_MAX_TOOLS) : DEFAULT_MAX_TOOLS;
  return { tools: normalizeTools(tools.value), max: max > 0 ? max : DEFAULT_MAX_TOOLS };
}

/** Tools the administrator enabled, with the user's choice of which ones start selected in a new message. */
export function McpSettingsPage() {
  const t = useT();
  const errorMessage = useErrorMessage();
  usePageTitle(t("settings.title"));
  const meta = useLoad(load, "mcp");
  const { values, loaded, failed, update, reload } = useUserSettings();
  const [query, setQuery] = useState("");

  const tools = useMemo(() => meta.data?.tools ?? [], [meta.data]);
  const max = meta.data?.max ?? DEFAULT_MAX_TOOLS;
  const selected = useMemo(() => pruneSelection(parseToolIds(values[SETTING_KEYS.defaultMcpToolIds]), tools), [values, tools]);
  const groups = useMemo(() => groupByServer(tools.filter((x) => matchesQuery(x, query))), [tools, query]);
  const loading = meta.loading || !loaded;

  const change = async (id: number) => {
    const next = toggleTool(selected, id, max);
    if (next.length === selected.length && !selected.includes(id)) {
      toast.info(t("settings.tools.limitReached", { max }));
      return;
    }
    try {
      await update({ [SETTING_KEYS.defaultMcpToolIds]: serializeToolIds(next) });
    } catch (e) {
      toast.error(t("settings.tools.saveFailed"), { description: errorMessage(e, t("settings.chat.toasts.retryLater")) });
    }
  };

  return (
    <SettingsPage title={t("settings.nav.tools")} description={t("settings.tools.description")}>
      {meta.error ? <LoadErrorNotice title={t("settings.tools.loadFailed")} error={meta.error} onRetry={meta.reload} /> : null}
      {failed ? <LoadErrorNotice title={t("settings.chat.toasts.loadFailed")} onRetry={() => void reload()} /> : null}
      {loading ? (
        <div className="grid gap-3 rounded-xl border bg-card p-4">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : tools.length === 0 ? (
        meta.error ? null : (
          <div className="rounded-xl border bg-card">
            <EmptyState icon={<Plug />} title={t("settings.tools.emptyTitle")} description={t("settings.tools.emptyBody")} />
          </div>
        )
      ) : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <Input className="max-w-xs" value={query} placeholder={t("settings.tools.search")} aria-label={t("settings.tools.search")} onChange={(e) => setQuery(e.target.value)} />
            <span className="text-sm tabular-nums text-muted-foreground">{t("settings.tools.counter", { count: selected.length, max })}</span>
          </div>
          {groups.length === 0 ? <p className="text-sm text-muted-foreground">{t("settings.tools.noMatches")}</p> : null}
          {groups.map((group) => (
            <SettingsSection key={group.serverID} title={group.serverName || t("settings.tools.unnamedServer")}>
              {group.tools.map((tool) => {
                const on = selected.includes(tool.id);
                return (
                  <label key={tool.id} className="flex cursor-pointer items-start gap-3 px-4 py-3 hover:bg-muted/40">
                    <Checkbox className="mt-0.5" checked={on} disabled={!on && selected.length >= max} onCheckedChange={() => void change(tool.id)} />
                    <span className="grid min-w-0 gap-0.5">
                      <span className="text-sm font-medium">{toolLabel(tool)}</span>
                      {tool.description ? <span className="line-clamp-2 text-[13px] text-muted-foreground">{tool.description}</span> : null}
                    </span>
                  </label>
                );
              })}
            </SettingsSection>
          ))}
        </>
      )}
    </SettingsPage>
  );
}
