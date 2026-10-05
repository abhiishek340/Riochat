/**
 * Public surface of the settings feature: the route pages, plus the small set
 * of preference helpers other features read (see ARCHITECTURE and GAPS).
 */
export {
  AboutSettingsPage,
  AccountSettingsPage,
  ChatSettingsPage,
  GeneralSettingsPage,
  McpSettingsPage,
  MemorySettingsPage,
  ModelsSettingsPage,
  NetworkSettingsPage,
  ProviderImportPage,
  SettingsLayout,
  SkillsSettingsPage,
  SubscriptionSettingsPage,
} from "./pages";

// Mounted once at the app root.
export { AppearanceRuntime } from "./appearance-runtime";

// Reused by an administration screen.
export { ImportLinkDialog } from "./models/import-link-dialog";

// Preference helpers for chat and the shell.
export { useChatFont, useChatFontWeight, useFontSize } from "./lib/appearance-device";
export { parseAppearanceBundle, serializeAppearanceBundle } from "./lib/appearance-prefs";
export type { AppearanceBundle, ChatFont, ChatFontWeight, FontSize } from "./lib/appearance-prefs";
export { normalizeSendShortcut } from "./lib/chat-settings";
export type { ChatPreferences, SendShortcut } from "./lib/chat-settings";
export { useChatPreferences, useUserSettings } from "./lib/use-user-settings";
export {
  completionNotificationBody,
  readCompletionNotificationsEnabled,
  shouldShowCompletionNotification,
} from "./lib/notifications";
