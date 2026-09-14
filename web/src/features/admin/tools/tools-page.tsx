"use client";

import { ArrowUpDown, Pencil, Plus, RefreshCw, Trash2, Wrench } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { Tooltip } from "@/components/ui/tooltip";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { AdminPage, Mono, Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { AdminSection } from "../components/section-guard";
import { SettingsGroup } from "../components/settings-group";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { formatDateTime } from "../lib/format";
import type { FieldDef } from "../lib/settings-form";
import { useNotify } from "../lib/use-notify";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSettingsForm } from "../lib/use-settings-form";
import { createServer, deleteServer, listServers, syncServer, updateServer, type McpServer } from "./api";
import { OrderSheet } from "./order-sheet";
import { ServerDialog } from "./server-dialog";
import { sortServers } from "./tool-list";
import { ToolSheet } from "./tool-sheet";

const NS = "mcp";
const ENABLE = `${NS}.mcp_enable`;
const MAIN: FieldDef[] = [
  { ns: NS, key: "mcp_enable", type: "bool", label: "adminTools.settings.enable", hint: "adminTools.settings.enableHint", def: "false" },
  // The prompt is saved exactly as typed (no trimming), unlike the other text settings.
  { ns: NS, key: "mcp_tool_prompt", type: "textarea", label: "adminTools.settings.prompt", hint: "adminTools.settings.promptHint", def: "", keepWhitespace: true },
];
const LIMITS: FieldDef[] = [
  { ns: NS, key: "mcp_max_selected_tools_per_message", type: "int", label: "adminTools.settings.maxSelected", hint: "adminTools.settings.maxSelectedHint", def: "32" },
  { ns: NS, key: "mcp_max_llm_calls_per_run", type: "int", label: "adminTools.settings.maxLlmCalls", hint: "adminTools.settings.maxLlmCallsHint", def: "5" },
  { ns: NS, key: "mcp_max_tool_calls_per_run", type: "int", label: "adminTools.settings.maxToolCalls", hint: "adminTools.settings.maxToolCallsHint", def: "8" },
  { ns: NS, key: "mcp_max_concurrent_calls", type: "int", label: "adminTools.settings.maxConcurrent", hint: "adminTools.settings.maxConcurrentHint", def: "8" },
  { ns: NS, key: "mcp_tool_timeout_seconds", type: "int", label: "adminTools.settings.timeout", hint: "adminTools.settings.timeoutHint", def: "10", suffix: "s" },
  { ns: NS, key: "mcp_tool_retry_count", type: "int", label: "adminTools.settings.retries", hint: "adminTools.settings.retriesHint", def: "0" },
];
const ALL_FIELDS = [...MAIN, ...LIMITS];
const PAGE_SIZE = 25;

export function AdminToolsPage() {
  return (
    <AdminSection>
      <ToolsContent />
    </AdminSection>
  );
}

function ToolsContent() {
  const t = useT();
  const form = useSettingsForm({ fields: ALL_FIELDS });
  const enabled = form.draft[ENABLE] === "true";
  return (
    <AdminPage title={t("adminTools.title")} description={t("adminTools.description")}>
      <SettingsGroup form={form} fields={MAIN} title={t("adminTools.settings.title")} description={t("adminTools.settings.description")} />
      <SettingsGroup form={form} fields={LIMITS} title={t("adminTools.settings.limitsTitle")} description={t("adminTools.settings.limitsDescription")} />
      {enabled && !form.loading ? <ServersSection /> : null}
    </AdminPage>
  );
}

type SyncAsk = McpServer | null;
