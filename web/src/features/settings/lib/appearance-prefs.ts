/**
 * Appearance preferences: value sets, the account-stored bundle and how it is
 * merged with device values. Pure logic only; storage and the DOM live in
 * appearance-device.ts.
 */
import { ACCOUNT_PRESET_OF, accentFromAccountPreset } from "@/lib/theme/account-preset";
import { ACCENTS, type Accent } from "@/lib/theme/config";
import { isRecord } from "@/lib/utils/guards";

export const FONT_SIZES = ["small", "standard", "medium", "large"] as const;
export type FontSize = (typeof FONT_SIZES)[number];
export const DEFAULT_FONT_SIZE: FontSize = "standard";
export const FONT_SCALE: Record<FontSize, number> = { small: 0.88, standard: 1, medium: 1.12, large: 1.24 };

export const CHAT_FONTS = ["default", "songti", "heiti", "mono"] as const;
export type ChatFont = (typeof CHAT_FONTS)[number];
export const DEFAULT_CHAT_FONT: ChatFont = "default";

export const CHAT_FONT_WEIGHTS = ["regular", "medium", "semibold", "bold"] as const;
export type ChatFontWeight = (typeof CHAT_FONT_WEIGHTS)[number];
export const DEFAULT_CHAT_FONT_WEIGHT: ChatFontWeight = "regular";
export const WEIGHT_VALUE: Record<ChatFontWeight, number> = { regular: 400, medium: 500, semibold: 600, bold: 700 };

export { ACCOUNT_PRESET_OF, accentFromAccountPreset };

export const PRESETS = ACCENTS;
export type Preset = Accent;

export function isFontSize(v: unknown): v is FontSize {
  return typeof v === "string" && (FONT_SIZES as readonly string[]).includes(v);
}
export function isChatFont(v: unknown): v is ChatFont {
  return typeof v === "string" && (CHAT_FONTS as readonly string[]).includes(v);
}
export function isChatFontWeight(v: unknown): v is ChatFontWeight {
  return typeof v === "string" && (CHAT_FONT_WEIGHTS as readonly string[]).includes(v);
}

/** Every key optional: the account may know only some of them. */
export interface AppearanceBundle {
  preset?: Preset;
  chatFont?: ChatFont;
  chatFontWeight?: ChatFontWeight;
  fontSize?: FontSize;
}

export interface FullAppearance {
  preset: Preset;
  chatFont: ChatFont;
  chatFontWeight: ChatFontWeight;
  fontSize: FontSize;
}

/** Lenient parse: junk JSON, non-objects and unknown values yield nothing for that key. */
export function parseAppearanceBundle(raw: unknown): AppearanceBundle {
  if (typeof raw !== "string" || !raw.trim()) return {};
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return {};
  }
  if (!isRecord(data)) return {};
  const out: AppearanceBundle = {};
  const preset = accentFromAccountPreset(data.preset);
  if (preset) out.preset = preset;
  if (isChatFont(data.chatFont)) out.chatFont = data.chatFont;
  if (isChatFontWeight(data.chatFontWeight)) out.chatFontWeight = data.chatFontWeight;
  if (isFontSize(data.fontSize)) out.fontSize = data.fontSize;
  return out;
}

/** Always writes all four keys. */
export function serializeAppearanceBundle(value: FullAppearance): string {
  return JSON.stringify({
    preset: ACCOUNT_PRESET_OF[value.preset],
    chatFont: value.chatFont,
    chatFontWeight: value.chatFontWeight,
    fontSize: value.fontSize,
  });
}

/** Device values overlaid by whatever the account stores. */
export function resolveAppearance(device: FullAppearance, account: AppearanceBundle): FullAppearance {
  return {
    preset: account.preset ?? device.preset,
    chatFont: account.chatFont ?? device.chatFont,
    chatFontWeight: account.chatFontWeight ?? device.chatFontWeight,
    fontSize: account.fontSize ?? device.fontSize,
  };
}

/** Device values overlaid by a pending patch (what gets sent to the account). */
export function overlayAppearance(device: FullAppearance, patch: AppearanceBundle): FullAppearance {
  return resolveAppearance(device, patch);
}

export function readFontSize(raw: string | null): FontSize {
  return isFontSize(raw) ? raw : DEFAULT_FONT_SIZE;
}
export function readChatFont(raw: string | null): ChatFont {
  return isChatFont(raw) ? raw : DEFAULT_CHAT_FONT;
}
export function readChatFontWeight(raw: string | null): ChatFontWeight {
  return isChatFontWeight(raw) ? raw : DEFAULT_CHAT_FONT_WEIGHT;
}
