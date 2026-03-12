import { isAccent, type Accent } from "./config";

/**
 * The account stores preset names from the server's fixed list; the client has
 * four accents of its own. Each accent writes one server name and every server
 * name reads back as some accent (see web/GAPS.md).
 */
export const ACCOUNT_PRESET_OF: Record<Accent, string> = {
  tide: "azure",
  ember: "ochre",
  moss: "lagoon",
  dusk: "ink",
};
const ACCENT_OF_ACCOUNT_PRESET: Record<string, Accent> = {
  azure: "tide",
  ochre: "ember",
  lagoon: "moss",
  ink: "dusk",
  default: "tide",
  cobalt: "tide",
  graphite: "dusk",
  sepia: "ember",
};

export function accentFromAccountPreset(value: unknown): Accent | undefined {
  if (typeof value !== "string") return undefined;
  if (isAccent(value)) return value;
  return ACCENT_OF_ACCOUNT_PRESET[value];
}
