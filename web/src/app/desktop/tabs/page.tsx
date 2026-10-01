import { TabStrip } from "@/features/auth-shell-share";

/**
 * The desktop shell's tab strip webview. `RootProviders` renders this route
 * without branding, i18n or session providers (see `isStripRoute`).
 */
export default function Page() {
  return <TabStrip />;
}
