/** Pure rules of the conversation screen: prompt library limits and the model-option policy. */
import { isPlainObject } from "../lib/json";
import { problem, type Problem } from "../lib/problem";

export const LIMITS = {
  name: 64,
  description: 256,
  content: 10_000,
  componentSummary: 1024,
  componentSchema: 16 * 1024,
  componentSource: 256 * 1024,
} as const;

/** Trim, drop leading slashes, trim again: `  //hello ` becomes `hello`. */
export function normalizeTemplateName(raw: string): string {
  return raw.trim().replace(/^\/+/, "").trim();
}

/** Characters, not UTF-16 units. */
export function length(text: string): number {
  return [...text].length;
}

export interface TemplateDraft {
  name: string;
  description: string;
  content: string;
}

/** Shared by prompts and skills (`content` is the markdown of a skill). */
export function validateTemplate(draft: TemplateDraft): Problem | null {
  const name = normalizeTemplateName(draft.name);
  const content = draft.content.trim();
  if (!name) return problem("adminConversation.problems.nameRequired");
  if (!content) return problem("adminConversation.problems.contentRequired");
  if (length(name) > LIMITS.name) return problem("adminConversation.problems.nameTooLong", { max: LIMITS.name });
  if (length(draft.description.trim()) > LIMITS.description) return problem("adminConversation.problems.descriptionTooLong", { max: LIMITS.description });
  if (length(content) > LIMITS.content) return problem("adminConversation.problems.contentTooLong", { max: LIMITS.content });
  return null;
}

export const COMPONENT_NAME = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;

export interface ComponentDraft {
  name: string;
  description: string;
  propsSummary: string;
  propsSchema: string;
  rendererSource: string;
}

/** Empty text is allowed; otherwise a JSON object (an array or scalar is not). */
export function validateSchemaText(text: string): Problem | null {
  const source = text.trim();
  if (!source) return null;
  if (source.length > LIMITS.componentSchema) return problem("adminConversation.problems.schemaTooLong");
  try {
    return isPlainObject(JSON.parse(source)) ? null : problem("adminConversation.problems.schemaInvalid");
  } catch {
    return problem("adminConversation.problems.schemaInvalid");
  }
}

export function validateComponent(draft: ComponentDraft): Problem | null {
  const name = draft.name.trim();
  if (!name || !COMPONENT_NAME.test(name) || length(name) > LIMITS.name) return problem("adminConversation.problems.componentName");
  if (!draft.description.trim()) return problem("adminConversation.problems.descriptionRequired");
  if (length(draft.description.trim()) > LIMITS.description) return problem("adminConversation.problems.descriptionTooLong", { max: LIMITS.description });
  if (!draft.propsSummary.trim()) return problem("adminConversation.problems.summaryRequired");
  if (length(draft.propsSummary.trim()) > LIMITS.componentSummary) return problem("adminConversation.problems.summaryTooLong", { max: LIMITS.componentSummary });
  const schema = validateSchemaText(draft.propsSchema);
  if (schema) return schema;
  if (!draft.rendererSource.trim()) return problem("adminConversation.problems.sourceRequired");
  if (draft.rendererSource.length > LIMITS.componentSource) return problem("adminConversation.problems.sourceTooLong");
  return null;
}

/** Blank or whitespace-only prompts become empty; other text is kept as typed. */
export function normalizePromptValue(value: string): string {
  return value.trim() === "" ? "" : value;
}

/** The task-model selects use "follow" for "same as the chat model". */
export function normalizeFollow(value: string): string {
  return value.trim() === "" ? "follow" : value;
}

/** Sample props for the live component preview: invalid or non-object JSON disables it. */
export function parseSampleProps(text: string): Record<string, unknown> | null {
  const source = text.trim();
  if (!source) return {};
  try {
    const value: unknown = JSON.parse(source);
    return isPlainObject(value) ? value : null;
  } catch {
    return null;
  }
}

// --- model option passthrough ----------------------------------------------

export const OPTION_PROTOCOLS = [
  "default",
  "openai_chat_completions",
  "openai_responses",
  "openai_image_generations",
  "openai_image_edits",
  "anthropic_messages",
  "gemini_generate_content",
  "google_image_generation",
  "gemini_interactions",
  "xai_responses",
  "xai_image",
  "xai_image_edits",
  "xai_video",
  "xai_video_extensions",
  "openrouter_chat_completions",
  "openrouter_responses",
  "openrouter_images",
] as const;

export type PathDoc = Record<string, string[]>;

/** Blank text means the built-in defaults. */
export const DEFAULT_ALLOWED: PathDoc = {
  default: ["temperature", "top_p", "max_tokens", "max_output_tokens", "max_completion_tokens", "stop", "response_format.type"],
  openai_chat_completions: ["service_tier", "presence_penalty", "frequency_penalty", "reasoning_effort", "verbosity", "thinking.type", "stream_options.include_usage"],
  openai_responses: ["service_tier", "reasoning.effort", "reasoning.summary", "text.verbosity"],
  openai_image_generations: ["background", "moderation", "n", "output_compression", "output_format", "partial_images", "quality", "response_format", "size", "style", "user"],
  openai_image_edits: ["background", "input_fidelity", "n", "output_compression", "output_format", "partial_images", "quality", "response_format", "size", "user"],
  anthropic_messages: ["speed", "top_k", "thinking.type", "thinking.budget_tokens"],
  gemini_generate_content: [
    "generationConfig.temperature",
    "generationConfig.topP",
    "generationConfig.maxOutputTokens",
    "generationConfig.responseMimeType",
    "generationConfig.thinkingConfig.includeThoughts",
    "generationConfig.thinkingConfig.thinkingLevel",
  ],
  google_image_generation: ["generationConfig.responseModalities", "generationConfig.imageConfig.aspectRatio", "generationConfig.imageConfig.imageSize"],
  gemini_interactions: [
    "generation_config.temperature",
    "generation_config.top_p",
    "generation_config.max_output_tokens",
    "generation_config.thinking_level",
    "generation_config.thinking_summaries",
    "response_format.type",
    "response_format.aspect_ratio",
    "response_format.image_size",
    "response_format.mime_type",
    "response_format.schema",
    "generation_config.video_config.task",
  ],
  xai_responses: ["reasoning.effort", "min_p", "parallel_tool_calls", "store", "top_k"],
  xai_image: ["aspect_ratio", "n", "resolution", "response_format"],
  xai_image_edits: ["aspect_ratio", "n", "resolution", "response_format"],
  xai_video: ["aspect_ratio", "duration", "resolution"],
  xai_video_extensions: ["duration"],
  openrouter_chat_completions: ["presence_penalty", "frequency_penalty", "reasoning_effort", "reasoning.effort", "reasoning.summary", "verbosity", "thinking.type", "stream_options.include_usage"],
  openrouter_responses: ["reasoning.effort", "reasoning.summary"],
  openrouter_images: ["aspect_ratio", "background", "n", "output_compression", "output_format", "provider.allow_fallbacks", "provider.ignore", "provider.only", "provider.order", "provider.sort", "quality", "resolution", "seed", "size", "user"],
};

export const DEFAULT_DENIED: PathDoc = {
  default: ["model", "messages", "input", "instructions", "prompt", "system", "systemInstruction", "headers", "api_key", "apiKey", "base_url", "baseURL", "stream", "previous_response_id"],
};

/** Paths no policy can open, listed in the guide. */
export const SYSTEM_DENIED = ["model", "messages", "input", "instructions", "prompt", "system", "systemInstruction", "headers", "api_key", "apiKey", "base_url", "baseURL"] as const;

export function prettyDoc(doc: PathDoc): string {
  return JSON.stringify(doc, null, 2);
}

export type DocParse = { ok: true; doc: PathDoc } | { ok: false; reason: "syntax" | "shape" | "protocol" };

/** Parses a policy document: an object of protocol key to list of non-empty path strings. */
export function parseDoc(text: string): DocParse {
  const source = text.trim();
  if (!source) return { ok: true, doc: {} };
  let value: unknown;
  try {
    value = JSON.parse(source);
  } catch {
    return { ok: false, reason: "syntax" };
  }
  if (!isPlainObject(value)) return { ok: false, reason: "shape" };
  const doc: PathDoc = {};
  for (const [key, list] of Object.entries(value)) {
    const protocol = key.trim().toLowerCase();
    if (!(OPTION_PROTOCOLS as readonly string[]).includes(protocol)) return { ok: false, reason: "protocol" };
    if (!Array.isArray(list) || list.some((p) => typeof p !== "string" || !p.trim())) return { ok: false, reason: "shape" };
    doc[protocol] = list.map((p: string) => p.trim());
  }
  return { ok: true, doc };
}

/** Blank values show (and save) the defaults. */
export function docText(stored: string, fallback: PathDoc): string {
  return stored.trim() ? stored : prettyDoc(fallback);
}

function paths(doc: PathDoc, protocol: string): string[] {
  return [...(doc.default ?? []), ...(protocol === "default" ? [] : (doc[protocol] ?? []))];
}

/** A rule covers a path when it equals it or is one of its ancestors. */
export function covers(rule: string, path: string): boolean {
  return path === rule || path.startsWith(`${rule}.`);
}

function underAny(list: readonly string[], path: string): string | undefined {
  return list.find((rule) => covers(rule, path));
}

export type Verdict = "systemDenied" | "denylist" | "allowlist" | "notAllowed" | "notDenied" | "disabled";

export interface PathResult {
  path: string;
  pass: boolean;
  verdict: Verdict;
  rule?: string;
}

/** Why one request parameter passes or is dropped under the current policy. */
export function evaluatePath(path: string, protocol: string, mode: string, allowed: PathDoc, denied: PathDoc): PathResult {
  const p = path.trim();
  const system = underAny(SYSTEM_DENIED, p);
  if (system) return { path: p, pass: false, verdict: "systemDenied", rule: system };
  if (mode === "disabled") return { path: p, pass: false, verdict: "disabled" };
  const deniedRule = underAny(paths(mergeWithDefault(denied, DEFAULT_DENIED), protocol), p);
  if (deniedRule) return { path: p, pass: false, verdict: "denylist", rule: deniedRule };
  if (mode === "denylist") return { path: p, pass: true, verdict: "notDenied" };
  const allowRule = underAny(paths(allowed, protocol), p);
  return allowRule ? { path: p, pass: true, verdict: "allowlist", rule: allowRule } : { path: p, pass: false, verdict: "notAllowed" };
}

/** Denied defaults are always layered on top of the stored denylist. */
function mergeWithDefault(doc: PathDoc, defaults: PathDoc): PathDoc {
  const out: PathDoc = { ...doc };
  for (const [key, list] of Object.entries(defaults)) out[key] = [...new Set([...(out[key] ?? []), ...list])];
  return out;
}

export function evaluatePaths(text: string, protocol: string, mode: string, allowed: PathDoc, denied: PathDoc): PathResult[] {
  return text
    .split(/[\n,]/)
    .map((p) => p.trim())
    .filter(Boolean)
    .map((p) => evaluatePath(p, protocol, mode, allowed, denied));
}

/** Names of the models that can serve chat, for the model selects. */
export function chatModelNames(models: ReadonlyArray<{ platformModelName: string; kindsJSON?: string }>): string[] {
  const names = new Set<string>();
  for (const m of models) {
    let kinds: unknown = ["chat"];
    try {
      kinds = JSON.parse(m.kindsJSON || '["chat"]');
    } catch {
      /* treat as chat */
    }
    if (Array.isArray(kinds) && kinds.includes("chat") && m.platformModelName) names.add(m.platformModelName);
  }
  return [...names].sort((a, b) => a.localeCompare(b));
}

/** `conversations-export-20261016T101500.jsonl` style fallback. */
export function exportFallbackName(now: Date = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `conversations-export-${now.getFullYear()}${p(now.getMonth() + 1)}${p(now.getDate())}T${p(now.getHours())}${p(now.getMinutes())}${p(now.getSeconds())}.jsonl`;
}
