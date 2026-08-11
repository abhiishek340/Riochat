"use client";

import { Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { SelectMenu } from "../components/menu-select";
import { cachedLoad } from "../lib/reference";
import { protocolLabel } from "../lib/protocols";
import { listAllUpstreams, type Upstream } from "../llm-api";
import { loadUpstreamModels, type UpstreamModelOption } from "./api";
import { protocolChoices, type SourceDraft } from "./rules";
import { Labeled } from "./ui";

export function loadUpstreamChoices(): Promise<Upstream[]> {
  return cachedLoad("models-upstreams", () => listAllUpstreams());
}

/** Upstreams for the pickers; they load once per sheet. */
export function useUpstreamChoices(enabled = true): Upstream[] {
  const [list, setList] = useState<Upstream[]>([]);
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    loadUpstreamChoices().then(
      (u) => live && setList(u),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [enabled]);
  return list;
}

function useUpstreamModels(upstreamId: number, q: string) {
  const [options, setOptions] = useState<UpstreamModelOption[]>([]);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    if (upstreamId <= 0) {
      setOptions([]);
      return;
    }
    const controller = new AbortController();
    const id = setTimeout(() => {
      setLoading(true);
      loadUpstreamModels(upstreamId, q, controller.signal).then(
        (list) => {
          setOptions(list);
          setLoading(false);
        },
        (error: unknown) => {
          if (isAbortError(error)) return;
          setOptions([]);
          setLoading(false);
        },
      );
    }, q ? 250 : 0);
    return () => {
      clearTimeout(id);
      controller.abort();
    };
  }, [upstreamId, q]);
  return { options, loading };
}

/**
 * One source being chosen: upstream, one of its models (searchable on the
 * server), the protocol (suggested from the model) and the routing numbers.
 */
export function SourcePicker({ row, onChange, kinds, upstreams, onRemove, disabled }: { row: SourceDraft; onChange: (patch: Partial<SourceDraft>) => void; kinds: readonly string[]; upstreams: readonly Upstream[]; onRemove?: () => void; disabled?: boolean }) {
  const t = useT();
  const [q, setQ] = useState("");
  const { options, loading } = useUpstreamModels(row.upstreamID, q);
  const upstreamOptions = useMemo(() => upstreams.map((u) => ({ value: String(u.id), label: u.name, text: u.name, hint: u.status === "active" ? undefined : t("adminModels.sources.upstreamOff") })), [upstreams, t]);
  const modelOptions = useMemo(() => options.map((m) => ({ value: String(m.id), label: m.name, text: m.name })), [options]);
  const protocols = protocolChoices(kinds, row.protocol);

  return (
    <div className="grid gap-3 rounded-lg border p-3 sm:grid-cols-6">
      <Labeled label={t("adminModels.sources.columns.upstream")} className="sm:col-span-2">
        <SelectMenu
          value={row.upstreamID > 0 ? String(row.upstreamID) : ""}
          options={upstreamOptions}
          searchable
          disabled={disabled}
          label={t("adminModels.sources.columns.upstream")}
          placeholder={t("adminModels.sources.pickUpstream")}
          onChange={(v) => {
            onChange({ upstreamID: Number(v), upstreamModelID: 0, protocol: "" });
            setQ("");
          }}
        />
      </Labeled>
      <Labeled group label={t("adminModels.sources.columns.upstreamModel")} className="sm:col-span-3">
        <div className="flex gap-2">
          <Input value={q} disabled={disabled || row.upstreamID <= 0} onChange={(e) => setQ(e.target.value)} placeholder={t("adminModels.sources.searchModels")} aria-label={t("adminModels.sources.searchModels")} className="h-9 w-28 shrink-0" />
          <SelectMenu
            value={row.upstreamModelID > 0 ? String(row.upstreamModelID) : ""}
            options={modelOptions}
            disabled={disabled || row.upstreamID <= 0}
            label={t("adminModels.sources.columns.upstreamModel")}
            placeholder={loading ? t("common.states.loading") : t("adminModels.sources.pickModel")}
            emptyText={t("adminModels.sources.noModels")}
            onChange={(v) => {
              const picked = options.find((m) => String(m.id) === v);
              onChange({ upstreamModelID: Number(v), protocol: picked?.suggestedProtocol && protocols.includes(picked.suggestedProtocol) ? picked.suggestedProtocol : row.protocol });
            }}
          />
        </div>
      </Labeled>
      <div className="flex items-end justify-end sm:col-span-1">
        {onRemove ? (
          <Button variant="ghost" size="icon-sm" disabled={disabled} aria-label={t("adminModels.sources.removeRow")} onClick={onRemove}>
            <Trash2 className="text-destructive" />
          </Button>
        ) : null}
      </div>
      <Labeled label={t("adminModels.sources.columns.protocol")} className="sm:col-span-3">
        <SelectMenu
          value={row.protocol}
          options={protocols.map((p) => ({ value: p, label: protocolLabel(p), text: protocolLabel(p) }))}
          disabled={disabled}
          label={t("adminModels.sources.columns.protocol")}
          placeholder={t("adminModels.sources.pickProtocol")}
          onChange={(v) => onChange({ protocol: v })}
        />
      </Labeled>
      <Labeled label={t("adminModels.sources.columns.priority")} className="sm:col-span-1">
        <Input inputMode="numeric" value={row.priority} disabled={disabled} onChange={(e) => onChange({ priority: e.target.value })} className="tabular-nums" />
      </Labeled>
      <Labeled label={t("adminModels.sources.columns.weight")} className="sm:col-span-1">
        <Input inputMode="numeric" value={row.weight} disabled={disabled} onChange={(e) => onChange({ weight: e.target.value })} className="tabular-nums" />
      </Labeled>
    </div>
  );
}
