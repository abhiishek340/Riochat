/** Recognised provider endpoints: used to pick a protocol, a name and an icon. */
import { DEFAULT_PROTOCOL, type ImportProtocol } from "./protocols";

export interface KnownProvider {
  host: string;
  name: string;
  protocol: ImportProtocol;
  icon: string;
}

const KNOWN: readonly KnownProvider[] = [
  { host: "api.openai.com", name: "OpenAI", protocol: "openai_chat_completions", icon: "hexagon" },
  { host: "api.anthropic.com", name: "Anthropic", protocol: "anthropic_messages", icon: "feather" },
  { host: "generativelanguage.googleapis.com", name: "Google AI", protocol: "google_generate_content", icon: "sparkles" },
  { host: "api.x.ai", name: "xAI", protocol: "xai_responses", icon: "orbit" },
  { host: "openrouter.ai", name: "OpenRouter", protocol: "openrouter_chat_completions", icon: "network" },
  { host: "api.deepseek.com", name: "DeepSeek", protocol: "openai_chat_completions", icon: "telescope" },
  { host: "api.mistral.ai", name: "Mistral", protocol: "openai_chat_completions", icon: "wind" },
  { host: "api.groq.com", name: "Groq", protocol: "openai_chat_completions", icon: "zap" },
  { host: "api.together.xyz", name: "Together", protocol: "openai_chat_completions", icon: "layers" },
  { host: "api.moonshot.cn", name: "Moonshot", protocol: "openai_chat_completions", icon: "moon" },
  { host: "dashscope.aliyuncs.com", name: "DashScope", protocol: "openai_chat_completions", icon: "cloud" },
];

/** Host name of a base URL, lower-cased; "" when it does not parse. */
export function hostOf(baseURL: string): string {
  try {
    return new URL(baseURL.trim()).hostname.toLowerCase();
  } catch {
    return "";
  }
}

export function recognizeProvider(baseURL: string): KnownProvider | undefined {
  const host = hostOf(baseURL);
  if (!host) return undefined;
  return KNOWN.find((k) => host === k.host || host.endsWith(`.${k.host}`));
}

/** Explicit protocol, else the recognised provider's, else Chat Completions. */
export function resolveProtocol(baseURL: string, explicit?: string): string {
  if (explicit) return explicit;
  return recognizeProvider(baseURL)?.protocol ?? DEFAULT_PROTOCOL;
}
