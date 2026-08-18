"use client";

import { useCallback, useMemo, useState } from "react";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { invalidateModels } from "../lib/reference";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import type { PlatformModel } from "../lib/wire";
import { deleteRoute, listSources, probeRoute, routeCircuit, updateSource, upstreamCircuit, type ProbeResult, type SourceView } from "../llm-api";
import { guessCircuitUntil, parsePositiveInt, withSourceChange, withSourceRemoved } from "./rules";

export interface UseSourcesOptions {
  model: PlatformModel | null;
  /** Load only while the list is shown. */
  enabled: boolean;
  breakerOn: boolean;
  /** The model row changed (counts moved). */
  onModel: (change: (m: PlatformModel) => PlatformModel) => void;
  /** Something other than a count changed and the list behind should refresh. */
  onChanged?: () => void;
  pageSize?: number;
}

const getKey = (s: SourceView) => s.id;

/**
 * The sources of one model with inline editing: every edit shows at once and
 * rolls back with a toast when the request fails; the model's active count
 * follows the availability of the edited source.
 */
export function useSources({ model, enabled, breakerOn, onModel, onChanged, pageSize = 25 }: UseSourcesOptions) {
  const t = useT();
  const notify = useNotify();
  const modelId = model?.id ?? 0;
  const filters = useMemo(() => ({ modelId }), [modelId]);
  const list = usePagedQuery<SourceView, { modelId: number }>({
    filters,
    enabled: enabled && modelId > 0,
    initialPageSize: pageSize,
    fetch: ({ page, pageSize: size, filters: f, signal }) => listSources(f.modelId, page, size, signal),
    onError: (error) => notify.error("adminModels.toast.sourcesLoadFailed", error),
  });
  const rows = useRowUpdates<SourceView, number>({ getKey, setRows: list.setRows });
  const [probing, setProbing] = useState<number | null>(null);
  const status = model?.status ?? "inactive";

  const edit = useCallback(
    (row: SourceView, patch: Partial<SourceView>, body: Record<string, unknown>) =>
      rows.update(row, (r) => ({ ...r, ...patch }), () => updateSource(modelId, row.id, body), {
        failKey: "adminModels.toast.sourceFailed",
        onSuccess: (server) => {
          const after = (server as SourceView | void) ?? { ...row, ...patch };
          onModel((m) => withSourceChange(m, row, after, breakerOn));
          invalidateModels();
        },
      }),
    [rows, modelId, onModel, breakerOn],
  );

  const toggle = (row: SourceView, on: boolean) => edit(row, { status: on ? "active" : "inactive" }, { status: on ? "active" : "inactive" });

  /** Priority and weight are positive integers; anything else reverts with a message. */
  const setNumber = (row: SourceView, field: "priority" | "weight", text: string): boolean => {
    const n = parsePositiveInt(text);
    if (n === null) {
      toast.error(t("adminModels.sources.numberInvalid"));
      return false;
    }
    if (n === row[field]) return true;
    void edit(row, { [field]: n }, { [field]: n });
    return true;
  };

  const setProtocol = (row: SourceView, protocol: string) => {
    if (protocol && protocol !== row.protocol) void edit(row, { protocol }, { protocol });
  };

  const setBreaker = (row: SourceView, values: { cbFailureThreshold: number; cbDurationMin: number; cbWindowMin: number }) => edit(row, values, values);

  const openCircuit = async (row: SourceView) => {
    // The circuit is shown open for a day until the next load says otherwise.
    const guess = { circuitOpen: true, circuitUntil: guessCircuitUntil(Date.now()), circuitScope: "route" };
    const ok = await rows.update(row, (r) => ({ ...r, ...guess }), async () => {
      await routeCircuit(row.upstreamID, row.id, "open");
    }, { failKey: "adminModels.toast.circuitFailed", onSuccess: () => onModel((m) => withSourceChange(m, row, { ...row, ...guess }, breakerOn)) });
    if (ok) {
      invalidateModels();
      list.reload();
    }
  };

  /** Resetting resets the whole upstream when that is where the circuit is open. */
  const resetCircuit = async (row: SourceView) => {
    const cleared = { circuitOpen: false, circuitUntil: "" };
    const ok = await rows.update(row, (r) => ({ ...r, ...cleared }), async () => {
      if (row.circuitScope === "upstream") await upstreamCircuit(row.upstreamID, "reset");
      else await routeCircuit(row.upstreamID, row.id, "reset");
    }, { failKey: "adminModels.toast.circuitFailed", onSuccess: () => onModel((m) => withSourceChange(m, row, { ...row, ...cleared }, breakerOn)) });
    if (ok) {
      invalidateModels();
      list.reload();
    }
  };

  const remove = async (row: SourceView) => {
    await deleteRoute(row.upstreamID, row.id);
    invalidateModels();
    toast.success(t("adminModels.toast.sourceDeleted"));
    onModel((m) => withSourceRemoved(m, row, breakerOn));
    if (list.rows.length <= 1 && list.page > 1) list.setPage(list.page - 1);
    else list.reload();
    onChanged?.();
  };

  const probe = async (row: SourceView): Promise<ProbeResult[] | null> => {
    setProbing(row.id);
    try {
      return [await probeRoute(row.upstreamID, row.id)];
    } catch (error) {
      notify.error("adminModels.toast.probeFailed", error);
      return null;
    } finally {
      setProbing(null);
    }
  };

  return { list, status, toggle, setNumber, setProtocol, setBreaker, openCircuit, resetCircuit, remove, probe, probing, isPending: rows.isPending };
}

export type SourcesController = ReturnType<typeof useSources>;
