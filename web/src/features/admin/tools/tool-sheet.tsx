"use client";

import { Braces, Pencil, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tooltip } from "@/components/ui/tooltip";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { BulkBar } from "../components/bulk-bar";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { JsonView } from "../components/json-view";
import { Pagination } from "../components/pagination";
import { Sheet } from "../components/sheet";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { attachmentProblem, stringArguments, type AttachmentDraft } from "../lib/mcp-schema";
import { useNotify } from "../lib/use-notify";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSelection } from "../lib/use-selection";
import { useSnapshot } from "../lib/use-snapshot";
import { listServerTools, patchTool, setToolsStatus, type McpServer, type McpTool, type ToolPatch } from "./api";
import { filterTools, mergeTools, sortTools, toolLabel, TOOL_SORTS, withToolCounts, type ToolSort } from "./tool-list";

const PAGE_SIZE = 25;

export function ToolSheet({ server, onClose, onServerChange, onSync }: { server: McpServer; onClose: () => void; onServerChange: (server: McpServer) => void; onSync: () => void }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [tools, setTools] = useState<McpTool[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [nonce, setNonce] = useState(0);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<"" | "active" | "inactive">("");
  const [sort, setSort] = useState<ToolSort>("custom");
  const [page, setPage] = useState(1);
  const [schemaOf, setSchemaOf] = useState<McpTool | null>(null);
  const [editing, setEditing] = useState<McpTool | null>(null);
  const [bulk, setBulk] = useState<"active" | "inactive" | null>(null);
  const selection = useSelection<number>();

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setFailed(false);
    listServerTools(server.id, controller.signal).then(
      (rows) => {
        setTools(rows);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
        notify.error("adminTools.toast.toolsLoadFailed", error);
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id, nonce]);

  // Keep the server's counters in step with the tool list.
  const lastCounts = useMemo(() => `${tools.length}:${tools.filter((x) => x.status === "active").length}`, [tools]);
  useEffect(() => {
    // A failed load leaves no list to count; the server's own counters stay.
    if (loading || failed) return;
    const next = withToolCounts(server, tools);
    if (next.toolCount !== server.toolCount || next.activeToolCount !== server.activeToolCount) onServerChange(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastCounts, loading, failed]);

  const visible = useMemo(() => sortTools(filterTools(tools, q, status), sort, locale), [tools, q, status, sort, locale]);
  const pageRows = visible.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);
  useEffect(() => {
    selection.prune(visible.map((x) => x.id));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible]);

  const getKey = useCallback((x: McpTool) => x.id, []);
  const rows = useRowUpdates<McpTool, number>({ getKey, setRows: setTools });

  const toggle = (tool: McpTool, on: boolean) => {
    const next = on ? "active" : "inactive";
    void rows.update(tool, (r) => ({ ...r, status: next }), () => patchTool(tool.id, { status: next }), {
      failKey: "adminTools.toast.updateFailed",
      merge: (guess, serverTool) => ({ ...guess, ...serverTool, serverName: serverTool.serverName || guess.serverName }),
    });
  };

  const applyBulk = async (next: "active" | "inactive") => {
    const ids = [...selection.selected].filter((id) => tools.find((x) => x.id === id)?.status !== next);
    if (ids.length === 0) return;
    const before = tools;
    setTools((list) => list.map((x) => (ids.includes(x.id) ? { ...x, status: next } : x)));
    try {
      const updated = await setToolsStatus(server.id, ids, next);
      setTools((list) => mergeTools(list, updated));
      notify.success("adminTools.tools.bulkDone", { count: ids.length });
      selection.clear();
    } catch (error) {
      setTools(before);
      notify.error("adminTools.toast.updateFailed", error);
      throw error;
    }
  };

  const columns: Column<McpTool>[] = [
    {
      id: "tool",
      header: t("adminTools.tools.columns.tool"),
      cell: (x) => (
        <div className="min-w-0">
          <div className="truncate font-medium">{toolLabel(x)}</div>
          {x.displayName.trim() && x.displayName !== x.name ? <div className="truncate font-mono text-xs text-muted-foreground">{x.name}</div> : null}
        </div>
      ),
    },
    { id: "description", header: t("adminTools.tools.columns.description"), className: "max-w-[24rem]", cell: (x) => <p className="line-clamp-2 text-[13px] text-muted-foreground">{x.description || "-"}</p> },
    {
      id: "attachment",
      header: t("adminTools.tools.columns.attachment"),
      cell: (x) => (x.attachmentInputMode === "image" ? <Badge tone="primary">{x.attachmentArgument || t("adminTools.tools.imageInput")}</Badge> : <span className="text-muted-foreground">{t("adminTools.tools.imageInputOff")}</span>),
    },
    {
      id: "status",
      header: t("adminTools.tools.columns.status"),
      cell: (x) => <Switch checked={x.status === "active"} disabled={rows.isPending(x.id)} onCheckedChange={(on) => toggle(x, on)} aria-label={toolLabel(x)} />,
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (x) => (
        <CellActions>
          <Tooltip label={t("adminTools.tools.viewSchema")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminTools.tools.viewSchema")}: ${toolLabel(x)}`} onClick={() => setSchemaOf(x)}>
              <Braces />
            </Button>
          </Tooltip>
          <Tooltip label={t("adminTools.tools.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminTools.tools.edit")}: ${toolLabel(x)}`} onClick={() => setEditing(x)}>
              <Pencil />
            </Button>
          </Tooltip>
        </CellActions>
      ),
    },
  ];

  const bulkCount = bulk ? [...selection.selected].filter((id) => tools.find((x) => x.id === id)?.status !== bulk).length : 0;

  return (
    <Sheet
      open
      onOpenChange={(open) => !open && onClose()}
      size="xl"
      title={t("adminTools.tools.title", { name: server.name })}
      description={t("adminTools.tools.description")}
      footer={
        <Button variant="outline" onClick={onSync}>
          <RefreshCw />
          {t("adminTools.servers.actions.sync")}
        </Button>
      }
    >
      <div className="space-y-3">
        <Toolbar>
          <SearchBox
            value={q}
            onChange={(v) => {
              setQ(v);
              setPage(1);
            }}
            placeholder={t("adminTools.tools.search")}
          />
          <OptionSelect
            value={status}
            onChange={(v) => {
              setStatus(v as "" | "active" | "inactive");
              setPage(1);
            }}
            label={t("adminCommon.table.status")}
            options={[
              { value: "", label: t("adminTools.tools.filter.all") },
              { value: "active", label: t("adminTools.tools.filter.active") },
              { value: "inactive", label: t("adminTools.tools.filter.inactive") },
            ]}
          />
          <OptionSelect value={sort} onChange={(v) => setSort(v as ToolSort)} label={t("adminTools.tools.sort.label")} options={TOOL_SORTS.map((s) => ({ value: s, label: t(`adminTools.tools.sort.${s}`) }))} />
        </Toolbar>
        <BulkBar count={selection.count} onClear={selection.clear}>
          <Button size="sm" variant="outline" onClick={() => setBulk("active")}>
            {t("adminTools.tools.enableSelected")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setBulk("inactive")}>
            {t("adminTools.tools.disableSelected")}
          </Button>
        </BulkBar>
        <DataTable columns={columns} rows={pageRows} rowKey={getKey} loading={loading} error={failed && tools.length === 0} onRetry={() => setNonce((n) => n + 1)} selection={selection} empty={t("adminTools.tools.empty")} rowLabel={(x) => x.displayName || x.name} label={t("adminTools.tools.title", { name: server.name })} />
        <Pagination page={page} pageSize={PAGE_SIZE} total={visible.length} onPage={setPage} />
      </div>

      <SchemaDialog tool={schemaOf} onClose={() => setSchemaOf(null)} />
      <ToolEditDialog
        tool={editing}
        onClose={() => setEditing(null)}
        onSaved={(saved) => {
          setTools((list) => list.map((x) => (x.id === saved.id ? { ...x, ...saved } : x)));
          notify.success("adminTools.toast.toolSaved");
        }}
      />
      <ConfirmDialog
        open={bulk !== null}
        onOpenChange={(open) => !open && setBulk(null)}
        title={bulk === "inactive" ? t("adminTools.tools.confirmDisable", { count: bulkCount }) : t("adminTools.tools.confirmEnable", { count: bulkCount })}
        description={t("adminTools.tools.confirmBody")}
        onConfirm={async () => {
          if (bulk) await applyBulk(bulk);
        }}
      />
    </Sheet>
  );
}
