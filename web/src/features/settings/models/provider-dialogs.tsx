"use client";

import { ListChecks } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import {
  createProvider,
  fetchProviderModels,
  probeProvider,
  updateProvider,
  type AvailableModel,
  type PersonalProvider,
  type ProviderAccess,
  type ProviderSource,
  type UpdateInput,
} from "./api";
import { IconPicker } from "./icon-picker";
import { ModelPicker } from "./model-picker";
import { mergeAvailable, modelsChanged, preselect, toRequestModels, type SelectedModel } from "./selection";
import { recognizeProvider, resolveProtocol } from "./well-known";

export interface ProviderDraft {
  name?: string;
  baseURL?: string;
  apiKey?: string;
  protocol?: string;
}

/** Button-like field showing up to two chips of the chosen models. */
function ModelsField({ selection, disabled, onOpen }: { selection: SelectedModel[]; disabled?: boolean; onOpen: () => void }) {
  const t = useT();
  const shown = selection.slice(0, 2);
  const more = selection.length - shown.length;
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onOpen}
      aria-haspopup="dialog"
      className="flex min-h-9 w-full items-center gap-2 rounded-md border border-input bg-background px-3 py-1.5 text-left text-sm shadow-sm outline-none transition-colors hover:bg-accent/40 focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50"
    >
      <ListChecks className="size-4 shrink-0 text-muted-foreground" />
      {selection.length === 0 ? (
        <span className="text-muted-foreground">{t("settings.models.form.modelsPlaceholder")}</span>
      ) : (
        <span className="flex min-w-0 flex-wrap items-center gap-1.5">
          {shown.map((m) => (
            <Badge key={m.name} tone="neutral" className="max-w-40 truncate font-mono">
              {m.name}
            </Badge>
          ))}
          {more > 0 ? <Badge tone="outline">+{more}</Badge> : null}
        </span>
      )}
    </button>
  );
}

type ProbeState =
  | { status: "idle"; models: AvailableModel[]; error: null }
  | { status: "loading"; models: AvailableModel[]; error: null }
  | { status: "ready"; models: AvailableModel[]; error: null }
  | { status: "error"; models: AvailableModel[]; error: string };

const IDLE: ProbeState = { status: "idle", models: [], error: null };

export function AddProviderDialog({
  open,
  onOpenChange,
  access,
  source,
  locked = false,
  initial,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  access: ProviderAccess;
  source: ProviderSource;
  /** Import flows fix the endpoint. */
  locked?: boolean;
  initial?: ProviderDraft;
  onCreated: (provider: PersonalProvider) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [name, setName] = useState("");
  const [icon, setIcon] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [probe, setProbe] = useState<ProbeState>(IDLE);
  const [selection, setSelection] = useState<SelectedModel[]>([]);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const abort = useRef<AbortController | null>(null);

  // Every opening starts from the given draft.
  useEffect(() => {
    if (!open) return;
    abort.current?.abort();
    setName(initial?.name ?? "");
    setIcon("");
    setBaseURL(initial?.baseURL ?? "");
    setApiKey(initial?.apiKey ?? "");
    setProbe(IDLE);
    setSelection([]);
    setPickerOpen(false);
    setSaving(false);
    setError(null);
    // The draft is read at open time only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => () => abort.current?.abort(), []);

  const protocol = resolveProtocol(baseURL, initial?.protocol);
  const known = recognizeProvider(baseURL);
  const perModel = access.modelProtocols.length > 0;
  const ready = baseURL.trim() !== "" && apiKey.trim() !== "";

  const discardProbe = () => {
    abort.current?.abort();
    setProbe(IDLE);
    setSelection([]);
  };

  const runProbe = async () => {
    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;
    setProbe((p) => ({ status: "loading", models: p.models, error: null }));
    try {
      const models = await probeProvider({ protocol, baseURL: baseURL.trim(), apiKey: apiKey.trim() }, controller.signal);
      if (controller.signal.aborted) return;
      setProbe({ status: "ready", models, error: null });
      setSelection((current) => {
        // First probe: pre-select small catalogs. A refresh keeps what is still listed.
        if (current.length === 0 && probe.status !== "ready") return preselect(models);
        const names = new Set(models.map((m) => m.name));
        return current.filter((m) => names.has(m.name));
      });
    } catch (e) {
      if (controller.signal.aborted || isAbortError(e)) return;
      setProbe((p) => ({ status: "error", models: p.models, error: errorMessage(e) }));
    }
  };

  const openPicker = () => {
    setPickerOpen(true);
    if (probe.status === "idle" || probe.status === "error") void runProbe();
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (saving || probe.status !== "ready" || selection.length === 0) return;
    setSaving(true);
    setError(null);
    try {
      const provider = await createProvider({
        name: name.trim() || known?.name || undefined,
        icon: icon || undefined,
        protocol,
        baseURL: baseURL.trim(),
        apiKey: apiKey.trim(),
        models: toRequestModels(selection, perModel),
        source,
      });
      onCreated(provider);
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err, t("settings.models.form.saveFailed")));
    } finally {
      setSaving(false);
    }
  };

  const canSave = probe.status === "ready" && selection.length > 0 && !saving;

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => (saving ? undefined : onOpenChange(next))}>
        <DialogContent className="max-w-lg">
          <form onSubmit={(e) => void save(e)} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{locked ? t("settings.models.form.importTitle") : t("settings.models.form.addTitle")}</DialogTitle>
              <DialogDescription>{t("settings.models.form.description")}</DialogDescription>
            </DialogHeader>

            <Field>
              <FieldLabel>{t("settings.models.form.name")}</FieldLabel>
              <Input value={name} maxLength={64} autoComplete="off" disabled={saving} placeholder={known?.name ?? t("settings.models.form.namePlaceholder")} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("settings.models.form.icon")}</FieldLabel>
              <IconPicker value={icon} onChange={setIcon} automaticSlug={known?.icon} disabled={saving} />
            </Field>
            <Field required>
              <FieldLabel>{t("settings.models.form.baseURL")}</FieldLabel>
              <Input
                value={baseURL}
                readOnly={locked}
                disabled={saving}
                inputMode="url"
                autoComplete="off"
                spellCheck={false}
                placeholder="https://api.example.com/v1"
                onChange={(e) => {
                  setBaseURL(e.target.value);
                  discardProbe();
                }}
              />
              <FieldHint>{locked ? t("settings.models.form.baseURLLocked") : t("settings.models.form.baseURLHint")}</FieldHint>
            </Field>
            <Field required>
              <FieldLabel>{t("settings.models.form.apiKey")}</FieldLabel>
              <Input
                type="password"
                value={apiKey}
                disabled={saving}
                autoComplete="new-password"
                spellCheck={false}
                placeholder="sk-…"
                onChange={(e) => {
                  setApiKey(e.target.value);
                  discardProbe();
                }}
              />
              <FieldHint>{t("settings.models.form.apiKeyHint")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("settings.models.form.models")}</FieldLabel>
              <ModelsField selection={selection} disabled={!ready || saving} onOpen={openPicker} />
              {!ready ? <FieldHint>{t("settings.models.form.modelsNeedKey")}</FieldHint> : null}
            </Field>
            <FieldError>{error}</FieldError>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
                {t("common.actions.cancel")}
              </Button>
              <Button type="submit" loading={saving} disabled={!canSave}>
                {t("common.actions.save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ModelPicker
        open={pickerOpen}
        onOpenChange={setPickerOpen}
        available={probe.models}
        loading={probe.status === "loading"}
        error={probe.status === "error" ? probe.error : null}
        selected={selection}
        offeredProtocols={access.modelProtocols}
        fallbackProtocol={protocol}
        onRefresh={() => void runProbe()}
        onConfirm={setSelection}
      />
    </>
  );
}

export function EditProviderDialog({
  provider,
  onOpenChange,
  access,
  onSaved,
}: {
  provider: PersonalProvider | null;
  onOpenChange: (open: boolean) => void;
  access: ProviderAccess;
  onSaved: (provider: PersonalProvider) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const open = provider !== null;
  const [name, setName] = useState("");
  const [icon, setIcon] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [selection, setSelection] = useState<SelectedModel[]>([]);
  const [live, setLive] = useState<AvailableModel[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const seededFor = useRef<string | null>(null);
  const providerRef = useRef(provider);
  if (provider) providerRef.current = provider;
  const shown = provider ?? providerRef.current;

  const refresh = async (target: PersonalProvider, signal?: AbortSignal) => {
    setLoading(true);
    setLoadError(null);
    try {
      setLive(await fetchProviderModels(target.id, target.protocol, signal));
    } catch (e) {
      if (signal?.aborted || isAbortError(e)) return;
      setLoadError(errorMessage(e));
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  };

  // Form state is re-seeded only when a different provider is opened; a list
  // refresh keeps whatever has been edited.
  useEffect(() => {
    if (!provider) {
      seededFor.current = null;
      return;
    }
    if (seededFor.current === provider.id) return;
    seededFor.current = provider.id;
    setName(provider.name);
    setIcon(provider.icon);
    setApiKey("");
    setSelection(provider.models.map((m) => ({ name: m.name, protocols: [...m.protocols] })));
    setLive([]);
    setPickerOpen(false);
    const controller = new AbortController();
    void refresh(provider, controller.signal);
    return () => {
      // A re-run for the same provider (development double effects, quick
      // reopen) must seed and load again, not trust the aborted attempt.
      controller.abort();
      seededFor.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [provider?.id]);

  const available = shown ? mergeAvailable(live, shown.models) : [];
  const perModel = access.modelProtocols.length > 0;
  const known = shown ? recognizeProvider(shown.baseURL) : undefined;

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!provider || saving || loading || selection.length === 0) return;
    const body: UpdateInput = {};
    const nextName = name.trim();
    if (nextName && nextName !== provider.name) body.name = nextName;
    if (icon !== provider.icon) body.icon = icon;
    if (apiKey.trim()) body.apiKey = apiKey.trim();
    if (modelsChanged(provider.models, selection)) body.models = toRequestModels(selection, perModel);
    if (Object.keys(body).length === 0) {
      onOpenChange(false);
      return;
    }
    setSaving(true);
    try {
      const next = await updateProvider(provider.id, body);
      onSaved(next);
      toast.success(t("settings.models.toasts.saved"));
      onOpenChange(false);
    } catch (err) {
      toast.error(t("settings.models.toasts.saveFailed"), { description: errorMessage(err) });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => (saving ? undefined : onOpenChange(next))}>
        <DialogContent className="max-w-lg">
          <form onSubmit={(e) => void save(e)} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.models.form.editTitle")}</DialogTitle>
              <DialogDescription>{t("settings.models.form.editDescription")}</DialogDescription>
            </DialogHeader>
            <Field>
              <FieldLabel>{t("settings.models.form.name")}</FieldLabel>
              <Input value={name} maxLength={64} autoComplete="off" disabled={saving} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("settings.models.form.icon")}</FieldLabel>
              <IconPicker value={icon} onChange={setIcon} automaticSlug={known?.icon} disabled={saving} />
            </Field>
            <Field>
              <FieldLabel>{t("settings.models.form.baseURL")}</FieldLabel>
              <Input value={shown?.baseURL ?? ""} readOnly disabled className="font-mono text-[13px]" />
              <FieldHint>{t("settings.models.form.baseURLLocked")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("settings.models.form.rotateKey")}</FieldLabel>
              <Input
                type="password"
                value={apiKey}
                disabled={saving}
                autoComplete="new-password"
                spellCheck={false}
                placeholder={shown?.keyHint ? t("settings.models.form.keyKept", { hint: shown.keyHint }) : t("settings.models.form.keyKeptPlain")}
                onChange={(e) => setApiKey(e.target.value)}
              />
              <FieldHint>{t("settings.models.form.rotateKeyHint")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("settings.models.form.models")}</FieldLabel>
              <ModelsField selection={selection} disabled={saving} onOpen={() => setPickerOpen(true)} />
              {loadError ? <FieldHint className="text-warning-foreground dark:text-warning">{t("settings.models.form.listFailed", { reason: loadError })}</FieldHint> : null}
            </Field>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
                {t("common.actions.cancel")}
              </Button>
              <Button type="submit" loading={saving} disabled={loading || selection.length === 0}>
                {t("common.actions.save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ModelPicker
        open={pickerOpen && open}
        onOpenChange={setPickerOpen}
        available={available}
        loading={loading}
        error={loadError}
        selected={selection}
        offeredProtocols={access.modelProtocols}
        fallbackProtocol={shown?.protocol ?? ""}
        onRefresh={provider ? () => void refresh(provider) : undefined}
        onConfirm={setSelection}
      />
    </>
  );
}
