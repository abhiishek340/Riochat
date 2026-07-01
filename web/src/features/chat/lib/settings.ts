/**
 * The account settings that steer chat (all stored by the server as strings).
 */

export const KEYS = {
  defaultModel: "chat.default_model",
  sendOnEnter: "chat.send_on_enter",
  showTokenUsage: "chat.show_token_usage",
  showModelInfo: "chat.show_model_info",
  showLatency: "chat.show_latency",
  showBillingCost: "chat.show_billing_cost",
  showProcessTrace: "chat.show_process_trace",
  markdownRender: "chat.markdown_render",
  autoExpandThinking: "chat.auto_expand_thinking",
  autoExpandToolCalls: "chat.auto_expand_tool_calls",
  autoGenerateLabels: "chat.auto_generate_labels",
  deleteFilesByDefault: "chat.delete_conversation_files_by_default",
  restoreDraftOnFailure: "chat.restore_draft_on_failure",
  preserveDrafts: "chat.preserve_conversation_drafts",
  reuseOptions: "chat.reuse_model_options",
  inputHeight: "chat.input_height",
  contentWidth: "chat.content_width",
  placements: "chat.model_control_placements",
  defaultToolIds: "chat.default_mcp_tool_ids",
} as const;

export type SendShortcut = "enter" | "modifier";
export type InputHeight = "compact" | "standard" | "loose";
export type ContentWidth = "compact" | "standard" | "wide";

export interface ChatPrefs {
  defaultModel: string;
  sendOn: SendShortcut;
  showTokenUsage: boolean;
  showModelInfo: boolean;
  showLatency: boolean;
  showBillingCost: boolean;
  showProcessTrace: boolean;
  markdownRender: boolean;
  autoExpandThinking: boolean;
  autoExpandToolCalls: boolean;
  autoGenerateLabels: boolean;
  deleteFilesByDefault: boolean;
  restoreDraftOnFailure: boolean;
  preserveDrafts: boolean;
  reuseOptions: boolean;
  inputHeight: InputHeight;
  contentWidth: ContentWidth;
  placements: string;
  defaultToolIds: number[];
}

export type SettingsMap = Readonly<Record<string, string>>;

const onUnlessFalse = (map: SettingsMap, key: string) => map[key] !== "false";

export const CONTENT_WIDTH_REM: Record<ContentWidth, number> = { compact: 46, standard: 56, wide: 68 };
export const INPUT_MAX_HEIGHT_REM: Record<InputHeight, number> = { compact: 8, standard: 11, loose: 16 };

export function parseToolIds(raw: string | undefined): number[] {
  if (!raw) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const out: number[] = [];
    for (const v of parsed) if (typeof v === "number" && Number.isInteger(v) && v > 0 && !out.includes(v)) out.push(v);
    return out;
  } catch {
    return [];
  }
}

export function parsePrefs(map: SettingsMap): ChatPrefs {
  const send = map[KEYS.sendOnEnter];
  const height = map[KEYS.inputHeight];
  const width = map[KEYS.contentWidth];
  return {
    defaultModel: map[KEYS.defaultModel] ?? "",
    // `enter` sends on Enter; any other recognised value means modifier+Enter; unknown -> Enter.
    sendOn: send === "ctrl_enter" || send === "meta_enter" || send === "modifier_enter" ? "modifier" : "enter",
    showTokenUsage: onUnlessFalse(map, KEYS.showTokenUsage),
    showModelInfo: onUnlessFalse(map, KEYS.showModelInfo),
    showLatency: onUnlessFalse(map, KEYS.showLatency),
    showBillingCost: onUnlessFalse(map, KEYS.showBillingCost),
    showProcessTrace: onUnlessFalse(map, KEYS.showProcessTrace),
    markdownRender: onUnlessFalse(map, KEYS.markdownRender),
    autoExpandThinking: onUnlessFalse(map, KEYS.autoExpandThinking),
    autoExpandToolCalls: onUnlessFalse(map, KEYS.autoExpandToolCalls),
    autoGenerateLabels: onUnlessFalse(map, KEYS.autoGenerateLabels),
    deleteFilesByDefault: map[KEYS.deleteFilesByDefault] === "true",
    restoreDraftOnFailure: onUnlessFalse(map, KEYS.restoreDraftOnFailure),
    preserveDrafts: onUnlessFalse(map, KEYS.preserveDrafts),
    reuseOptions: onUnlessFalse(map, KEYS.reuseOptions),
    inputHeight: height === "compact" || height === "loose" ? height : "standard",
    contentWidth: width === "compact" || width === "wide" ? width : "standard",
    placements: map[KEYS.placements] ?? "",
    defaultToolIds: parseToolIds(map[KEYS.defaultToolIds]),
  };
}

export const DEFAULT_PREFS: ChatPrefs = parsePrefs({});

// --- keyboard ------------------------------------------------------------------------

export interface KeyLike {
  key: string;
  shiftKey: boolean;
  altKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  isComposing?: boolean;
  keyCode?: number;
  nativeEvent?: { isComposing?: boolean };
}

/** A key press during IME composition never sends and never confirms a menu item. */
export function isComposingKey(e: KeyLike): boolean {
  return !!(e.isComposing || e.nativeEvent?.isComposing || e.key === "Process" || e.keyCode === 229);
}

export function isApple(platform: string): boolean {
  return /mac|iphone|ipad|ipod/i.test(platform);
}

/** Whether this key press is the send shortcut. */
export function isSendKey(e: KeyLike, mode: SendShortcut, apple: boolean): boolean {
  if (e.key !== "Enter" || isComposingKey(e)) return false;
  if (e.shiftKey || e.altKey) return false;
  if (mode === "enter") return !e.ctrlKey && !e.metaKey;
  return apple ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
}
