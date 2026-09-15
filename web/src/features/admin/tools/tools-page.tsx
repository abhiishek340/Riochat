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

function ServersSection() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [servers, setServers] = useState<McpServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [editing, setEditing] = useState<McpServer | "new" | null>(null);
  const [toolsOf, setToolsOf] = useState<number | null>(null);
  const [orderOpen, setOrderOpen] = useState(false);
  const [syncAsk, setSyncAsk] = useState<SyncAsk>(null);
  const [removing, setRemoving] = useState<McpServer | null>(null);
  const [syncingId, setSyncingId] = useState<number | null>(null);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    listServers(controller.signal).then(
      (rows) => {
        setServers(sortServers(rows));
        setFailed(false);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
        notify.error("adminTools.toast.loadFailed", error);
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  const getKey = useCallback((s: McpServer) => s.id, []);
  const rows = useRowUpdates<McpServer, number>({ getKey, setRows: setServers });

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return servers.filter((s) => (!status || s.status === status) && (!needle || s.name.toLowerCase().includes(needle) || s.baseURL.toLowerCase().includes(needle)));
  }, [servers, q, status]);
  const pageRows = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);
  useEffect(() => {
    if (page > 1 && (page - 1) * PAGE_SIZE >= filtered.length) setPage(Math.max(1, Math.ceil(filtered.length / PAGE_SIZE)));
  }, [filtered.length, page]);

  const statusText = (value: string) => (value === "active" ? t("adminTools.servers.status.active") : value === "inactive" ? t("adminTools.servers.status.inactive") : value);

  const toggle = (server: McpServer, on: boolean) => {
    const next = on ? "active" : "inactive";
    void rows.update(
      server,
      (r) => ({ ...r, status: next }),
      () => updateServer(server.id, { name: server.name, baseURL: server.baseURL, headersJSON: server.headersJSON.trim() || "{}", status: next }),
      {
        failKey: "adminTools.toast.updateFailed",
        merge: (_guess, serverRow) => serverRow,
        onSuccess: () => notify.success("adminTools.toast.statusChanged", { name: server.name, status: statusText(next) }),
      },
    );
  };

  const runSync = async (server: McpServer, overwrite: boolean) => {
    setSyncingId(server.id);
    try {
      const tools = await syncServer(server.id, overwrite);
      notify.success("adminTools.sync.done", { count: tools.length });
      setToolsOf(server.id);
    } catch (error) {
      notify.error("adminTools.sync.failed", error);
    } finally {
      setSyncingId(null);
      reload();
    }
  };

  const startSync = (server: McpServer) => {
    if (server.requiresToolMetadataSyncConfirmation) setSyncAsk(server);
    else void runSync(server, false);
  };

  const columns: Column<McpServer>[] = [
    {
      id: "name",
      header: t("adminTools.servers.columns.name"),
      sticky: "left",
      cell: (s) => (
        <div className="min-w-0">
          <div className="truncate font-medium">{s.name}</div>
          {s.lastError ? <div className="truncate text-xs text-destructive">{t("adminTools.servers.lastError", { error: s.lastError })}</div> : null}
        </div>
      ),
    },
    { id: "url", header: t("adminTools.servers.columns.address"), cell: (s) => <Mono className="break-all">{s.baseURL}</Mono> },
    {
      id: "tools",
      header: t("adminTools.servers.columns.tools"),
      cell: (s) => <Badge tone={s.activeToolCount > 0 ? "success" : "neutral"}>{t("adminTools.servers.toolCount", { active: s.activeToolCount, total: s.toolCount })}</Badge>,
    },
    {
      id: "status",
      header: t("adminTools.servers.columns.status"),
      cell: (s) => <Switch checked={s.status === "active"} disabled={rows.isPending(s.id)} onCheckedChange={(on) => toggle(s, on)} aria-label={`${s.name}: ${statusText(s.status)}`} />,
    },
    {
      id: "synced",
      header: t("adminTools.servers.columns.synced"),
      className: "whitespace-nowrap text-muted-foreground",
      cell: (s) => (s.lastSyncedAt ? formatDateTime(s.lastSyncedAt, locale) : t("adminTools.servers.neverSynced")),
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (s) => (
        <CellActions>
          <Tooltip label={t("adminTools.servers.actions.tools")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminTools.servers.actions.tools")}: ${s.name}`} onClick={() => setToolsOf(s.id)}>
              <Wrench />
            </Button>
          </Tooltip>
          <Tooltip label={t("adminTools.servers.actions.sync")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminTools.servers.actions.sync")}: ${s.name}`} loading={syncingId === s.id} onClick={() => startSync(s)}>
              <RefreshCw />
            </Button>
          </Tooltip>
          <Tooltip label={t("adminTools.servers.actions.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminTools.servers.actions.edit")}: ${s.name}`} onClick={() => setEditing(s)}>
              <Pencil />
            </Button>
          </Tooltip>
          <Tooltip label={t("adminTools.servers.actions.delete")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminTools.servers.actions.delete")}: ${s.name}`} onClick={() => setRemoving(s)}>
              <Trash2 className="text-destructive" />
            </Button>
          </Tooltip>
        </CellActions>
      ),
    },
  ];

  const activeServer = servers.find((s) => s.id === toolsOf) ?? null;

  return (
    <Section
      title={t("adminTools.servers.title")}
      description={t("adminTools.servers.description")}
      actions={
        <>
          <Button variant="outline" size="sm" onClick={() => setOrderOpen(true)} disabled={servers.length === 0}>
            <ArrowUpDown />
            {t("adminTools.servers.order")}
          </Button>
          <Button size="sm" onClick={() => setEditing("new")}>
            <Plus />
            {t("adminTools.servers.add")}
          </Button>
        </>
      }
      flush
    >
      <div className="space-y-3 p-4">
        <Toolbar>
          <SearchBox
            value={q}
            onChange={(v) => {
              setQ(v);
              setPage(1);
            }}
            placeholder={t("adminTools.servers.search")}
            className="sm:w-72"
          />
          <OptionSelect
            value={status}
            onChange={(v) => {
              setStatus(v);
              setPage(1);
            }}
            label={t("adminCommon.table.status")}
            options={[
              { value: "", label: t("adminTools.servers.allStatuses") },
              { value: "active", label: t("adminTools.servers.status.active") },
              { value: "inactive", label: t("adminTools.servers.status.inactive") },
            ]}
          />
        </Toolbar>
        <DataTable columns={columns} rows={pageRows} rowKey={getKey} loading={loading} error={failed} onRetry={reload} empty={t("adminTools.servers.empty")} label={t("adminTools.servers.title")} />
        <Pagination page={page} pageSize={PAGE_SIZE} total={filtered.length} onPage={setPage} />
      </div>

      <ServerDialog
        target={editing}
        onOpenChange={(open) => !open && setEditing(null)}
        onSave={async (input, existing) => {
          if (existing) {
            const saved = await updateServer(existing.id, input);
            setServers((list) => list.map((s) => (s.id === saved.id ? saved : s)));
            notify.success("adminTools.toast.updated");
            return;
          }
          const created = await createServer(input);
          notify.success("adminTools.toast.created");
          setServers((list) => sortServers([...list, created]));
          await runSync(created, false);
        }}
      />
      {activeServer ? (
        <ToolSheet
          key={activeServer.id}
          server={activeServer}
          onClose={() => setToolsOf(null)}
          onServerChange={(next) => setServers((list) => list.map((s) => (s.id === next.id ? next : s)))}
          onSync={() => startSync(activeServer)}
        />
      ) : null}
      <OrderSheet
        open={orderOpen}
        servers={servers}
        onOpenChange={setOrderOpen}
        onSaved={(results) => {
          setServers(sortServers(results.map((r) => r.server)));
        }}
      />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminTools.delete.title", { name: removing?.name ?? "" })}
        description={t("adminTools.delete.body")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          if (!removing) return;
          try {
            await deleteServer(removing.id);
          } catch (error) {
            notify.error("adminTools.toast.deleteFailed", error);
            throw error;
          }
          setServers((list) => list.filter((s) => s.id !== removing.id));
          if (toolsOf === removing.id) setToolsOf(null);
          notify.success("adminTools.delete.done");
        }}
      />
      <Dialog open={syncAsk !== null} onOpenChange={(open) => !open && setSyncAsk(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("adminTools.sync.confirmTitle", { name: syncAsk?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("adminTools.sync.confirmBody")}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setSyncAsk(null)}>
              {t("common.actions.cancel")}
            </Button>
            <Button
              variant="outline"
              onClick={() => {
                const s = syncAsk;
                setSyncAsk(null);
                if (s) void runSync(s, false);
              }}
            >
              {t("adminTools.sync.keep")}
            </Button>
            <Button
              onClick={() => {
                const s = syncAsk;
                setSyncAsk(null);
                if (s) void runSync(s, true);
              }}
            >
              {t("adminTools.sync.overwrite")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Section>
  );
}
