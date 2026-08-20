"use client";

import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, NativeSelect } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { MultiSelectMenu, SelectMenu } from "../components/menu-select";
import { KINDS, protocolLabel, PROTOCOLS } from "../lib/protocols";
import type { PlatformModel } from "../lib/wire";
import type { DisplayGroup, Vendor } from "../llm-api";
import { bulkWindowProblem, groupDigits, parseWindowInput } from "./context";
import { Labeled } from "./ui";

export type BulkAction = "status" | "kinds" | "protocol" | "vendor" | "group" | "window";

export type BulkValue =
  | { action: "status"; status: "active" | "inactive" }
  | { action: "kinds"; kinds: string[] }
  | { action: "protocol"; protocol: string }
  | { action: "vendor"; vendor: string }
  | { action: "group"; groupId: number }
  | { action: "window"; window: number };

/** One bulk edit of the selected models: pick the new value, confirm, apply. */
export function BulkDialog({ action, models, vendors, groups, onClose, onApply }: { action: BulkAction | null; models: PlatformModel[]; vendors: Vendor[]; groups: DisplayGroup[]; onClose: () => void; onApply: (value: BulkValue) => Promise<boolean> }) {
  const t = useT();
  const [status, setStatus] = useState<"active" | "inactive">("active");
  const [kinds, setKinds] = useState<string[]>([]);
  const [protocol, setProtocol] = useState("");
  const [vendor, setVendor] = useState("");
  const [group, setGroup] = useState("0");
  const [windowText, setWindowText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (action) setError(null);
  }, [action]);

  const vendorOptions = useMemo(() => vendors.map((v) => ({ value: v.key, label: v.name, text: `${v.name} ${v.key}` })), [vendors]);
  const groupOptions = useMemo(() => [{ value: "0", label: t("adminModels.form.followVendor") }, ...groups.map((g) => ({ value: String(g.id), label: g.name, text: g.name }))], [groups, t]);

  const value = (): BulkValue | string => {
    switch (action) {
      case "status":
        return { action, status };
      case "kinds":
        return { action, kinds };
      case "protocol":
        return protocol ? { action, protocol } : t("adminModels.bulk.problems.protocol");
      case "vendor":
        return vendor ? { action, vendor } : t("adminModels.bulk.problems.vendor");
      case "group":
        return { action: "group", groupId: Number(group) };
      case "window": {
        const problem = bulkWindowProblem(models, windowText);
        if (problem) return problem.kind === "invalid" ? t("adminModels.bulk.problems.window") : t(problem.kind === "maxOutput" ? "adminModels.bulk.problems.maxOutput" : "adminModels.bulk.problems.notObject", { model: problem.model });
        const parsed = parseWindowInput(windowText);
        return parsed.kind === "value" ? { action, window: parsed.value } : t("adminModels.bulk.problems.window");
      }
      default:
        return "";
    }
  };

  const apply = async () => {
    const v = value();
    if (typeof v === "string") {
      setError(v);
      return;
    }
    setBusy(true);
    setError(null);
    const ok = await onApply(v);
    setBusy(false);
    if (ok) onClose();
  };

  const windowParsed = parseWindowInput(windowText);
  return (
    <Dialog open={action !== null} onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{action ? t(`adminModels.bulk.titles.${action}`, { count: models.length }) : ""}</DialogTitle>
          <DialogDescription>{action === "window" && windowParsed.kind === "value" ? t("adminModels.bulk.windowConfirm", { count: models.length, value: groupDigits(windowParsed.value) }) : t("adminModels.bulk.description", { count: models.length })}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          {action === "status" ? (
            <Labeled label={t("adminModels.columns.status")}>
              <NativeSelect value={status} aria-label={t("adminModels.columns.status")} onChange={(e) => setStatus(e.target.value === "inactive" ? "inactive" : "active")}>
                <option value="active">{t("adminModels.status.active")}</option>
                <option value="inactive">{t("adminModels.status.inactive")}</option>
              </NativeSelect>
            </Labeled>
          ) : null}
          {action === "kinds" ? (
            <Labeled label={t("adminModels.form.kinds")}>
              <MultiSelectMenu values={kinds} options={KINDS.map((k) => ({ value: k, label: t(`adminModels.kinds.${k}`), text: t(`adminModels.kinds.${k}`) }))} label={t("adminModels.form.kinds")} placeholder={t("adminModels.form.noKinds")} onChange={setKinds} />
            </Labeled>
          ) : null}
          {action === "protocol" ? (
            <Labeled label={t("adminModels.columns.protocols")} hint={t("adminModels.bulk.protocolHint")}>
              <SelectMenu value={protocol} options={PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p), text: protocolLabel(p) }))} searchable label={t("adminModels.columns.protocols")} onChange={setProtocol} />
            </Labeled>
          ) : null}
          {action === "vendor" ? (
            <Labeled label={t("adminModels.form.vendor")}>
              <SelectMenu value={vendor} options={vendorOptions} searchable label={t("adminModels.form.vendor")} onChange={setVendor} />
            </Labeled>
          ) : null}
          {action === "group" ? (
            <Labeled label={t("adminModels.form.displayGroup")}>
              <SelectMenu value={group} options={groupOptions} searchable label={t("adminModels.form.displayGroup")} onChange={setGroup} />
            </Labeled>
          ) : null}
          {action === "window" ? (
            <Labeled label={t("adminModels.window.label")} hint={t("adminModels.bulk.windowHint")}>
              <Input inputMode="numeric" value={windowText} placeholder="128,000" aria-label={t("adminModels.window.label")} onChange={(e) => setWindowText(groupDigits(e.target.value))} className="tabular-nums" />
            </Labeled>
          ) : null}
          {error ? <p role="alert" className="text-[13px] text-destructive">{error}</p> : null}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={apply}>
            {t("adminCommon.common.apply")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
