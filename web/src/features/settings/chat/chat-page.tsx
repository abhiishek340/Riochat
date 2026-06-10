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

export function ChatSettingsPage() {
  const t = useT();
  const errorMessage = useErrorMessage();
  usePageTitle(t("settings.title"));
  const { values, loaded, failed, update, reload } = useUserSettings();
  const meta = useLoad(loadMeta, "meta");
  const { policy } = useFeaturePolicy();
  const font = useChatFont();
  const weight = useChatFontWeight();
  const { schedule } = useAppearanceSaver();
  const [apple, setApple] = useState(false);

  useEffect(() => {
    setApple(isApplePlatform());
  }, []);

  const s = parseChatSettings(values, apple);
  const loading = !loaded || meta.loading;
  const info: Meta = meta.data ?? { models: [], billingMode: "self", compactEnabled: false };
  const costLocked = info.billingMode === "self";
  const shortcuts = shortcutOptions(apple);

  const save = async (patch: Record<string, string>) => {
    try {
      await update(patch);
    } catch (e) {
      toast.error(t("settings.chat.toasts.saveFailed"), { description: errorMessage(e, t("settings.chat.toasts.retryLater")) });
    }
  };
  const toggle = (key: string) => (next: boolean) => void save({ [key]: boolString(next) });

  const k = SETTING_KEYS;
  const toggleRow = (id: string, titleKey: MessageKey, descKey: MessageKey, key: string, checked: boolean) => (
    <SettingRow title={t(titleKey)} description={t(descKey)} htmlFor={id}>
      <Switch id={id} checked={checked} disabled={loading} onCheckedChange={toggle(key)} />
    </SettingRow>
  );

  const groups = groupModels(info.models);
  const option = (m: ChatModelOption) => (
    <option key={m.name} value={m.name}>
      {m.name}
    </option>
  );

  return (
    <SettingsPage title={t("settings.nav.chat")} description={t("settings.chat.description")}>
      {failed ? <LoadErrorNotice title={t("settings.chat.toasts.loadFailed")} onRetry={() => void reload()} /> : null}
      <SettingsSection title={t("settings.chat.automation.title")}>
        <SettingRow title={t("settings.chat.automation.defaultModel")} description={t("settings.chat.automation.defaultModelHint")} htmlFor="default-model">
          {loading ? (
            <Skeleton className="h-9 w-64" />
          ) : (
            <NativeSelect id="default-model" className="w-64 max-w-full" value={s.defaultModel} onChange={(e) => void save({ [k.defaultModel]: e.target.value })}>
              <option value="">{t("settings.chat.automation.recommended")}</option>
              {s.defaultModel && !info.models.some((m) => m.name === s.defaultModel) ? <option value={s.defaultModel}>{s.defaultModel}</option> : null}
              {groups.map((g) =>
                g.group ? (
                  <optgroup key={g.group} label={g.group}>
                    {g.items.map(option)}
                  </optgroup>
                ) : (
                  g.items.map(option)
                ),
              )}
            </NativeSelect>
          )}
        </SettingRow>
        {toggleRow("auto-title", "settings.chat.automation.autoTitle", "settings.chat.automation.autoTitleHint", k.autoGenerateTitle, s.autoGenerateTitle)}
        {toggleRow("auto-labels", "settings.chat.automation.autoLabels", "settings.chat.automation.autoLabelsHint", k.autoGenerateLabels, s.autoGenerateLabels)}
      </SettingsSection>

      <SettingsSection title={t("settings.chat.input.title")}>
        <SettingRow
          title={t("settings.chat.input.shortcut")}
          description={t("settings.chat.input.shortcutHint", { shortcut: shortcuts.find((o) => o.value === s.sendOnEnter)?.label ?? "Enter" })}
          htmlFor="send-shortcut"
        >
          <NativeSelect id="send-shortcut" className="w-44" value={s.sendOnEnter} disabled={loading} onChange={(e) => void save({ [k.sendOnEnter]: e.target.value })}>
            {shortcuts.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </NativeSelect>
        </SettingRow>
        <SettingRow title={t("settings.chat.input.height")} description={t("settings.chat.input.heightHint")} htmlFor="input-height">
          <NativeSelect id="input-height" className="w-44" value={s.inputHeight} disabled={loading} onChange={(e) => void save({ [k.inputHeight]: e.target.value as InputHeight })}>
            {INPUT_HEIGHTS.map((h) => (
              <option key={h} value={h}>
                {t(`settings.chat.input.heights.${h}`)}
              </option>
            ))}
          </NativeSelect>
        </SettingRow>
        {toggleRow("restore-draft", "settings.chat.input.restoreDraft", "settings.chat.input.restoreDraftHint", k.restoreDraftOnFailure, s.restoreDraftOnFailure)}
        {toggleRow("keep-drafts", "settings.chat.input.keepDrafts", "settings.chat.input.keepDraftsHint", k.preserveConversationDrafts, s.preserveConversationDrafts)}
        {toggleRow("reuse-options", "settings.chat.input.reuseOptions", "settings.chat.input.reuseOptionsHint", k.reuseModelOptions, s.reuseModelOptions)}
        {toggleRow("delete-files", "settings.chat.input.deleteFiles", "settings.chat.input.deleteFilesHint", k.deleteFilesByDefault, s.deleteFilesByDefault)}
      </SettingsSection>

      <SettingsSection title={t("settings.chat.display.title")}>
        {toggleRow("markdown", "settings.chat.display.markdown", "settings.chat.display.markdownHint", k.markdownRender, s.markdownRender)}
        {toggleRow("expand-thinking", "settings.chat.display.expandThinking", "settings.chat.display.expandThinkingHint", k.autoExpandThinking, s.autoExpandThinking)}
        {toggleRow("expand-tools", "settings.chat.display.expandTools", "settings.chat.display.expandToolsHint", k.autoExpandToolCalls, s.autoExpandToolCalls)}
        {policy.processTraceEnabled
          ? toggleRow("process-trace", "settings.chat.display.processTrace", "settings.chat.display.processTraceHint", k.showProcessTrace, s.showProcessTrace)
          : null}
        {toggleRow("model-info", "settings.chat.display.modelInfo", "settings.chat.display.modelInfoHint", k.showModelInfo, s.showModelInfo)}
        {toggleRow("token-usage", "settings.chat.display.tokenUsage", "settings.chat.display.tokenUsageHint", k.showTokenUsage, s.showTokenUsage)}
        {toggleRow("latency", "settings.chat.display.latency", "settings.chat.display.latencyHint", k.showLatency, s.showLatency)}
        <SettingRow
          title={t("settings.chat.display.cost")}
          description={costLocked && !loading ? t("settings.chat.display.costLocked") : t("settings.chat.display.costHint")}
          htmlFor="billing-cost"
        >
          <Switch id="billing-cost" checked={!costLocked && s.showBillingCost} disabled={loading || costLocked} onCheckedChange={toggle(k.showBillingCost)} />
        </SettingRow>
        <div className="grid gap-5 px-4 py-4">
          <div className="grid gap-2">
            <h4 className="text-sm font-medium">{t("settings.chat.display.contentWidth")}</h4>
            <OptionGrid label={t("settings.chat.display.contentWidth")} columns="sm:grid-cols-3">
              {CONTENT_WIDTHS.map((w: ContentWidth) => (
                <OptionCard
                  key={w}
                  pressed={s.contentWidth === w}
                  disabled={loading}
                  title={t(`settings.chat.display.widths.${w}`)}
                  caption={`${CONTENT_WIDTH_PX[w]} px`}
                  onPress={() => void save({ [k.contentWidth]: w })}
                >
                  <div className="flex h-12 items-stretch justify-center bg-muted/60 py-1.5">
                    <div className="rounded-sm bg-primary/25" style={{ width: `${(CONTENT_WIDTH_PX[w] / 1080) * 100}%` }} />
                  </div>
                </OptionCard>
              ))}
            </OptionGrid>
          </div>
          <div className="grid gap-2">
            <h4 className="text-sm font-medium">{t("settings.chat.display.font")}</h4>
            <OptionGrid label={t("settings.chat.display.font")}>
              {CHAT_FONTS.map((f: ChatFont) => (
                <OptionCard
                  key={f}
                  pressed={font === f}
                  title={t(`settings.chat.display.fonts.${f}`)}
                  onPress={() => {
                    setChatFont(f);
                    schedule({ chatFont: f });
                  }}
                >
                  <div className="flex h-12 items-center justify-center bg-muted/60 text-xl" style={{ fontFamily: FONT_SAMPLE[f] }}>
                    Aa Bb
                  </div>
                </OptionCard>
              ))}
            </OptionGrid>
          </div>
          <div className="grid gap-2">
            <h4 className="text-sm font-medium">{t("settings.chat.display.weight")}</h4>
            <OptionGrid label={t("settings.chat.display.weight")}>
              {CHAT_FONT_WEIGHTS.map((w: ChatFontWeight) => (
                <OptionCard
                  key={w}
                  pressed={weight === w}
                  title={t(`settings.chat.display.weights.${w}`)}
                  caption={String(WEIGHT_VALUE[w])}
                  onPress={() => {
                    setChatFontWeight(w);
                    schedule({ chatFontWeight: w });
                  }}
                >
                  <div className="flex h-12 items-center justify-center bg-muted/60 text-xl" style={{ fontWeight: WEIGHT_VALUE[w] }}>
                    Aa Bb
                  </div>
                </OptionCard>
              ))}
            </OptionGrid>
          </div>
        </div>
      </SettingsSection>

      <SettingsSection title={t("settings.chat.context.title")}>
        {info.compactEnabled
          ? toggleRow("auto-compact", "settings.chat.context.autoCompact", "settings.chat.context.autoCompactHint", k.contextCompactAuto, s.contextCompactAuto)
          : null}
        {toggleRow("passback", "settings.chat.context.passback", "settings.chat.context.passbackHint", k.reasoningPassback, s.reasoningPassback)}
      </SettingsSection>

      <SettingsSection title={t("settings.chat.files.title")}>
        <SettingRow title={t("settings.chat.files.mode")} description={t(`settings.chat.files.modeHints.${s.fileMode}`)} htmlFor="file-mode">
          <NativeSelect id="file-mode" className="w-56" value={s.fileMode} disabled={loading} onChange={(e) => void save({ [k.fileMode]: e.target.value as FileMode })}>
            {FILE_MODES.map((m) => (
              <option key={m} value={m}>
                {t(`settings.chat.files.modes.${m}`)}
              </option>
            ))}
          </NativeSelect>
        </SettingRow>
      </SettingsSection>

      <PreferenceMemory />
    </SettingsPage>
  );
}
