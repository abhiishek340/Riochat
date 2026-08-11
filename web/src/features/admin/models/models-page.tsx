"use client";

import { Activity, ArrowDownUp, ChevronRight, Layers, ListTree, MoreHorizontal, Pencil, Plus, Trash2, Zap } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { cn } from "@/lib/utils/cn";
import { BulkBar } from "../components/bulk-bar";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { ModelIcon } from "../components/model-icon";
import { AdminPage, Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { ProbeDialog } from "../components/probe-dialog";
import { AdminSection } from "../components/section-guard";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { batchDeleteInChunks, failedIds, splitBatch } from "../lib/bulk";
import { formatDateTime } from "../lib/format";
import { kindsForProtocols, kindsJson, parseKinds, parseStringList, protocolLabel, PROTOCOLS } from "../lib/protocols";
import { invalidateModels } from "../lib/reference";
import { useBulk } from "../lib/use-bulk";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSelection } from "../lib/use-selection";
import type { PlatformModel } from "../lib/wire";
import { batchDeleteModels, assignDisplayGroup, deleteModel, listDisplayGroups, listModels, listVendors, probeModel, probeModelAll, setModelProtocols, updateModel, deleteRoute, type BatchResult, type DisplayGroup, type ProbeResult, type Vendor } from "../llm-api";
import { loadBreaker, saveBreaker, type BreakerSetting } from "./api";
import { BulkDialog, type BulkAction, type BulkValue } from "./bulk-dialog";
import { parseCaps, stringifyCaps } from "./capabilities";
import { windowOf, writeCustomWindow } from "./context";
import { ModelSheet } from "./model-sheet";
import { OrderSheet } from "./order-sheet";
import { PresentationDialog } from "./presentation-dialog";
import { breakerEnabled, leavesList, protocolTargets, reloadsAfterToggle, sourceBadge, withStatus } from "./rules";
import { SourcesSheet } from "./sources-sheet";
import { SourcesTable } from "./sources-table";
import { useSources } from "./use-sources";

const SORTS = ["sortOrder_asc", "updated_desc", "id_desc", "platformModelName_asc", "sourceCount_desc"] as const;

type Confirm = { items: PlatformModel[] } | null;

export function AdminModelsPage() {
  return (
    <AdminSection>
      <ModelsContent />
    </AdminSection>
  );
}

function InlineSources({ model, breakerOn, onModel, onProbe, onChanged }: { model: PlatformModel; breakerOn: boolean; onModel: (id: number, change: (m: PlatformModel) => PlatformModel) => void; onProbe: (r: ProbeResult[]) => void; onChanged: () => void }) {
  const ctl = useSources({ model, enabled: true, breakerOn, pageSize: 10, onModel: (change) => onModel(model.id, change), onChanged });
  return <SourcesTable model={model} ctl={ctl} breakerOn={breakerOn} onProbe={onProbe} compact />;
}
