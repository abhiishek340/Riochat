"use client";

import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { Checkbox } from "../components/checkbox";
import { Section, SettingRow } from "../components/page";
import { LoadError } from "../components/load-error";
import { Pagination } from "../components/pagination";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { runBulk } from "../lib/bulk";
import { useSelection } from "../lib/use-selection";
import type { BillingConfig } from "../lib/wire";
import { listServers, listServerTools, patchTool, type McpTool } from "../tools/api";
import { toolLabel } from "../tools/tool-list";
import { patchConfig } from "./api";
import { nanoText, nativeChanges, nativeFull, parseUsdPrice } from "./native";

export function NativeToolsSection({ config, onConfig }: { config: BillingConfig; onConfig: (config: BillingConfig) => void }) {
  const t = useT();
  const message = useErrorMessage();
  const [enabled, setEnabled] = useState(config.nativeToolBillingEnabled);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setEnabled(config.nativeToolBillingEnabled);
    setDrafts({});
  }, [config]);

  const tools = config.nativeToolPricing;
  const dirty = enabled !== config.nativeToolBillingEnabled || nativeChanges(tools, drafts).length > 0;
  const save = async () => {
    setSaving(true);
    try {
      onConfig(await patchConfig({ mode: config.mode, nativeToolBillingEnabled: enabled, nativeToolPricing: nativeFull(tools, drafts) }));
      toast.success(t("adminBilling.native.saved"));
    } catch (error) {
      toast.error(t("adminBilling.native.saveFailed"), { description: message(error) });
    } finally {
      setSaving(false);
    }
  };

  return (
    <Section title={t("adminBilling.native.title")} description={t("adminBilling.native.description")}>
      <SettingRow label={t("adminBilling.native.enabled")} hint={t("adminBilling.native.enabledHint")} htmlFor="native-billing">
        <Switch id="native-billing" checked={enabled} onCheckedChange={setEnabled} />
      </SettingRow>
      {tools.length === 0 ? <p className="py-4 text-sm text-muted-foreground">{t("adminBilling.native.empty")}</p> : null}
      <div className="divide-y border-t">
        {tools.map((tool) => {
          const text = drafts[tool.toolKey] ?? nanoText(tool.priceNanousd);
          const invalid = !parseUsdPrice(text).ok;
          return (
            <div key={tool.toolKey} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="text-sm font-medium">{tool.label || tool.toolKey}</div>
                {tool.description ? <p className="text-[13px] text-muted-foreground">{tool.description}</p> : null}
              </div>
              <div className="flex items-center gap-2">
                <span className="text-sm text-muted-foreground">$</span>
                <Input inputMode="decimal" value={text} disabled={!enabled} aria-invalid={invalid || undefined} aria-label={`${tool.label || tool.toolKey}: ${t("adminBilling.native.perCall")}`} onChange={(e) => setDrafts((d) => ({ ...d, [tool.toolKey]: e.target.value }))} className="w-28 text-right tabular-nums" />
                <span className="text-sm text-muted-foreground">{t("adminBilling.native.perCall")}</span>
              </div>
            </div>
          );
        })}
      </div>
      {dirty ? (
        <div className="mt-4 flex justify-end gap-2 border-t pt-4">
          <Button
            variant="outline"
            onClick={() => {
              setEnabled(config.nativeToolBillingEnabled);
              setDrafts({});
            }}
          >
            {t("adminCommon.settings.revert")}
          </Button>
          <Button loading={saving} onClick={save}>
            {t("adminCommon.settings.save")}
          </Button>
        </div>
      ) : null}
    </Section>
  );
}

const PAGE_SIZE = 25;

/** Per-call prices for every MCP tool; a bulk price only edits drafts and one save writes the changed rows. */
export function McpToolsPricing() {
  const t = useT();
  const message = useErrorMessage();
  const [tools, setTools] = useState<McpTool[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [drafts, setDrafts] = useState<Record<number, string>>({});
  const [q, setQ] = useState("");
  const [server, setServer] = useState("");
  const [page, setPage] = useState(1);
  const [bulk, setBulk] = useState("");
  const [saving, setSaving] = useState(false);
  const selection = useSelection<number>();

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setFailed(false);
    (async () => {
      try {
        const servers = await listServers(controller.signal);
        const lists = await Promise.all(servers.map((s) => listServerTools(s.id, controller.signal)));
        setTools(lists.flat());
      } catch (error) {
        if (isAbortError(error)) return;
        setFailed(true);
        toast.error(t("adminBilling.mcp.loadFailed"), { description: message(error) });
      }
      setLoading(false);
    })();
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt]);

  const servers = useMemo(() => [...new Map(tools.map((x) => [String(x.serverID), x.serverName])).entries()], [tools]);
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return tools.filter((x) => (!server || String(x.serverID) === server) && (!needle || toolLabel(x).toLowerCase().includes(needle) || x.name.toLowerCase().includes(needle) || x.serverName.toLowerCase().includes(needle)));
  }, [tools, q, server]);
  const slice = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);
  const ids = slice.map((x) => x.id);

  const changed = tools.filter((x) => {
    const text = drafts[x.id];
    if (text === undefined) return false;
    const parsed = parseUsdPrice(text);
    return parsed.ok && parsed.nano !== x.priceNanousd;
  });

  const applyBulk = () => {
    const parsed = parseUsdPrice(bulk);
    if (!parsed.ok || bulk.trim() === "") return toast.error(t("adminBilling.mcp.invalidPrice"));
    setDrafts((d) => ({ ...d, ...Object.fromEntries([...selection.selected].map((id) => [id, bulk.trim()])) }));
  };

  const save = async () => {
    setSaving(true);
    try {
      const outcome = await runBulk(changed, async (x) => {
        const parsed = parseUsdPrice(drafts[x.id] ?? "");
        if (!parsed.ok) throw new Error("invalid");
        const saved = await patchTool(x.id, { priceNanousd: parsed.nano });
        setTools((list) => list.map((y) => (y.id === x.id ? { ...y, priceNanousd: saved.priceNanousd } : y)));
        setDrafts((d) => {
          const next = { ...d };
          delete next[x.id];
          return next;
        });
      });
      if (outcome.failed.length > 0) toast.error(t("adminBilling.mcp.savePartial", { ok: outcome.succeeded.length, failed: outcome.failed.length }), { description: message(outcome.failed[0]?.error) });
      else toast.success(t("adminBilling.mcp.saved", { count: outcome.succeeded.length }));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Section title={t("adminBilling.mcp.title")} description={t("adminBilling.mcp.description")}>
      <div className="space-y-3">
        <Toolbar>
          <SearchBox
            value={q}
            onChange={(v) => {
              setQ(v);
              setPage(1);
            }}
            placeholder={t("adminBilling.mcp.search")}
          />
          <OptionSelect
            value={server}
            onChange={(v) => {
              setServer(v);
              setPage(1);
            }}
            label={t("adminBilling.mcp.server")}
            options={[{ value: "", label: t("adminBilling.mcp.anyServer") }, ...servers.map(([id, name]) => ({ value: id, label: name }))]}
          />
          {selection.count > 0 ? (
            <span className="flex items-center gap-1">
              <Input inputMode="decimal" value={bulk} onChange={(e) => setBulk(e.target.value)} placeholder="0.00" aria-label={t("adminBilling.mcp.bulkPrice")} className="h-8 w-24" />
              <Button size="sm" variant="outline" onClick={applyBulk}>
                {t("adminBilling.mcp.applyToSelected", { count: selection.count })}
              </Button>
            </span>
          ) : null}
        </Toolbar>
        {loading ? (
          <Skeleton className="h-40 w-full" />
        ) : failed ? (
          <LoadError message={t("adminBilling.mcp.loadFailed")} onRetry={() => setAttempt((n) => n + 1)} />
        ) : slice.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">{t("adminBilling.mcp.empty")}</p>
        ) : (
          <div className="overflow-auto rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/60 text-left text-xs uppercase tracking-wide text-muted-foreground">
                <tr>
                  <th className="w-10 px-3 py-2">
                    <Checkbox aria-label={t("adminCommon.table.selectAll")} checked={selection.headerState(ids)} onCheckedChange={(on) => selection.toggleAll(ids, on)} />
                  </th>
                  <th className="px-3 py-2">{t("adminBilling.mcp.tool")}</th>
                  <th className="px-3 py-2">{t("adminBilling.mcp.server")}</th>
                  <th className="px-3 py-2 text-right">{t("adminBilling.mcp.price")}</th>
                </tr>
              </thead>
              <tbody>
                {slice.map((x) => {
                  const text = drafts[x.id] ?? nanoText(x.priceNanousd);
                  return (
                    <tr key={x.id} className="border-t">
                      <td className="px-3 py-2">
                        <Checkbox aria-label={t("adminCommon.table.selectRowNamed", { name: toolLabel(x) })} checked={selection.has(x.id)} onCheckedChange={(on) => selection.toggle(x.id, on)} />
                      </td>
                      <td className="px-3 py-2 font-medium">{toolLabel(x)}</td>
                      <td className="px-3 py-2 text-muted-foreground">{x.serverName}</td>
                      <td className="px-3 py-2 text-right">
                        <span className="inline-flex items-center gap-1">
                          <span className="text-muted-foreground">$</span>
                          <Input inputMode="decimal" value={text} aria-invalid={!parseUsdPrice(text).ok || undefined} aria-label={`${toolLabel(x)}: ${t("adminBilling.mcp.price")}`} onChange={(e) => setDrafts((d) => ({ ...d, [x.id]: e.target.value }))} className="h-8 w-28 text-right tabular-nums" />
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        <Pagination page={page} pageSize={PAGE_SIZE} total={filtered.length} onPage={setPage} />
        {changed.length > 0 ? (
          <div className="flex justify-end gap-2 border-t pt-4">
            <Button variant="outline" onClick={() => setDrafts({})}>
              {t("adminCommon.settings.revert")}
            </Button>
            <Button loading={saving} onClick={save}>
              {t("adminBilling.mcp.saveChanged", { count: changed.length })}
            </Button>
          </div>
        ) : null}
      </div>
    </Section>
  );
}
