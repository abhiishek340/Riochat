"use client";

import { Download } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import { AdminPage, Section } from "../components/page";
import { AdminSection } from "../components/section-guard";
import { SettingsGroup } from "../components/settings-group";
import { downloadBlob, fileNameFromDisposition } from "../lib/download";
import type { Draft, FieldDef, FieldOption } from "../lib/settings-form";
import { useNotify } from "../lib/use-notify";
import { useSettingsForm } from "../lib/use-settings-form";
import { downloadExport, listAvailableModels } from "./api";
import { LIBRARY_FIELDS, PromptLibrary } from "./library";
import { OPTION_FIELDS, OptionPolicy } from "./option-policy";
import { chatModelNames, exportFallbackName, normalizeFollow, normalizePromptValue } from "./rules";

const NS = "chat";
const FOLLOW_IDS = [`${NS}.conversation_task_model`, `${NS}.compact_task_model`];
const PROMPT_IDS = [`${NS}.conversation_title_prompt`, `${NS}.conversation_labels_prompt`, `${NS}.default_system_prompt`, `${NS}.compact_system_prompt`, `${NS}.compact_light_prompt`];
const COMPACT = `${NS}.context_compact_enabled`;
const LLM = `${NS}.compact_llm_enabled`;
const compactOn = (d: Draft) => d[COMPACT] === "true";
const llmOn = (d: Draft) => compactOn(d) && d[LLM] === "true";

function basics(models: FieldOption[]): FieldDef[] {
  return [
    { ns: NS, key: "conversation_default_model", type: "select", label: "adminConversation.basics.defaultModel", hint: "adminConversation.basics.defaultModelHint", def: "", options: [{ value: "", labelKey: "adminConversation.basics.recommended" }, ...models] },
    { ns: NS, key: "conversation_task_model", type: "select", label: "adminConversation.basics.taskModel", hint: "adminConversation.basics.taskModelHint", def: "follow", blankDefault: true, options: [{ value: "follow", labelKey: "adminConversation.basics.follow" }, ...models] },
    { ns: NS, key: "conversation_title_prompt", type: "textarea", label: "adminConversation.basics.titlePrompt", hint: "adminConversation.basics.titlePromptHint", def: "" },
    { ns: NS, key: "conversation_labels_prompt", type: "textarea", label: "adminConversation.basics.labelsPrompt", hint: "adminConversation.basics.labelsPromptHint", def: "" },
    { ns: NS, key: "default_system_prompt", type: "textarea", label: "adminConversation.basics.systemPrompt", hint: "adminConversation.basics.systemPromptHint", def: "" },
    { ns: NS, key: "ui_components_enabled", type: "bool", label: "adminConversation.basics.uiComponents", hint: "adminConversation.basics.uiComponentsHint", def: "true" },
    { ns: NS, key: "process_trace_enabled", type: "bool", label: "adminConversation.basics.processTrace", hint: "adminConversation.basics.processTraceHint", def: "true" },
  ];
}

function context(models: FieldOption[]): FieldDef[] {
  const task: FieldOption[] = [{ value: "follow", labelKey: "adminConversation.basics.follow" }, ...models];
  return [
    { ns: NS, key: "context_window_fallback_tokens", type: "int", label: "adminConversation.context.fallbackWindow", hint: "adminConversation.context.fallbackWindowHint", def: "128000", blankDefault: true },
    { ns: NS, key: "context_token_budget_enabled", type: "bool", label: "adminConversation.context.tokenBudget", hint: "adminConversation.context.tokenBudgetHint", def: "true" },
    { ns: NS, key: "context_compact_enabled", type: "bool", label: "adminConversation.context.compact", hint: "adminConversation.context.compactHint", def: "false" },
    { ns: NS, key: "context_max_turns", type: "int", label: "adminConversation.context.maxTurns", hint: "adminConversation.context.maxTurnsHint", def: "48", visible: compactOn },
    { ns: NS, key: "context_compact_trigger_percent", type: "int", label: "adminConversation.context.trigger", hint: "adminConversation.context.triggerHint", def: "80", suffix: "%", visible: compactOn },
    { ns: NS, key: "context_compact_preserve_recent_turns", type: "int", label: "adminConversation.context.preserve", hint: "adminConversation.context.preserveHint", def: "8", visible: compactOn },
    { ns: NS, key: "context_compact_highlights_per_role", type: "int", label: "adminConversation.context.highlights", hint: "adminConversation.context.highlightsHint", def: "6", visible: compactOn },
    { ns: NS, key: "context_compact_snippet_chars", type: "int", label: "adminConversation.context.snippet", hint: "adminConversation.context.snippetHint", def: "140", visible: compactOn },
    { ns: NS, key: "context_artifact_retention_days", type: "int", label: "adminConversation.context.retention", hint: "adminConversation.context.retentionHint", def: "90", blankDefault: true, visible: compactOn },
    { ns: NS, key: "compact_async_enabled", type: "bool", label: "adminConversation.context.async", hint: "adminConversation.context.asyncHint", def: "true", visible: compactOn },
    { ns: NS, key: "compact_llm_enabled", type: "bool", label: "adminConversation.context.llm", hint: "adminConversation.context.llmHint", def: "true", visible: compactOn },
    { ns: NS, key: "compact_task_model", type: "select", label: "adminConversation.context.taskModel", hint: "adminConversation.context.taskModelHint", def: "follow", blankDefault: true, options: task, visible: llmOn },
    { ns: NS, key: "compact_max_failures", type: "int", label: "adminConversation.context.maxFailures", hint: "adminConversation.context.maxFailuresHint", def: "3", visible: llmOn },
    { ns: NS, key: "compact_system_prompt", type: "textarea", label: "adminConversation.context.systemPrompt", hint: "adminConversation.context.systemPromptHint", def: "", visible: llmOn },
    { ns: NS, key: "compact_light_prompt", type: "textarea", label: "adminConversation.context.lightPrompt", hint: "adminConversation.context.lightPromptHint", def: "", visible: llmOn },
  ];
}

export function AdminConversationPage() {
  return (
    <AdminSection>
      <ConversationContent />
    </AdminSection>
  );
}

function ConversationContent() {
  const t = useT();
  const notify = useNotify();
  const policy = useFeaturePolicy();
  const [modelNames, setModelNames] = useState<string[]>([]);

  // The model selects are best effort: a failed lookup leaves only the special first option.
  useEffect(() => {
    const controller = new AbortController();
    listAvailableModels(controller.signal).then(
      (rows) => setModelNames(chatModelNames(rows)),
      (error: unknown) => {
        if (!isAbortError(error)) setModelNames([]);
      },
    );
    return () => controller.abort();
  }, []);

  const options = useMemo<FieldOption[]>(() => modelNames.map((name) => ({ value: name, text: name })), [modelNames]);
  const basicFields = useMemo(() => basics(options), [options]);
  const contextFields = useMemo(() => context(options), [options]);
  const all = useMemo(() => [...basicFields, ...contextFields, ...LIBRARY_FIELDS, ...OPTION_FIELDS], [basicFields, contextFields]);

  const form = useSettingsForm({
    fields: all,
    normalize: (next, changedId) => {
      let out = next;
      for (const id of FOLLOW_IDS) {
        const value = out[id];
        if (value !== undefined && value !== normalizeFollow(value)) out = { ...out, [id]: normalizeFollow(value) };
      }
      // Whitespace-only prompts read as empty when loaded; typing is never rewritten.
      if (changedId === null) {
        for (const id of PROMPT_IDS) {
          const value = out[id];
          if (value !== undefined && value !== normalizePromptValue(value)) out = { ...out, [id]: normalizePromptValue(value) };
        }
      }
      return out;
    },
  });

  const [exporting, setExporting] = useState(false);
  const exportAll = async () => {
    if (exporting) return;
    setExporting(true);
    try {
      const { blob, disposition, manifest } = await downloadExport();
      downloadBlob(fileNameFromDisposition(disposition, exportFallbackName()), blob);
      if (manifest) {
        if (manifest.complete && manifest.failed === 0) notify.success("adminConversation.export.done", { count: manifest.exported });
        else notify.warning("adminConversation.export.partial", { exported: manifest.exported, failed: manifest.failed });
      }
    } catch (error) {
      notify.error("adminConversation.export.failed", error);
    } finally {
      setExporting(false);
    }
  };

  return (
    <AdminPage title={t("adminConversation.title")} description={t("adminConversation.description")}>
      <SettingsGroup
        form={form}
        fields={basicFields}
        title={t("adminConversation.basics.title")}
        description={t("adminConversation.basics.description")}
        onSaved={(sent) => {
          const trace = sent.find((x) => x.key === "process_trace_enabled");
          if (trace) policy.patch({ processTraceEnabled: form.draft["chat.process_trace_enabled"] !== "false" });
        }}
      />
      <SettingsGroup form={form} fields={contextFields} title={t("adminConversation.context.title")} description={t("adminConversation.context.description")} />
      <PromptLibrary form={form} />
      <OptionPolicy form={form} />
      <Section title={t("adminConversation.export.title")} description={t("adminConversation.export.description")}>
        <Button variant="outline" onClick={exportAll} loading={exporting}>
          <Download />
          {t("adminConversation.export.button")}
        </Button>
      </Section>
    </AdminPage>
  );
}
