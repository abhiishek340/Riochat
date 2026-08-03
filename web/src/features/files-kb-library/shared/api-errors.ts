/**
 * Localized text for the backend error codes this area can meet. The shared
 * catalog only knows a few `file.*` codes; knowledge bases, skills, prompt
 * presets and indexing would otherwise surface the server's English message.
 * Importing this module registers the formatter once (the area's entry point
 * and its pages do so); codes not listed here fall through to the shared rules.
 */
import { registerErrorFormatter, type ErrorFormatter } from "@/lib/i18n/errors";

export const AREA_ERROR_KEYS: Readonly<Record<string, string>> = {
  "file.required": "fileRequired",
  "file.invalid_stream": "fileInvalidStream",
  "file.invalid_id": "fileInvalidId",
  "file.invalid_reference": "fileInvalidReference",
  "file.extract_not_ready": "fileExtractNotReady",
  "file.invalid_name": "fileInvalidName",
  "file.embedding_unavailable": "fileEmbeddingUnavailable",
  "file.too_large_for_context": "fileTooLargeForContext",
  "request.invalid_file": "invalidFile",
  "embedding.too_many_files": "embeddingTooMany",
  "embedding.service_not_configured": "embeddingNotConfigured",
  "embedding.service_unavailable": "embeddingUnavailable",
  "embedding.submit_failed": "embeddingSubmitFailed",
  "knowledge_base.disabled": "kbDisabled",
  "knowledge_base.invalid": "kbInvalid",
  "knowledge_base.not_found": "kbNotFound",
  "knowledge_base.conflict": "kbConflict",
  "knowledge_base.platform_file_in_use": "kbPlatformFileInUse",
  "knowledge_base.file_cleanup_unavailable": "kbCleanupUnavailable",
  "knowledge_base.internal": "kbInternal",
  "knowledge_base.owner_file_reference": "kbOwnerFileReference",
  "knowledge_base.invalid_reference": "kbInvalidReference",
  "knowledge_base.not_ready": "kbNotReady",
  "knowledge_base.unavailable": "kbUnavailable",
  "skill.invalid_id": "skillInvalidId",
  "skill.too_many_ids": "skillTooManyIds",
  "skill.not_found": "skillNotFound",
  "skill_trigger.already_exists": "skillTriggerExists",
  "request.invalid_skill": "invalidSkill",
  "prompt_preset.invalid_id": "promptInvalidId",
  "prompt_preset.not_found": "promptNotFound",
  "prompt_preset_trigger.already_exists": "promptTriggerExists",
  "request.invalid_prompt_preset": "invalidPrompt",
};

/** Code prefixes the formatter listens to (the registry matches by prefix). */
const PREFIXES = [...new Set(Object.keys(AREA_ERROR_KEYS))];

export const formatAreaError: ErrorFormatter = (error, ctx) => {
  const name = error.errorCode ? AREA_ERROR_KEYS[error.errorCode] : undefined;
  return name ? ctx.lookup(`filesWs.apiErrors.${name}`) : undefined;
};

let registered = false;
/** Idempotent; safe to call from several entry points. */
export function registerAreaErrors(): void {
  if (registered) return;
  registered = true;
  for (const prefix of PREFIXES) registerErrorFormatter(prefix, formatAreaError);
}

registerAreaErrors();
