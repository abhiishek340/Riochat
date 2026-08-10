/**
 * The upstream protocols a personal model can be served through: display
 * names, grouping into per-vendor choices and the combined image/video pairs.
 * Labels are English on purpose (they name vendor endpoints).
 */

export type ProtocolKind = "chat" | "image" | "video";

interface ProtocolInfo {
  id: string;
  endpoint: string;
  vendor: string;
  kind: ProtocolKind;
}

const CATALOG: readonly ProtocolInfo[] = [
  { id: "openai_chat_completions", endpoint: "Chat Completions", vendor: "OpenAI", kind: "chat" },
  { id: "openai_responses", endpoint: "Responses", vendor: "OpenAI", kind: "chat" },
  { id: "openai_image_generations", endpoint: "Images Generations", vendor: "OpenAI", kind: "image" },
  { id: "openai_image_edits", endpoint: "Images Edits", vendor: "OpenAI", kind: "image" },
  { id: "anthropic_messages", endpoint: "Messages", vendor: "Anthropic", kind: "chat" },
  { id: "google_generate_content", endpoint: "Generate Content", vendor: "Google", kind: "chat" },
  { id: "google_image_generation", endpoint: "Image Generation", vendor: "Google", kind: "image" },
  { id: "gemini_interactions", endpoint: "Interactions", vendor: "Google", kind: "chat" },
  { id: "xai_responses", endpoint: "Responses", vendor: "xAI", kind: "chat" },
  { id: "xai_image", endpoint: "Images Generations", vendor: "xAI", kind: "image" },
  { id: "xai_image_edits", endpoint: "Images Edits", vendor: "xAI", kind: "image" },
  { id: "xai_video", endpoint: "Video Generations", vendor: "xAI", kind: "video" },
  { id: "xai_video_extensions", endpoint: "Video Extensions", vendor: "xAI", kind: "video" },
  { id: "openrouter_chat_completions", endpoint: "Chat Completions", vendor: "OpenRouter", kind: "chat" },
  { id: "openrouter_responses", endpoint: "Responses", vendor: "OpenRouter", kind: "chat" },
  { id: "openrouter_images", endpoint: "Images", vendor: "OpenRouter", kind: "image" },
];

const BY_ID = new Map(CATALOG.map((p) => [p.id, p]));

/** Protocol pairs that travel together, with the label of the combination. */
const PAIRS: readonly { members: readonly [string, string]; endpoint: string }[] = [
  { members: ["openai_image_generations", "openai_image_edits"], endpoint: "Images Generations + Edits" },
  { members: ["xai_image", "xai_image_edits"], endpoint: "Images Generations + Edits" },
  { members: ["xai_video", "xai_video_extensions"], endpoint: "Video Generations + Extensions" },
];

/** The eight protocols a provider (and an import link) may name. */
export const IMPORT_PROTOCOLS = [
  "openai_chat_completions",
  "openai_responses",
  "anthropic_messages",
  "google_generate_content",
  "gemini_interactions",
  "xai_responses",
  "openrouter_chat_completions",
  "openrouter_responses",
] as const;
export type ImportProtocol = (typeof IMPORT_PROTOCOLS)[number];
export const DEFAULT_PROTOCOL: ImportProtocol = "openai_chat_completions";

export function isImportProtocol(value: unknown): value is ImportProtocol {
  return typeof value === "string" && (IMPORT_PROTOCOLS as readonly string[]).includes(value);
}

export function protocolKind(id: string): ProtocolKind {
  return BY_ID.get(id)?.kind ?? "chat";
}

/** "Responses (OpenAI)"; an unknown protocol is shown as it is. */
export function protocolLabel(id: string): string {
  const info = BY_ID.get(id);
  return info ? `${info.endpoint} (${info.vendor})` : id;
}

export interface ProtocolChoice {
  /** Member protocols joined by "+", in the order the server lists them. */
  value: string;
  label: string;
  kind: ProtocolKind;
  members: string[];
}

export function joinProtocols(protocols: readonly string[]): string {
  return protocols.join("+");
}
export function splitProtocols(value: string): string[] {
  return value.split("+").filter(Boolean);
}

/**
 * Builds the per-model protocol choices from the protocols the server offers:
 * grouped by vendor (first appearance wins), and a pair shows up as one
 * combined choice directly before the first protocol of the pair.
 */
export function buildProtocolChoices(offered: readonly string[]): ProtocolChoice[] {
  const present = new Set(offered);
  const vendorOrder: string[] = [];
  const perVendor = new Map<string, string[]>();
  for (const id of offered) {
    const vendor = BY_ID.get(id)?.vendor ?? "";
    if (!perVendor.has(vendor)) {
      perVendor.set(vendor, []);
      vendorOrder.push(vendor);
    }
    perVendor.get(vendor)?.push(id);
  }
  const out: ProtocolChoice[] = [];
  for (const vendor of vendorOrder) {
    for (const id of perVendor.get(vendor) ?? []) {
      const pair = PAIRS.find((p) => p.members[0] === id && present.has(p.members[1]));
      if (pair) {
        const info = BY_ID.get(id);
        out.push({
          value: joinProtocols(pair.members),
          label: `${pair.endpoint} (${info?.vendor ?? ""})`,
          kind: info?.kind ?? "image",
          members: [...pair.members],
        });
      }
      out.push({ value: id, label: protocolLabel(id), kind: protocolKind(id), members: [id] });
    }
  }
  return out;
}

/** Adds the current value as a choice when the offered ones do not contain it. */
export function withCurrentChoice(choices: readonly ProtocolChoice[], current: string): ProtocolChoice[] {
  if (!current || choices.some((c) => c.value === current)) return [...choices];
  const members = splitProtocols(current);
  return [
    ...choices,
    { value: current, label: members.map(protocolLabel).join(" + "), kind: protocolKind(members[0] ?? ""), members },
  ];
}
