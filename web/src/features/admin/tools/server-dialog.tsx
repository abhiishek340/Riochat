"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { fillHeaders, MCP_USER_CONTEXT_HEADER } from "../lib/headers";
import { isPlainObject } from "../lib/json";
import { useSnapshot } from "../lib/use-snapshot";
import type { McpServer, ServerInput } from "./api";

export interface ServerDraft {
  name: string;
  baseURL: string;
  status: boolean;
  authToken: string;
  headers: string;
}

export function draftOf(server: McpServer | null): ServerDraft {
  return {
    name: server?.name ?? "",
    baseURL: server?.baseURL ?? "",
    status: server ? server.status === "active" : true,
    authToken: "",
    headers: server?.headersJSON?.trim() && server.headersJSON.trim() !== "{}" ? server.headersJSON : "{}",
  };
}

export type ServerProblem = "nameRequired" | "addressRequired" | "headersInvalid";

export function validateServerDraft(draft: ServerDraft): ServerProblem | null {
  if (!draft.name.trim()) return "nameRequired";
  if (!draft.baseURL.trim()) return "addressRequired";
  const headers = draft.headers.trim();
  if (headers) {
    try {
      if (!isPlainObject(JSON.parse(headers))) return "headersInvalid";
    } catch {
      return "headersInvalid";
    }
  }
  return null;
}

export function inputOf(draft: ServerDraft): ServerInput {
  const input: ServerInput = {
    name: draft.name.trim(),
    baseURL: draft.baseURL.trim(),
    headersJSON: draft.headers.trim() || "{}",
    status: draft.status ? "active" : "inactive",
  };
  if (draft.authToken.trim()) input.authToken = draft.authToken.trim();
  return input;
}

export function ServerDialog({ target, onOpenChange, onSave }: { target: McpServer | "new" | null; onOpenChange: (open: boolean) => void; onSave: (input: ServerInput, existing: McpServer | null) => Promise<void> }) {
  const t = useT();
  const message = useErrorMessage();
  const open = target !== null;
  const shown = useSnapshot(target);
  const existing = shown && shown !== "new" ? shown : null;
  const [draft, setDraft] = useState<ServerDraft>(draftOf(null));
  const [problem, setProblem] = useState<ServerProblem | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // The form starts fresh every time the dialog opens.
  useEffect(() => {
    if (target === null) return;
    setDraft(draftOf(target === "new" ? null : target));
    setProblem(null);
    setError(null);
  }, [target]);

  const set = <K extends keyof ServerDraft>(key: K, value: ServerDraft[K]) => setDraft((d) => ({ ...d, [key]: value }));

  const quickFill = () => {
    const out = fillHeaders(draft.headers, MCP_USER_CONTEXT_HEADER, "strict");
    if (out.kind === "invalid") toast.error(t("adminTools.server.headersInvalid"));
    else if (out.kind === "conflict") toast.info(t("adminTools.server.headerConflict"));
    else if (out.kind === "unchanged") toast.info(t("adminTools.server.headerPresent"));
    else {
      set("headers", out.text);
      toast.success(t("adminTools.server.headerAdded"));
    }
  };

  const submit = async () => {
    if (busy) return;
    const found = validateServerDraft(draft);
    setProblem(found);
    if (found) return;
    setBusy(true);
    setError(null);
    try {
      await onSave(inputOf(draft), existing);
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{existing ? t("adminTools.server.editTitle", { name: existing.name }) : t("adminTools.server.createTitle")}</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <Field required invalid={problem === "nameRequired"}>
            <FieldLabel>{t("adminTools.server.name")}</FieldLabel>
            <Input value={draft.name} onChange={(e) => set("name", e.target.value)} autoComplete="off" />
            <FieldError>{problem === "nameRequired" ? t("adminTools.server.nameRequired") : null}</FieldError>
          </Field>
          <Field required invalid={problem === "addressRequired"}>
            <FieldLabel>{t("adminTools.server.address")}</FieldLabel>
            <Input value={draft.baseURL} onChange={(e) => set("baseURL", e.target.value)} placeholder="https://mcp.example.com/mcp" autoComplete="off" spellCheck={false} />
            <FieldError>{problem === "addressRequired" ? t("adminTools.server.addressRequired") : null}</FieldError>
          </Field>
          <Field>
            <FieldLabel>{t("adminTools.server.authToken")}</FieldLabel>
            <Input type="password" value={draft.authToken} onChange={(e) => set("authToken", e.target.value)} autoComplete="new-password" placeholder={existing ? t("adminCommon.settings.configured") : undefined} />
            <FieldHint>{t("adminTools.server.authTokenHint")}</FieldHint>
          </Field>
          <Field invalid={problem === "headersInvalid"}>
            <div className="flex items-center justify-between gap-2">
              <FieldLabel>{t("adminTools.server.headers")}</FieldLabel>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button type="button" variant="outline" size="sm">
                    {t("adminTools.server.quickFill")}
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuItem onSelect={quickFill}>{t("adminTools.server.userContext")}</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            <Textarea value={draft.headers} onChange={(e) => set("headers", e.target.value)} rows={5} spellCheck={false} className="font-mono text-[12.5px]" />
            <FieldHint>{t("adminTools.server.headersHint")}</FieldHint>
            <FieldError>{problem === "headersInvalid" ? t("adminTools.server.headersInvalid") : null}</FieldError>
          </Field>
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={draft.status} onCheckedChange={(on) => set("status", on)} />
            {t("adminTools.server.status")}
          </label>
          <FieldError>{error}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
