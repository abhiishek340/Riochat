import {
  BarChart3,
  BookOpen,
  Boxes,
  CreditCard,
  Files,
  FolderOpen,
  Info,
  KeyRound,
  Library,
  ListChecks,
  Megaphone,
  MessageSquare,
  MessagesSquare,
  Network,
  ScrollText,
  ShieldCheck,
  Users,
  Wrench,
  LogIn,
  UsersRound,
  type LucideIcon,
} from "lucide-react";
import type { FeatureName } from "@/lib/capabilities/features";
import type { MessageKey } from "@/lib/i18n/provider";
import type { FeaturePolicy } from "@/lib/api/types";

export interface NavEntry {
  id: string;
  href: string;
  labelKey: MessageKey;
  icon: LucideIcon;
  /** Hidden while this server capability is off. */
  feature?: FeatureName;
  /** Adds a small status mark after the label. */
  mark?: "update";
  /** Listed only for viewers holding this role. */
  role?: "superadmin";
  /** Hidden while this admin-controlled policy flag is off. */
  policy?: keyof Pick<FeaturePolicy, "knowledgeBaseEnabled">;
}

export interface NavGroup {
  id: string;
  labelKey?: MessageKey;
  items: NavEntry[];
}

/** Primary navigation of the signed-in app. Routes live in src/app/(app). */
export const APP_NAV: NavGroup[] = [
  {
    id: "main",
    items: [
      { id: "chat", href: "/chat", labelKey: "shell.nav.chat", icon: MessageSquare },
      { id: "recent", href: "/recent", labelKey: "shell.nav.recent", icon: MessagesSquare },
      { id: "files", href: "/files", labelKey: "shell.nav.files", icon: Files },
      { id: "library", href: "/library", labelKey: "shell.nav.library", icon: Library },
      { id: "knowledge-bases", href: "/knowledge-bases", labelKey: "shell.nav.knowledgeBases", icon: BookOpen, policy: "knowledgeBaseEnabled" },
    ],
  },
];

/**
 * Administration console. Routes live in src/app/admin. One flat list: the
 * console's section table fixes the order, so entries are not regrouped.
 */
export const ADMIN_NAV: NavGroup[] = [
  {
    id: "console",
    items: [
      { id: "statistics", href: "/admin/statistics", labelKey: "shell.admin.nav.statistics", icon: BarChart3 },
      { id: "users", href: "/admin/users", labelKey: "shell.admin.nav.users", icon: Users, feature: "multiUser" },
      { id: "groups", href: "/admin/groups", labelKey: "shell.admin.nav.groups", icon: UsersRound, feature: "multiUser" },
      { id: "upstreams", href: "/admin/upstreams", labelKey: "shell.admin.nav.upstreams", icon: Network },
      { id: "models", href: "/admin/models", labelKey: "shell.admin.nav.models", icon: Boxes },
      { id: "tools", href: "/admin/tools", labelKey: "shell.admin.nav.tools", icon: Wrench },
      { id: "billing", href: "/admin/billing", labelKey: "shell.admin.nav.billing", icon: CreditCard },
      { id: "announcements", href: "/admin/announcements", labelKey: "shell.admin.nav.announcements", icon: Megaphone, feature: "announcements" },
      { id: "logs", href: "/admin/logs", labelKey: "shell.admin.nav.logs", icon: ScrollText },
      { id: "content-moderation", href: "/admin/content-moderation", labelKey: "shell.admin.nav.contentModeration", icon: ShieldCheck, feature: "contentModeration", role: "superadmin" },
      { id: "login", href: "/admin/login", labelKey: "shell.admin.nav.login", icon: LogIn, feature: "identityProviders" },
      { id: "conversation", href: "/admin/conversation", labelKey: "shell.admin.nav.conversation", icon: MessagesSquare },
      { id: "files", href: "/admin/files", labelKey: "shell.admin.nav.files", icon: FolderOpen },
      { id: "knowledge-bases", href: "/admin/knowledge-bases", labelKey: "shell.admin.nav.knowledgeBases", icon: BookOpen },
      { id: "user-keys", href: "/admin/user-keys", labelKey: "shell.admin.nav.userKeys", icon: KeyRound, feature: "multiUser" },
      { id: "about", href: "/admin/about", labelKey: "shell.admin.nav.about", icon: Info, mark: "update" },
    ],
  },
];

export const ALL_NAV_ENTRIES: NavEntry[] = [...APP_NAV, ...ADMIN_NAV].flatMap((g) => g.items);

/** Icon used by settings-like areas that have no nav entry of their own. */
export const SETTINGS_ICON = ListChecks;

export function isActivePath(pathname: string, href: string): boolean {
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function findNavEntry(pathname: string): NavEntry | undefined {
  return ALL_NAV_ENTRIES.find((e) => isActivePath(pathname, e.href));
}
