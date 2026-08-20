"use client";

import { Pencil, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast-store";
import { ApiError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { MultiSelectMenu } from "../components/menu-select";
import { ModelIcon } from "../components/model-icon";
import { invalidateModels, loadAllModels } from "../lib/reference";
import { useNotify } from "../lib/use-notify";
import type { PlatformModel } from "../lib/wire";
import { createDisplayGroup, createVendor, deleteDisplayGroup, deleteVendor, updateDisplayGroup, updateVendor, type DisplayGroup, type Vendor } from "../llm-api";
import { IconField } from "./icon-picker";
import { normalizeVendorKey, readVendorUsage, validateVendor } from "./rules";

export function PresentationDialog({ open, onOpenChange, vendors, groups, onChanged }: { open: boolean; onOpenChange: (open: boolean) => void; vendors: Vendor[]; groups: DisplayGroup[]; onChanged: () => void }) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("adminModels.presentation.title")}</DialogTitle>
          <DialogDescription>{t("adminModels.presentation.description")}</DialogDescription>
        </DialogHeader>
        <Tabs defaultValue="vendors">
          <TabsList>
            <TabsTrigger value="vendors">{t("adminModels.presentation.vendors")}</TabsTrigger>
            <TabsTrigger value="groups">{t("adminModels.presentation.groups")}</TabsTrigger>
          </TabsList>
          <TabsContent value="vendors">
            <VendorsTab vendors={vendors} onChanged={onChanged} />
          </TabsContent>
          <TabsContent value="groups">{open ? <GroupsTab groups={groups} onChanged={onChanged} /> : null}</TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

function VendorsTab({ vendors, onChanged }: { vendors: Vendor[]; onChanged: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const [editing, setEditing] = useState<Vendor | "new" | null>(null);
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [icon, setIcon] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState<Vendor | null>(null);

  const open = (target: Vendor | "new") => {
    setEditing(target);
    setError(null);
    setKey(target === "new" ? "" : target.key);
    setName(target === "new" ? "" : target.name);
    setIcon(target === "new" ? "" : target.icon);
  };

  const save = async () => {
    if (!editing) return;
    const creating = editing === "new";
    const issue = validateVendor({ key, name }, creating);
    if (issue) {
      setError(t(`adminModels.presentation.problems.${issue === "key" ? "vendorKey" : "name"}`));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (creating) await createVendor({ key: normalizeVendorKey(key), name: name.trim(), icon: icon.trim() });
      else await updateVendor(editing.key, { name: name.trim(), icon: icon.trim() });
      invalidateModels();
      toast.success(t("adminModels.presentation.saved"));
      setEditing(null);
      onChanged();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!removing) return;
    try {
      await deleteVendor(removing.key);
    } catch (e) {
      if (e instanceof ApiError && e.errorCode === "llm.model_vendor_in_use") {
        const usage = readVendorUsage(e.details);
        const text = t("adminModels.presentation.vendorInUse", { count: usage.count, names: usage.names.join(", "), more: usage.more });
        toast.error(text);
        throw new Error(text);
      }
      throw e;
    }
    invalidateModels();
    toast.success(t("adminModels.presentation.deleted"));
    onChanged();
  };

  return (
    <div className="space-y-4">
      {editing ? (
        <div className="space-y-3 rounded-lg border p-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <Field required>
              <FieldLabel>{t("adminModels.presentation.key")}</FieldLabel>
              <Input value={key} disabled={editing !== "new"} onChange={(e) => setKey(e.target.value)} spellCheck={false} autoComplete="off" />
              {editing === "new" ? <FieldHint>{t("adminModels.presentation.keyHint")}</FieldHint> : null}
            </Field>
            <Field required>
              <FieldLabel>{t("adminModels.presentation.name")}</FieldLabel>
              <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
            </Field>
          </div>
          <IconField value={icon} onChange={setIcon} name={name || key} label={t("adminModels.presentation.icon")} />
          {error ? <FieldError>{error}</FieldError> : null}
          <div className="flex justify-end gap-2">
            <Button variant="outline" disabled={busy} onClick={() => setEditing(null)}>
              {t("common.actions.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {t("common.actions.save")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex justify-end">
          <Button size="sm" onClick={() => open("new")}>
            <Plus />
            {t("adminModels.presentation.addVendor")}
          </Button>
        </div>
      )}
      <ul className="divide-y rounded-lg border">
        {vendors.map((v) => (
          <li key={v.key} className="flex items-center gap-3 px-3 py-2">
            <ModelIcon icon={v.icon} name={v.name} size={24} />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate text-sm font-medium">{v.name}</span>
                {v.builtIn ? <Badge tone="outline">{t("adminModels.presentation.builtIn")}</Badge> : null}
              </div>
              <div className="font-mono text-xs text-muted-foreground">{v.key}</div>
            </div>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${v.name}`} onClick={() => open(v)}>
              <Pencil />
            </Button>
            <Button variant="ghost" size="icon-sm" disabled={v.builtIn} aria-label={`${t("common.actions.delete")}: ${v.name}`} onClick={() => setRemoving(v)}>
              <Trash2 className={v.builtIn ? undefined : "text-destructive"} />
            </Button>
          </li>
        ))}
      </ul>
      <ConfirmDialog open={removing !== null} onOpenChange={(next) => !next && setRemoving(null)} title={t("adminModels.presentation.deleteVendorTitle", { name: removing?.name ?? "" })} description={t("adminModels.presentation.deleteVendorBody")} confirmLabel={t("common.actions.delete")} destructive onConfirm={remove} />
    </div>
  );
}

function GroupsTab({ groups, onChanged }: { groups: DisplayGroup[]; onChanged: () => void }) {
  const t = useT();
  const notify = useNotify();
  const message = useErrorMessage();
  const [models, setModels] = useState<PlatformModel[] | null>(null);
  const [editing, setEditing] = useState<DisplayGroup | "new" | null>(null);
  const [name, setName] = useState("");
  const [icon, setIcon] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [membersEdited, setMembersEdited] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState<DisplayGroup | null>(null);

  useEffect(() => {
    let live = true;
    loadAllModels().then(
      (list) => live && setModels(list),
      (e: unknown) => {
        if (live) {
          setModels([]);
          notify.error("adminModels.toast.loadFailed", e);
        }
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const options = useMemo(() => (models ?? []).map((m) => ({ value: String(m.id), label: m.platformModelName, text: m.platformModelName })), [models]);

  const open = (target: DisplayGroup | "new") => {
    setEditing(target);
    setError(null);
    setName(target === "new" ? "" : target.name);
    setIcon(target === "new" ? "" : target.icon);
    setMembers(target === "new" || !models ? [] : models.filter((m) => m.displayGroupID === target.id).map((m) => String(m.id)));
    setMembersEdited(false);
  };

  const save = async () => {
    if (!editing) return;
    if (name.trim() === "") {
      setError(t("adminModels.presentation.problems.name"));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const ids = members.map(Number);
      if (editing === "new") await createDisplayGroup({ name: name.trim(), icon: icon.trim(), modelIDs: ids });
      else await updateDisplayGroup(editing.id, { name: name.trim(), icon: icon.trim(), ...(membersEdited ? { modelIDs: ids } : {}) });
      invalidateModels();
      void loadAllModels().then(setModels, () => undefined);
      toast.success(t("adminModels.presentation.saved"));
      setEditing(null);
      onChanged();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!removing) return;
    try {
      await deleteDisplayGroup(removing.id);
    } catch (e) {
      notify.error("adminModels.presentation.deleteFailed", e);
      throw e;
    }
    invalidateModels();
    void loadAllModels().then(setModels, () => undefined);
    toast.success(t("adminModels.presentation.deleted"));
    onChanged();
  };

  if (models === null) return <Skeleton className="h-32 w-full" />;
  return (
    <div className="space-y-4">
      {editing ? (
        <div className="space-y-3 rounded-lg border p-3">
          <Field required>
            <FieldLabel>{t("adminModels.presentation.name")}</FieldLabel>
            <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <IconField value={icon} onChange={setIcon} name={name} label={t("adminModels.presentation.icon")} />
          <div className="grid gap-1.5">
            <span className="text-sm font-medium">{t("adminModels.presentation.members")}</span>
            <MultiSelectMenu
              values={members}
              options={options}
              searchable
              label={t("adminModels.presentation.members")}
              placeholder={t("adminModels.presentation.noMembers")}
              onChange={(next) => {
                setMembers(next);
                setMembersEdited(true);
              }}
            />
            <p className="text-[13px] text-muted-foreground">{t("adminModels.presentation.membersHint")}</p>
          </div>
          {error ? <FieldError>{error}</FieldError> : null}
          <div className="flex justify-end gap-2">
            <Button variant="outline" disabled={busy} onClick={() => setEditing(null)}>
              {t("common.actions.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {t("common.actions.save")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex justify-end">
          <Button size="sm" onClick={() => open("new")}>
            <Plus />
            {t("adminModels.presentation.addGroup")}
          </Button>
        </div>
      )}
      {groups.length === 0 ? (
        <p className="rounded-lg border border-dashed p-4 text-center text-sm text-muted-foreground">{t("adminModels.presentation.noGroups")}</p>
      ) : (
        <ul className="divide-y rounded-lg border">
          {groups.map((g) => (
            <li key={g.id} className="flex items-center gap-3 px-3 py-2">
              <ModelIcon icon={g.icon} name={g.name} size={24} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{g.name}</div>
                <div className="text-xs text-muted-foreground">{t("adminModels.presentation.memberCount", { count: models.filter((m) => m.displayGroupID === g.id).length })}</div>
              </div>
              <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${g.name}`} onClick={() => open(g)}>
                <Pencil />
              </Button>
              <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.delete")}: ${g.name}`} onClick={() => setRemoving(g)}>
                <Trash2 className="text-destructive" />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog open={removing !== null} onOpenChange={(next) => !next && setRemoving(null)} title={t("adminModels.presentation.deleteGroupTitle", { name: removing?.name ?? "" })} description={t("adminModels.presentation.deleteGroupBody")} confirmLabel={t("common.actions.delete")} destructive onConfirm={remove} />
    </div>
  );
}
