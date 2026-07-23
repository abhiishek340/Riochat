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
