/**
 * Upstream protocol adapters and model kinds. Protocol names and their
 * labels are the vendors' own terms and are the same in every language.
 */

export const KINDS = ["chat", "audio", "image_gen", "image_edit", "video_gen", "video_extension"] as const;
export type Kind = (typeof KINDS)[number];

/** Canonical display order: OpenAI, Anthropic, Google, xAI, OpenRouter; chat before image before video. */
export const PROTOCOLS = [
  "openai_chat_completions",
  "openai_responses",
  "openai_image_generations",
  "openai_image_edits",
  "openai_video_generations",
  "anthropic_messages",
  "google_generate_content",
  "google_image_generation",
  "gemini_interactions",
  "xai_responses",
  "xai_image",
  "xai_image_edits",
  "xai_video",
  "xai_video_extensions",
  "openrouter_chat_completions",
  "openrouter_responses",
  "openrouter_images",
] as const;
export type Protocol = (typeof PROTOCOLS)[number];

const LABELS: Record<Protocol, string> = {
  openai_chat_completions: "Chat Completions (OpenAI)",
  openai_responses: "Responses (OpenAI)",
  openai_image_generations: "Image Generations (OpenAI)",
  openai_image_edits: "Image Edits (OpenAI)",
  openai_video_generations: "Video Generations (OpenAI)",
  anthropic_messages: "Messages (Anthropic)",
  google_generate_content: "Generate Content (Google)",
  google_image_generation: "Image Generation (Google)",
  gemini_interactions: "Interactions (Gemini)",
  xai_responses: "Responses (xAI)",
  xai_image: "Image (xAI)",
  xai_image_edits: "Image Edits (xAI)",
  xai_video: "Video (xAI)",
  xai_video_extensions: "Video Extensions (xAI)",
  openrouter_chat_completions: "Chat Completions (OpenRouter)",
  openrouter_responses: "Responses (OpenRouter)",
  openrouter_images: "Images (OpenRouter)",
};

export function isProtocol(value: string): value is Protocol {
  return (PROTOCOLS as readonly string[]).includes(value);
}

/** Unknown protocols render as their raw name. */
export function protocolLabel(protocol: string): string {
  return isProtocol(protocol) ? LABELS[protocol] : protocol;
}

export function isKind(value: string): value is Kind {
  return (KINDS as readonly string[]).includes(value);
}

const KIND_BY_PROTOCOL: Record<Protocol, readonly Kind[]> = {
  openai_chat_completions: ["chat"],
  openai_responses: ["chat"],
  openai_image_generations: ["image_gen"],
  openai_image_edits: ["image_edit"],
  openai_video_generations: ["video_gen"],
  anthropic_messages: ["chat"],
  google_generate_content: ["chat"],
  google_image_generation: ["image_gen", "image_edit"],
  gemini_interactions: ["chat", "image_gen", "image_edit", "video_gen"],
  xai_responses: ["chat"],
  xai_image: ["image_gen"],
  xai_image_edits: ["image_edit"],
  xai_video: ["video_gen"],
  xai_video_extensions: ["video_extension"],
  openrouter_chat_completions: ["chat"],
  openrouter_responses: ["chat"],
  openrouter_images: ["image_gen", "image_edit"],
};

/** Protocols that can serve each kind, used for per-upstream defaults. */
export const PROTOCOLS_BY_KIND: Record<Kind, readonly Protocol[]> = {
  chat: ["openai_chat_completions", "openai_responses", "anthropic_messages", "google_generate_content", "gemini_interactions", "xai_responses", "openrouter_chat_completions", "openrouter_responses"],
  audio: ["openai_chat_completions", "openai_responses", "anthropic_messages", "google_generate_content", "xai_responses", "openrouter_chat_completions", "openrouter_responses"],
  image_gen: ["openai_image_generations", "google_image_generation", "gemini_interactions", "xai_image", "openrouter_images"],
  image_edit: ["openai_image_edits", "google_image_generation", "gemini_interactions", "xai_image_edits", "openrouter_images"],
  video_gen: ["openai_video_generations", "gemini_interactions", "xai_video"],
  video_extension: ["xai_video_extensions"],
};

export function sortProtocols(protocols: readonly string[]): string[] {
  const rank = (p: string) => {
    const i = (PROTOCOLS as readonly string[]).indexOf(p);
    return i < 0 ? PROTOCOLS.length : i;
  };
  return [...new Set(protocols)].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
}

/** Kinds served by a set of protocols (union, canonical order); `chat` when nothing is known. */
export function kindsForProtocols(protocols: readonly string[]): Kind[] {
  const found = new Set<Kind>();
  for (const p of protocols) if (isProtocol(p)) for (const k of KIND_BY_PROTOCOL[p]) found.add(k);
  const out = KINDS.filter((k) => found.has(k));
  return out.length ? out : ["chat"];
}

const PAIRS: ReadonlyArray<readonly [Protocol, Protocol]> = [
  ["openai_image_generations", "openai_image_edits"],
  ["xai_image", "xai_image_edits"],
  ["xai_video", "xai_video_extensions"],
];

/** One protocol, or exactly one of the three generation + edit/extension pairs. */
export function isAllowedSelection(protocols: readonly string[]): boolean {
  const set = new Set(protocols);
  if (set.size === 1) return true;
  if (set.size !== 2) return false;
  return PAIRS.some(([a, b]) => set.has(a) && set.has(b));
}

/** Result of picking `next` as the new multi-select value: an unsupported combination collapses to the picked protocol alone. */
export function resolveSelection(previous: readonly string[], next: readonly string[]): string[] {
  const unique = sortProtocols(next);
  if (unique.length === 0) return [];
  if (isAllowedSelection(unique)) return unique;
  const added = unique.filter((p) => !previous.includes(p));
  const pick = added[added.length - 1] ?? unique[unique.length - 1];
  return pick ? [pick] : [];
}

/** True when the protocol talks to an image endpoint (image streaming toggle applies). */
export function isImageProtocol(protocol: string): boolean {
  return ["openai_image_generations", "openai_image_edits", "google_image_generation", "openrouter_images", "xai_image", "xai_image_edits"].includes(protocol);
}

export function parseKinds(text: string | null | undefined): Kind[] {
  if (!text) return [];
  try {
    const value: unknown = JSON.parse(text);
    if (!Array.isArray(value)) return [];
    return KINDS.filter((k) => value.includes(k));
  } catch {
    return [];
  }
}

export function parseStringList(text: string | null | undefined): string[] {
  if (!text) return [];
  try {
    const value: unknown = JSON.parse(text);
    return Array.isArray(value) ? value.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

export function kindsJson(kinds: readonly string[]): string {
  return JSON.stringify(KINDS.filter((k) => kinds.includes(k)));
}

/** Vendor key from the compat names used by upstreams. */
export const COMPATIBLE = ["openai", "anthropic", "google", "xai", "openrouter", "custom"] as const;
export type Compatible = (typeof COMPATIBLE)[number];

export const COMPATIBLE_LABELS: Record<Compatible, string> = {
  openai: "OpenAI",
  anthropic: "Anthropic",
  google: "Google",
  xai: "xAI",
  openrouter: "OpenRouter",
  custom: "Custom",
};
