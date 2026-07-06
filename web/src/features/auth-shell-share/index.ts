/**
 * Public surface of the auth / shell / share area: route page components plus
 * the pieces the app shell mounts (extras, search button, user-menu items,
 * desktop gate) and the conversation-change bus other lists can use.
 */
export { LoginPage } from "./auth/login-page";
export { AuthCallbackPage } from "./auth/callback-page";
export { SharePage } from "./share/share-page";
export { ShareLayout } from "./share/share-layout";
export { RecentPage } from "./recent/recent-page";
export { DesktopGate } from "./desktop/gate";
export { DesktopSetupPage } from "./desktop/setup-route";
export { TabStrip } from "./desktop/strip";
export { isStripRoute } from "./desktop/strip-model";
export { ShellExtras } from "./shell/shell-extras";
export { SearchTrigger } from "./shell/search-host";
export { AnnouncementsMenuItem, DownloadDesktopMenuItem, LanguageSubmenu, UpgradePlanMenuItem } from "./shell/user-menu-extras";
export { OnboardingOverlay } from "./onboarding/overlay";
export { requestOpenSearch, NEW_CHAT_EVENT } from "./lib/shortcuts";
export { announceConversationChange, subscribeConversationChanges, CONVERSATION_CHANGED_EVENT, type ConversationChange } from "./lib/conversation-events";
export { requestOpenAnnouncements } from "./lib/announcements-model";
export { createVersionWatcher, VERSION_CHECK_INTERVAL_MS } from "./lib/version-watch";
