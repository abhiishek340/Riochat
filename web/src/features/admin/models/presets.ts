/** Starting points for the capabilities of a model. Each is a plain capabilities object. */
import { toolProtocol, type Caps } from "./capabilities";

export type PresetId = "textOnly" | "openaiVision" | "openaiReasoning" | "anthropicThinking" | "geminiThinking" | "imageGeneration";

export interface Preset {
  id: PresetId;
  /** Route protocol the preset is meant for; used to mark presets that do not fit the model. */
  protocol: string;
  caps: Caps;
}

export const PRESETS: readonly Preset[] = [
  {
    id: "textOnly",
    protocol: "",
    caps: { inputModalities: ["text"] },
  },
  {
    id: "openaiVision",
    protocol: "openai_chat_completions",
    caps: { inputModalities: ["text", "image"] },
  },
  {
    id: "openaiReasoning",
    protocol: "openai_responses",
    caps: {
      inputModalities: ["text", "image", "pdf"],
      reasoning: { format: "openai", levels: ["low", "medium", "high"], default: "medium" },
    },
  },
  {
    id: "anthropicThinking",
    protocol: "anthropic_messages",
    caps: {
      inputModalities: ["text", "image", "pdf"],
      reasoning: { format: "anthropic_budget", levels: ["none", "low", "medium", "high"], default: "medium", budgets: { low: 2048, medium: 8192, high: 24576 } },
    },
  },
  {
    id: "geminiThinking",
    protocol: "google_generate_content",
    caps: {
      inputModalities: ["text", "image", "pdf", "audio", "video"],
      reasoning: { format: "gemini_level", levels: ["low", "high"], default: "high" },
    },
  },
  {
    id: "imageGeneration",
    protocol: "openai_image_generations",
    caps: { inputModalities: ["text", "image"], image: { stream: true } },
  },
];

/** A preset fits when it names no protocol or one of the model's route protocols (google spellings are equal). */
export function presetMatches(preset: Preset, routeProtocols: readonly string[]): boolean {
  if (preset.protocol === "") return true;
  return routeProtocols.some((p) => toolProtocol(p) === toolProtocol(preset.protocol));
}

/** Models other than the one being edited that carry a capabilities object, filtered by name. */
export function copySources<T extends { id: number; platformModelName: string; capabilitiesJSON: string }>(models: readonly T[], selfId: number | null, query: string): T[] {
  const q = query.trim().toLowerCase();
  return models.filter((m) => {
    if (m.id === selfId) return false;
    const text = m.capabilitiesJSON.trim();
    if (text === "" || text === "{}") return false;
    return q === "" || m.platformModelName.toLowerCase().includes(q);
  });
}
