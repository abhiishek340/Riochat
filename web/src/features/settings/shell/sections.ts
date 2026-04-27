import {
  Bot,
  Brain,
  CreditCard,
  Globe,
  Info,
  KeyRound,
  MessageSquare,
  Plug,
  ShieldCheck,
  SlidersHorizontal,
  type LucideIcon,
} from "lucide-react";
import type { Capabilities, FeatureName } from "@/lib/capabilities/features";
import type { MessageKey } from "@/lib/i18n/provider";

export type SectionId =
  | "general"
  | "chat"
  | "memory"
  | "skills"
  | "tools"
  | "models"
  | "subscription"
  | "account"
  | "network"
  | "about";

export interface SectionDef {
  id: SectionId;
  /** Path relative to the base path, without a leading slash. */
  slug: string;
  labelKey: MessageKey;
  icon: LucideIcon;
  /** Hidden while this server capability is off. */
  feature?: FeatureName;
  /** Hidden unless the personal-provider access answer says enabled. */
  needsProviderAccess?: boolean;
  /** Hidden outside the desktop shell. */
  desktopOnly?: boolean;
}

export const DEFAULT_BASE_PATH = "/settings";

/** Fixed order of the navigation. */
export const SECTIONS: readonly SectionDef[] = [
  { id: "general", slug: "general", labelKey: "settings.nav.general", icon: SlidersHorizontal },
  { id: "chat", slug: "chat", labelKey: "settings.nav.chat", icon: MessageSquare },
  { id: "memory", slug: "memory", labelKey: "settings.nav.memory", icon: Brain },
  { id: "skills", slug: "skills", labelKey: "settings.nav.skills", icon: Bot },
  { id: "tools", slug: "tools", labelKey: "settings.nav.tools", icon: Plug },
  { id: "models", slug: "models", labelKey: "settings.nav.models", icon: KeyRound, feature: "multiUser", needsProviderAccess: true },
  { id: "subscription", slug: "subscription", labelKey: "settings.nav.subscription", icon: CreditCard, feature: "billingGating" },
  { id: "account", slug: "account", labelKey: "settings.nav.account", icon: ShieldCheck },
  { id: "network", slug: "network", labelKey: "settings.nav.network", icon: Globe, desktopOnly: true },
  { id: "about", slug: "about", labelKey: "settings.nav.about", icon: Info },
];

export interface VisibilityInput {
  flags: Capabilities;
  /** `null` while the personal-provider answer is unknown (treated as off). */
  providerAccess: boolean | null;
  desktop: boolean;
}

export function isSectionVisible(section: SectionDef, input: VisibilityInput): boolean {
  if (section.feature && !input.flags[section.feature]) return false;
  if (section.needsProviderAccess && input.providerAccess !== true) return false;
  if (section.desktopOnly && !input.desktop) return false;
  return true;
}

export function visibleSections(input: VisibilityInput): SectionDef[] {
  return SECTIONS.filter((s) => isSectionVisible(s, input));
}

export function sectionPath(section: SectionDef, basePath = DEFAULT_BASE_PATH): string {
  return `${basePath.replace(/\/+$/, "")}/${section.slug}`;
}

/** A section owns its path and everything beneath it. */
export function sectionForPath(pathname: string, basePath = DEFAULT_BASE_PATH): SectionDef | undefined {
  const clean = pathname.replace(/\/+$/, "");
  return SECTIONS.find((s) => {
    const own = sectionPath(s, basePath);
    return clean === own || clean.startsWith(`${own}/`);
  });
}

/** The section to highlight: the owner of the path, else the default one. */
export function activeSection(pathname: string, basePath = DEFAULT_BASE_PATH): SectionDef {
  return sectionForPath(pathname, basePath) ?? (SECTIONS[0] as SectionDef);
}

/** The section at this path is hidden by a server capability that is off. */
export function isBlockedByCapability(pathname: string, flags: Capabilities, basePath = DEFAULT_BASE_PATH): boolean {
  const section = sectionForPath(pathname, basePath);
  return Boolean(section?.feature && !flags[section.feature]);
}

/** The section at this path only exists in the desktop shell. */
export function isBlockedByPlatform(pathname: string, desktop: boolean, basePath = DEFAULT_BASE_PATH): boolean {
  const section = sectionForPath(pathname, basePath);
  return Boolean(section?.desktopOnly && !desktop);
}
