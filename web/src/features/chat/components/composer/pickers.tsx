"use client";

import { useEffect, useMemo, useState } from "react";
import { BookOpen, ChevronDown, ChevronRight, LayoutGrid, Search, Star, Wrench } from "lucide-react";
import { Badge, Button, Popover, PopoverContent, PopoverTrigger, Switch, Tooltip, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { listKnowledgeBases } from "../../api";
import { formatMoney, type Currency } from "../../lib/billing";
import { MAX_KNOWLEDGE_BASES } from "../../lib/selection";
import { KEYS } from "../../lib/settings";
import { saveSettings } from "../../state/resources";
import type { KnowledgeBaseDTO, McpToolDTO, UiComponentDTO } from "../../types";

function TriggerButton({ label, count, children, active }: { label: string; count?: number; children: React.ReactNode; active?: boolean }) {
  return (
    <Tooltip label={label}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={label}
          className={cn("relative inline-flex size-8 items-center justify-center rounded-lg text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-accent [&_svg]:size-4", active && "text-primary")}
        >
          {children}
          {count ? <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-primary px-1 text-center text-[10px] font-medium leading-4 text-primary-foreground">{count}</span> : null}
        </button>
      </PopoverTrigger>
    </Tooltip>
  );
}

// --- MCP tools ---------------------------------------------------------------------------------

export function ToolsPicker({
  tools,
  loaded,
  selectedIds,
  cap,
  defaultIds,
  onToggle,
  onToggleServer,
  currency,
}: {
  tools: McpToolDTO[];
  loaded: boolean;
  selectedIds: number[];
  cap: number;
  defaultIds: number[];
  onToggle: (tool: McpToolDTO) => void;
  onToggleServer: (tools: McpToolDTO[]) => void;
  currency: Currency;
}) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [described, setDescribed] = useState<number | null>(null);

  const servers = useMemo(() => {
    const q = query.trim().toLowerCase();
    const map = new Map<number, { id: number; name: string; tools: McpToolDTO[] }>();
    for (const tool of tools) {
      const hay = `${tool.displayName} ${tool.name} ${tool.serverName} ${tool.description}`.toLowerCase();
      if (q && !hay.includes(q)) continue;
      let s = map.get(tool.serverID);
      if (!s) map.set(tool.serverID, (s = { id: tool.serverID, name: tool.serverName || t("chatComposer.tools.unknownServer"), tools: [] }));
      s.tools.push(tool);
    }
    return [...map.values()];
  }, [tools, query, t]);

  const saveDefaults = async (ids: number[], message: "on" | "off") => {
    try {
      await saveSettings({ [KEYS.defaultToolIds]: JSON.stringify(ids) });
      toast.success(message === "on" ? t("chatComposer.tools.defaultEnabled") : t("chatComposer.tools.defaultDisabled"));
    } catch {
      toast.error(t("chatComposer.tools.defaultFailed"));
    }
  };

  if (loaded && tools.length === 0) return null;
  return (
    <Popover>
      <TriggerButton label={t("chatComposer.tools.title")} count={selectedIds.length} active={selectedIds.length > 0}>
        <Wrench />
      </TriggerButton>
      <PopoverContent side="top" align="start" className="w-[22rem] p-0">
        <div className="flex items-center justify-between border-b px-3 py-2">
          <span className="text-sm font-medium">{t("chatComposer.tools.title")}</span>
          <span className="text-xs text-muted-foreground">{t("chatComposer.tools.selected", { count: selectedIds.length })} / {cap}</span>
        </div>
        <div className="flex items-center gap-2 border-b px-3 py-1.5">
          <Search className="size-3.5 text-muted-foreground" />
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("chatComposer.tools.search")} aria-label={t("chatComposer.tools.search")} className="h-6 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground" />
        </div>
        <div className="max-h-80 overflow-auto p-1.5">
          {servers.length === 0 ? <p className="px-3 py-6 text-center text-sm text-muted-foreground">{query ? t("chatComposer.tools.noMatch") : t("chatComposer.tools.empty")}</p> : null}
          {servers.map((s) => {
            const onCount = s.tools.filter((x) => selectedIds.includes(x.id)).length;
            const open = !collapsed[String(s.id)];
            const allDefault = s.tools.every((x) => defaultIds.includes(x.id));
            return (
              <div key={s.id} className="mb-1">
                <div className="flex items-center gap-1.5 rounded-lg px-1.5 py-1 hover:bg-accent/50">
                  <button type="button" aria-expanded={open} aria-label={open ? t("chatComposer.tools.collapseServer", { server: s.name }) : t("chatComposer.tools.expandServer", { server: s.name })} onClick={() => setCollapsed((c) => ({ ...c, [String(s.id)]: open }))} className="rounded p-0.5 text-muted-foreground">
                    {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                  </button>
                  <span className="min-w-0 flex-1 truncate text-sm font-medium">{s.name}</span>
                  <span className="text-xs text-muted-foreground">{t("chatComposer.tools.serverCount", { selected: onCount, total: s.tools.length })}</span>
                  <Tooltip label={allDefault ? t("chatComposer.tools.unsetDefaultServer", { server: s.name }) : t("chatComposer.tools.setDefaultServer", { server: s.name })}>
                    <button
                      type="button"
                      aria-label={allDefault ? t("chatComposer.tools.unsetDefaultServer", { server: s.name }) : t("chatComposer.tools.setDefaultServer", { server: s.name })}
                      aria-pressed={allDefault}
                      onClick={() => {
                        const ids = new Set(defaultIds);
                        for (const x of s.tools) {
                          if (allDefault) ids.delete(x.id);
                          else ids.add(x.id);
                        }
                        void saveDefaults([...ids], allDefault ? "off" : "on");
                      }}
                      className={cn("rounded p-0.5", allDefault ? "text-warning" : "text-muted-foreground hover:text-foreground")}
                    >
                      <Star className={cn("size-3.5", allDefault && "fill-current")} />
                    </button>
                  </Tooltip>
                  <Switch checked={onCount === s.tools.length} onCheckedChange={() => onToggleServer(s.tools)} aria-label={t("chatComposer.tools.toggleServer", { server: s.name })} />
                </div>
                {open ? (
                  <ul className="ml-5 grid gap-0.5 border-l pl-2">
                    {s.tools.map((tool) => {
                      const on = selectedIds.includes(tool.id);
                      const isDefault = defaultIds.includes(tool.id);
                      const name = tool.displayName || tool.name;
                      return (
                        <li key={tool.id} className="rounded-lg px-1.5 py-1 hover:bg-accent/40">
                          <div className="flex items-center gap-1.5">
                            <span className="min-w-0 flex-1 truncate text-sm">{name}</span>
                            {tool.priceNanousd > 0 ? (
                              <Tooltip label={t("chatComposer.tools.paidNote")}>
                                <Badge tone="warning">{t("chatComposer.tools.paid", { price: formatMoney(tool.priceNanousd, currency) })}</Badge>
                              </Tooltip>
                            ) : null}
                            {tool.attachmentInputMode === "image" ? <Badge tone="outline">{t("chatComposer.tools.imageProcessor")}</Badge> : null}
                            <button type="button" onClick={() => setDescribed(described === tool.id ? null : tool.id)} className="text-xs text-muted-foreground hover:text-foreground" aria-expanded={described === tool.id} aria-label={t("chatComposer.tools.viewDescription")}>
                              ?
                            </button>
                            <Tooltip label={isDefault ? t("chatComposer.tools.unsetDefault", { tool: name }) : t("chatComposer.tools.setDefault", { tool: name })}>
                              <button
                                type="button"
                                aria-label={isDefault ? t("chatComposer.tools.unsetDefault", { tool: name }) : t("chatComposer.tools.setDefault", { tool: name })}
                                aria-pressed={isDefault}
                                onClick={() => void saveDefaults(isDefault ? defaultIds.filter((i) => i !== tool.id) : [...defaultIds, tool.id], isDefault ? "off" : "on")}
                                className={cn("rounded p-0.5", isDefault ? "text-warning" : "text-muted-foreground hover:text-foreground")}
                              >
                                <Star className={cn("size-3.5", isDefault && "fill-current")} />
                              </button>
                            </Tooltip>
                            <Switch checked={on} onCheckedChange={() => onToggle(tool)} aria-label={t("chatComposer.tools.toggleTool", { tool: name })} />
                          </div>
                          {described === tool.id ? <p className="mt-1 text-xs text-muted-foreground">{tool.description || t("chatComposer.tools.noDescription")}</p> : null}
                        </li>
                      );
                    })}
                  </ul>
                ) : null}
              </div>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}

// --- knowledge bases ----------------------------------------------------------------------------------

export function KnowledgePicker({
  selectedIds,
  onToggle,
  ragAvailable,
  ragReason,
}: {
  selectedIds: string[];
  onToggle: (id: string) => void;
  ragAvailable: boolean;
  ragReason: string;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const debounced = useDebouncedValue(query.trim(), 200);
  const [items, setItems] = useState<KnowledgeBaseDTO[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");

  useEffect(() => {
    if (!open || !ragAvailable) return;
    let live = true;
    setState("loading");
    listKnowledgeBases({ q: debounced, page: 1, pageSize: 50 }).then(
      (res) => {
        if (!live) return;
        setItems(res.results);
        setTotal(res.total);
        setPage(1);
        setState("idle");
      },
      () => live && setState("error"),
    );
    return () => {
      live = false;
    };
  }, [open, debounced, ragAvailable]);

  const more = async () => {
    const next = page + 1;
    setState("loading");
    try {
      const res = await listKnowledgeBases({ q: debounced, page: next, pageSize: 50 });
      setItems((cur) => [...cur, ...res.results.filter((r) => !cur.some((c) => c.publicID === r.publicID))]);
      setPage(next);
      setState("idle");
    } catch {
      setState("error");
    }
  };

  const reasonKey = (r: string) => {
    if (/host/i.test(r)) return "chatComposer.kb.reason.host";
    if (/model/i.test(r)) return "chatComposer.kb.reason.model";
    if (/client/i.test(r)) return "chatComposer.kb.reason.client";
    return "chatComposer.kb.reason.store";
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <TriggerButton label={t("chatComposer.kb.title")} count={selectedIds.length} active={selectedIds.length > 0}>
        <BookOpen />
      </TriggerButton>
      <PopoverContent side="top" align="start" className="w-80 p-0">
        <div className="flex items-center justify-between border-b px-3 py-2">
          <span className="text-sm font-medium">{t("chatComposer.kb.title")}</span>
          <span className="text-xs text-muted-foreground">{t("chatComposer.kb.selected", { count: selectedIds.length })} / {MAX_KNOWLEDGE_BASES}</span>
        </div>
        {!ragAvailable ? (
          <div className="grid gap-1 px-4 py-6 text-center">
            <p className="text-sm font-medium">{t("chatComposer.kb.unavailable")}</p>
            <p className="text-xs text-muted-foreground">{t(reasonKey(ragReason) as "chatComposer.kb.reason.store")}</p>
          </div>
        ) : (
          <>
            <div className="flex items-center gap-2 border-b px-3 py-1.5">
              <Search className="size-3.5 text-muted-foreground" />
              <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("chatComposer.kb.search")} aria-label={t("chatComposer.kb.search")} className="h-6 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground" />
            </div>
            <div className="max-h-72 overflow-auto p-1.5">
              {state === "error" ? <p className="px-3 py-4 text-center text-sm text-destructive">{t("chatComposer.kb.loadFailed")}</p> : null}
              {state !== "error" && items.length === 0 && state !== "loading" ? <p className="px-3 py-6 text-center text-sm text-muted-foreground">{t("chatComposer.kb.empty")}</p> : null}
              {items.map((kb) => {
                const ready = kb.readyFileCount > 0;
                const on = selectedIds.includes(kb.publicID);
                return (
                  <label key={kb.publicID} className={cn("flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm", ready ? "cursor-pointer hover:bg-accent/50" : "opacity-60")}>
                    <input type="checkbox" checked={on} disabled={!ready && !on} onChange={() => onToggle(kb.publicID)} className="size-4 accent-[var(--primary)]" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate">{kb.name}</span>
                      <span className="block truncate text-xs text-muted-foreground">{ready ? t("chatComposer.kb.readyFiles", { count: kb.readyFileCount }) : t("chatComposer.kb.notReady")}</span>
                    </span>
                  </label>
                );
              })}
              {items.length < total ? (
                <Button variant="ghost" size="sm" className="w-full" loading={state === "loading"} onClick={() => void more()}>
                  {t("chatComposer.kb.loadMore")}
                </Button>
              ) : null}
            </div>
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}

// --- interactive components --------------------------------------------------------------------------------

export function UiComponentsPicker({
  components,
  effective,
  onChange,
  onReset,
  explicit,
}: {
  components: UiComponentDTO[];
  effective: number[];
  onChange: (ids: number[]) => void;
  onReset: () => void;
  explicit: boolean;
}) {
  const t = useT();
  const visible = components.filter((c) => c.enabled);
  if (visible.length === 0) return null;
  const scopes: { id: string; label: string; items: UiComponentDTO[] }[] = [
    { id: "builtin", label: t("chatComposer.ui.builtin"), items: visible.filter((c) => c.scope === "builtin" || c.rendererKind === "builtin") },
    { id: "platform", label: t("chatComposer.ui.platform"), items: visible.filter((c) => c.scope === "platform" && c.rendererKind !== "builtin") },
    { id: "user", label: t("chatComposer.ui.mine"), items: visible.filter((c) => c.scope !== "builtin" && c.scope !== "platform" && c.rendererKind !== "builtin") },
  ].filter((s) => s.items.length > 0);
  const toggle = (id: number) => onChange(effective.includes(id) ? effective.filter((x) => x !== id) : [...effective, id]);
  return (
    <Popover>
      <TriggerButton label={t("chatComposer.ui.title")} count={effective.length} active={effective.length > 0}>
        <LayoutGrid />
      </TriggerButton>
      <PopoverContent side="top" align="start" className="w-72 p-0">
        <div className="flex items-center justify-between border-b px-3 py-2">
          <span className="text-sm font-medium">{t("chatComposer.ui.title")}</span>
          <button type="button" disabled={!explicit} onClick={onReset} className="text-xs text-muted-foreground hover:text-foreground disabled:opacity-40">
            {t("chatComposer.ui.restoreDefault")}
          </button>
        </div>
        <div className="max-h-72 overflow-auto p-1.5">
          {scopes.map((s) => (
            <div key={s.id}>
              <div className="px-2 pb-1 pt-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{s.label}</div>
              {s.items.map((c) => (
                <label key={c.id} className="flex cursor-pointer items-start gap-2 rounded-lg px-2 py-1.5 text-sm hover:bg-accent/50">
                  <input type="checkbox" checked={effective.includes(c.id)} onChange={() => toggle(c.id)} className="mt-0.5 size-4 accent-[var(--primary)]" />
                  <span className="min-w-0">
                    <span className="block truncate">{c.name}</span>
                    {c.description ? <span className="block text-xs text-muted-foreground">{c.description}</span> : null}
                  </span>
                </label>
              ))}
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
