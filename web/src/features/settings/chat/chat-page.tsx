"use client";

import { useEffect, useState } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { NativeSelect } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT, type MessageKey } from "@/lib/i18n/provider";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import { LoadErrorNotice, OptionCard, OptionGrid, SettingRow, SettingsPage, SettingsSection } from "../parts";
import { PreferenceMemory } from "../memory/preference-memory";
import { CHAT_FONTS, CHAT_FONT_WEIGHTS, WEIGHT_VALUE, type ChatFont, type ChatFontWeight } from "../lib/appearance-prefs";
import { setChatFont, setChatFontWeight, useAppearanceSaver, useChatFont, useChatFontWeight } from "../lib/appearance-device";
import {
  boolString,
  CONTENT_WIDTHS,
  CONTENT_WIDTH_PX,
  FILE_MODES,
  INPUT_HEIGHTS,
  isApplePlatform,
  parseChatSettings,
  SETTING_KEYS,
  type ContentWidth,
  type FileMode,
  type InputHeight,
} from "../lib/chat-settings";
import { useLoad } from "../lib/use-load";
import { useUserSettings } from "../lib/use-user-settings";
import { fetchBillingMode, fetchCompactPolicy, groupModels, listChatModels, type ChatModelOption } from "./api";
import { shortcutOptions } from "./send-shortcut";

const FONT_SAMPLE: Record<ChatFont, string> = {
  default: "inherit",
  songti: '"Songti SC", "Noto Serif CJK SC", "Source Han Serif SC", Georgia, serif',
  heiti: '"PingFang SC", "Noto Sans CJK SC", "Microsoft YaHei", system-ui, sans-serif',
  mono: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
};

interface Meta {
  models: ChatModelOption[];
  billingMode: string;
  compactEnabled: boolean;
}

async function loadMeta(signal: AbortSignal): Promise<Meta> {
  // Each call may fail on its own without taking the page down.
  const [models, mode, compact] = await Promise.allSettled([listChatModels(signal), fetchBillingMode(signal), fetchCompactPolicy(signal)]);
  return {
    models: models.status === "fulfilled" ? models.value : [],
    billingMode: mode.status === "fulfilled" ? mode.value : "self",
    compactEnabled: compact.status === "fulfilled" ? compact.value : false,
  };
}
