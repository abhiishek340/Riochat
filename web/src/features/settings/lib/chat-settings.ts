/**
 * The key/value user settings that steer chat, with their defaults and how a
 * stored string is read. The server keeps every value as a string.
 */

export type SettingsMap = Readonly<Record<string, string>>;

export const SETTING_KEYS = {
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
  autoGenerateTitle: "chat.auto_generate_title",
  autoGenerateLabels: "chat.auto_generate_labels",
  deleteFilesByDefault: "chat.delete_conversation_files_by_default",
  contextCompactAuto: "chat.context_compact_auto",
  restoreDraftOnFailure: "chat.restore_draft_on_failure",
  preserveConversationDrafts: "chat.preserve_conversation_drafts",
  reuseModelOptions: "chat.reuse_model_options",
  reasoningPassback: "chat.reasoning_content_passback",
  inputHeight: "chat.input_height",
  contentWidth: "chat.content_width",
  fileMode: "chat.file_mode",
  defaultMcpToolIds: "chat.default_mcp_tool_ids",
} as const;

export type SendShortcut = "enter" | "ctrl_enter" | "meta_enter";
export const INPUT_HEIGHTS = ["compact", "standard", "loose"] as const;
export type InputHeight = (typeof INPUT_HEIGHTS)[number];
export const CONTENT_WIDTHS = ["compact", "standard", "wide"] as const;
export type ContentWidth = (typeof CONTENT_WIDTHS)[number];
export const CONTENT_WIDTH_PX: Record<ContentWidth, number> = { compact: 760, standard: 900, wide: 1080 };
export const FILE_MODES = ["auto", "full_context", "rag"] as const;
export type FileMode = (typeof FILE_MODES)[number];

/** True unless the stored value is exactly "false". */
export function readOnByDefault(map: SettingsMap, key: string): boolean {
  return map[key] !== "false";
}
/** False unless the stored value is exactly "true". */
export function readOffByDefault(map: SettingsMap, key: string): boolean {
  return map[key] === "true";
}

function choice<T extends string>(value: string | undefined, allowed: readonly T[], fallback: T): T {
  return (allowed as readonly string[]).includes(value ?? "") ? (value as T) : fallback;
}

export function isApplePlatform(): boolean {
  if (typeof navigator === "undefined") return false;
  const nav = navigator as Navigator & { userAgentData?: { platform?: string } };
  const platform = nav.userAgentData?.platform ?? navigator.platform ?? "";
  return /mac|iphone|ipad|ipod/i.test(platform);
}

/** The modifier shortcut native to the platform. */
export function nativeModifierShortcut(apple: boolean): Exclude<SendShortcut, "enter"> {
  return apple ? "meta_enter" : "ctrl_enter";
}

/**
 * "enter" stays "enter"; either modifier variant maps to the one native to the
 * platform; anything else (including empty) means plain Enter.
 */
export function normalizeSendShortcut(value: string | null | undefined, apple: boolean = isApplePlatform()): SendShortcut {
  if (value === "ctrl_enter" || value === "meta_enter") return nativeModifierShortcut(apple);
  return "enter";
}

export interface ChatSettings {
  defaultModel: string;
  sendOnEnter: SendShortcut;
  showTokenUsage: boolean;
  showModelInfo: boolean;
  showLatency: boolean;
  showBillingCost: boolean;
  showProcessTrace: boolean;
  markdownRender: boolean;
  autoExpandThinking: boolean;
  autoExpandToolCalls: boolean;
  autoGenerateTitle: boolean;
  autoGenerateLabels: boolean;
  deleteFilesByDefault: boolean;
  contextCompactAuto: boolean;
  restoreDraftOnFailure: boolean;
  preserveConversationDrafts: boolean;
  reuseModelOptions: boolean;
  reasoningPassback: boolean;
  inputHeight: InputHeight;
  contentWidth: ContentWidth;
  fileMode: FileMode;
}

export function parseChatSettings(map: SettingsMap, apple: boolean = isApplePlatform()): ChatSettings {
  const k = SETTING_KEYS;
  return {
    defaultModel: map[k.defaultModel] ?? "",
    sendOnEnter: normalizeSendShortcut(map[k.sendOnEnter], apple),
    showTokenUsage: readOnByDefault(map, k.showTokenUsage),
    showModelInfo: readOnByDefault(map, k.showModelInfo),
    showLatency: readOnByDefault(map, k.showLatency),
    showBillingCost: readOnByDefault(map, k.showBillingCost),
    showProcessTrace: readOnByDefault(map, k.showProcessTrace),
    markdownRender: readOnByDefault(map, k.markdownRender),
    autoExpandThinking: readOnByDefault(map, k.autoExpandThinking),
    autoExpandToolCalls: readOnByDefault(map, k.autoExpandToolCalls),
    autoGenerateTitle: readOnByDefault(map, k.autoGenerateTitle),
    autoGenerateLabels: readOnByDefault(map, k.autoGenerateLabels),
    deleteFilesByDefault: readOffByDefault(map, k.deleteFilesByDefault),
    contextCompactAuto: readOffByDefault(map, k.contextCompactAuto),
    restoreDraftOnFailure: readOnByDefault(map, k.restoreDraftOnFailure),
    preserveConversationDrafts: readOnByDefault(map, k.preserveConversationDrafts),
    reuseModelOptions: readOnByDefault(map, k.reuseModelOptions),
    reasoningPassback: readOnByDefault(map, k.reasoningPassback),
    inputHeight: choice(map[k.inputHeight], INPUT_HEIGHTS, "standard"),
    contentWidth: choice(map[k.contentWidth], CONTENT_WIDTHS, "compact"),
    fileMode: choice(map[k.fileMode], FILE_MODES, "auto"),
  };
}

export function boolString(value: boolean): string {
  return value ? "true" : "false";
}

/** Booleans the chat feature reads (see useChatPreferences). */
export interface ChatPreferences {
  autoGenerateTitle: boolean;
  autoGenerateLabels: boolean;
  autoExpandThinking: boolean;
  autoExpandToolCalls: boolean;
  deleteFilesByDefault: boolean;
  reuseModelOptions: boolean;
  loaded: boolean;
}

export function chatPreferencesFrom(map: SettingsMap, loaded: boolean): ChatPreferences {
  if (!loaded) {
    return {
      autoGenerateTitle: true,
      autoGenerateLabels: true,
      autoExpandThinking: true,
      autoExpandToolCalls: true,
      deleteFilesByDefault: false,
      reuseModelOptions: true,
      loaded: false,
    };
  }
  const s = parseChatSettings(map);
  return {
    autoGenerateTitle: s.autoGenerateTitle,
    autoGenerateLabels: s.autoGenerateLabels,
    autoExpandThinking: s.autoExpandThinking,
    autoExpandToolCalls: s.autoExpandToolCalls,
    deleteFilesByDefault: s.deleteFilesByDefault,
    reuseModelOptions: s.reuseModelOptions,
    loaded: true,
  };
}

/** Parses the stored JSON array of default MCP tool ids; junk yields an empty list. */
export function parseToolIds(raw: string | undefined): number[] {
  if (!raw) return [];
  try {
    const data: unknown = JSON.parse(raw);
    if (!Array.isArray(data)) return [];
    const ids = data.filter((n): n is number => typeof n === "number" && Number.isInteger(n) && n > 0);
    return Array.from(new Set(ids));
  } catch {
    return [];
  }
}
