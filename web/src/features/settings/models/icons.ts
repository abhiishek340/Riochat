import {
  Anchor,
  Atom,
  Bird,
  Bot,
  Brain,
  Cloud,
  Compass,
  Cpu,
  Database,
  Diamond,
  Feather,
  FlaskConical,
  Flame,
  Gem,
  Globe,
  Hexagon,
  KeyRound,
  Layers,
  Leaf,
  Moon,
  Mountain,
  Network,
  Orbit,
  Rocket,
  Server,
  Sparkles,
  Star,
  Sun,
  Telescope,
  Terminal,
  Triangle,
  Waves,
  Wind,
  Zap,
  type LucideIcon,
} from "lucide-react";

/** Built-in provider icons. The stored value is the slug; uploads are not supported. */
export interface ProviderIcon {
  id: string;
  name: string;
  Icon: LucideIcon;
}

export const PROVIDER_ICONS: readonly ProviderIcon[] = [
  { id: "sparkles", name: "Sparkles", Icon: Sparkles },
  { id: "bot", name: "Robot", Icon: Bot },
  { id: "brain", name: "Brain", Icon: Brain },
  { id: "cloud", name: "Cloud", Icon: Cloud },
  { id: "server", name: "Server", Icon: Server },
  { id: "cpu", name: "Chip", Icon: Cpu },
  { id: "zap", name: "Bolt", Icon: Zap },
  { id: "globe", name: "Globe", Icon: Globe },
  { id: "key", name: "Key", Icon: KeyRound },
  { id: "flask", name: "Flask", Icon: FlaskConical },
  { id: "rocket", name: "Rocket", Icon: Rocket },
  { id: "star", name: "Star", Icon: Star },
  { id: "moon", name: "Moon", Icon: Moon },
  { id: "sun", name: "Sun", Icon: Sun },
  { id: "leaf", name: "Leaf", Icon: Leaf },
  { id: "flame", name: "Flame", Icon: Flame },
  { id: "gem", name: "Gem", Icon: Gem },
  { id: "compass", name: "Compass", Icon: Compass },
  { id: "feather", name: "Feather", Icon: Feather },
  { id: "atom", name: "Atom", Icon: Atom },
  { id: "orbit", name: "Orbit", Icon: Orbit },
  { id: "hexagon", name: "Hexagon", Icon: Hexagon },
  { id: "triangle", name: "Triangle", Icon: Triangle },
  { id: "diamond", name: "Diamond", Icon: Diamond },
  { id: "anchor", name: "Anchor", Icon: Anchor },
  { id: "bird", name: "Bird", Icon: Bird },
  { id: "telescope", name: "Telescope", Icon: Telescope },
  { id: "layers", name: "Layers", Icon: Layers },
  { id: "network", name: "Network", Icon: Network },
  { id: "terminal", name: "Terminal", Icon: Terminal },
  { id: "database", name: "Database", Icon: Database },
  { id: "waves", name: "Waves", Icon: Waves },
  { id: "mountain", name: "Mountain", Icon: Mountain },
  { id: "wind", name: "Wind", Icon: Wind },
];

const BY_ID = new Map(PROVIDER_ICONS.map((i) => [i.id, i]));
export const FALLBACK_ICON: LucideIcon = Server;
export const ICON_GRID_LIMIT = 120;

/** Icon for a stored slug; empty or unknown falls back to the recognised provider's, then to a server glyph. */
export function iconFor(slug: string, automaticSlug?: string): LucideIcon {
  return BY_ID.get(slug)?.Icon ?? (automaticSlug ? BY_ID.get(automaticSlug)?.Icon : undefined) ?? FALLBACK_ICON;
}

/** Case-insensitive match on id or name. */
export function searchIcons(query: string): ProviderIcon[] {
  const q = query.trim().toLowerCase();
  if (!q) return [...PROVIDER_ICONS];
  return PROVIDER_ICONS.filter((i) => i.id.includes(q) || i.name.toLowerCase().includes(q));
}
