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
