/**
 * Decides what kind of generation a send is, from the model's declared kinds,
 * the attachments and the requested response type. Pure, so the table of
 * outcomes can be tested exhaustively.
 */
import { isRecord } from "@/lib/utils/guards";

export type ModelKind = "chat" | "audio" | "image_gen" | "image_edit" | "video_gen";
export type TaskKind = "chat" | "image_generation" | "image_edit" | "video_generation" | "video_extension";
export type BlockCode = "B1" | "B2" | "B3" | "B4" | "B5" | "B6" | "B7" | "B8" | "B9";
export type ResponseType = "image" | "video" | "text" | null;

export interface AttachmentFacts {
  fileCategory: string;
  mimeType: string;
  detectedMime: string;
}

export interface ModelFacts {
  kinds: ReadonlySet<string>;
  supportsVideoExtension: boolean;
}

export interface TaskDecision {
  task: TaskKind;
  blocked: BlockCode | null;
}

const KNOWN_KINDS: readonly string[] = ["chat", "audio", "image_gen", "image_edit", "video_gen"];

/** Reads the `kindsJSON` string of a model into a normalised set. */
export function parseKinds(kindsJson: string | null | undefined): Set<string> {
  const out = new Set<string>();
  if (!kindsJson) return out;
  try {
    const parsed: unknown = JSON.parse(kindsJson);
    if (Array.isArray(parsed)) {
      for (const k of parsed) if (typeof k === "string") out.add(k.trim().toLowerCase());
    }
  } catch {
    /* unreadable kinds count as "unknown", i.e. chat capable */
  }
  return out;
}

/** An empty or fully unknown set counts as chat-capable; audio models also chat. */
export function isChatCapable(kinds: ReadonlySet<string>): boolean {
  if (kinds.has("chat") || kinds.has("audio")) return true;
  for (const k of kinds) if (KNOWN_KINDS.includes(k)) return false;
  return true;
}

const lower = (v: string | undefined) => (v ?? "").trim().toLowerCase();

export function isImageAttachment(a: AttachmentFacts): boolean {
  return lower(a.fileCategory) === "image" || lower(a.mimeType).startsWith("image/");
}

export function isVideoAttachment(a: AttachmentFacts): boolean {
  return lower(a.mimeType).startsWith("video/") || lower(a.detectedMime).startsWith("video/");
}

export function isMp4Attachment(a: AttachmentFacts): boolean {
  return lower(a.mimeType) === "video/mp4" || lower(a.detectedMime) === "video/mp4";
}

/** `response_format` as an object or array of objects; priority video, image, text. */
export function requestedResponseType(options: Record<string, unknown> | null | undefined): ResponseType {
  const raw = options?.response_format;
  const list = Array.isArray(raw) ? raw : raw === undefined ? [] : [raw];
  const types = new Set<string>();
  for (const item of list) {
    if (isRecord(item) && typeof item.type === "string") types.add(item.type.trim().toLowerCase());
  }
  if (types.has("video")) return "video";
  if (types.has("image")) return "image";
  if (types.has("text")) return "text";
  return null;
}

export function decideTask(input: {
  model: ModelFacts;
  attachments: readonly AttachmentFacts[];
  requested: ResponseType;
}): TaskDecision {
  const { model, attachments, requested } = input;
  const chat = isChatCapable(model.kinds);
  const genImage = model.kinds.has("image_gen");
  const editImage = model.kinds.has("image_edit");
  const genVideo = model.kinds.has("video_gen");
  const ext = model.supportsVideoExtension;

  const images = attachments.filter(isImageAttachment);
  const nonImages = attachments.filter((a) => !isImageAttachment(a));
  const hasVideo = attachments.some(isVideoAttachment);

  // 1. A video attachment aimed at a model that can extend videos (or cannot chat).
  if (hasVideo && (ext || !chat)) {
    if (!(attachments.length === 1 && isMp4Attachment(attachments[0] as AttachmentFacts))) {
      return { task: "video_extension", blocked: "B1" };
    }
    if (!ext) return { task: "video_extension", blocked: "B2" };
    return { task: "video_extension", blocked: null };
  }

  // 2. Files that are not images, for a model whose job is media.
  if (nonImages.length > 0 && (genImage || editImage || genVideo) && (images.length > 0 || !chat)) {
    if (images.length > 0 && editImage) return { task: "image_edit", blocked: "B3" };
    if (genVideo) return { task: "video_generation", blocked: "B4" };
    if (genImage) return { task: "image_generation", blocked: "B3" };
    return { task: "chat", blocked: "B3" };
  }

  // 3. Images only.
  if (images.length > 0 && nonImages.length === 0) {
    const tooMany = images.length > 1;
    if (requested === "video" && genVideo) return { task: "video_generation", blocked: tooMany ? "B5" : null };
    if (editImage) return { task: "image_edit", blocked: null };
    if (genVideo) return { task: "video_generation", blocked: tooMany ? "B5" : null };
    if (chat) return { task: "chat", blocked: null };
    return { task: "chat", blocked: "B6" };
  }

  // 4. Other attachments (also a mix with a chat model that has no media task).
  if (attachments.length > 0) {
    if (chat) return { task: "chat", blocked: null };
    if (genImage) return { task: "image_generation", blocked: "B7" };
    if (genVideo) return { task: "video_generation", blocked: "B4" };
    return { task: "chat", blocked: "B9" };
  }

  // 5. No attachments.
  if (requested === "video" && genVideo) return { task: "video_generation", blocked: null };
  if (requested === "image" && genImage) return { task: "image_generation", blocked: null };
  if (chat) return { task: "chat", blocked: null };
  if (genImage) return { task: "image_generation", blocked: null };
  if (genVideo) return { task: "video_generation", blocked: null };
  if (editImage) return { task: "image_edit", blocked: "B8" };
  return { task: "chat", blocked: "B9" };
}

export function isMediaTask(task: TaskKind): boolean {
  return task !== "chat";
}

/** Content type of the reply a task produces. */
export function replyContentType(task: TaskKind): "markdown" | "image" | "video" {
  if (task === "chat") return "markdown";
  return task === "image_generation" || task === "image_edit" ? "image" : "video";
}

export function taskEndpoint(conversationId: string, task: TaskKind): string {
  const base = `/conversations/${encodeURIComponent(conversationId)}`;
  switch (task) {
    case "chat":
      return `${base}/messages/stream`;
    case "image_generation":
      return `${base}/media/images/generations/stream`;
    case "image_edit":
      return `${base}/media/images/edits/stream`;
    case "video_generation":
      return `${base}/media/videos/generations/stream`;
    case "video_extension":
      return `${base}/media/videos/extensions/stream`;
  }
}

export type PlaceholderShape = "wide" | "portrait" | "square";

/** Loading placeholder shape from a `size` option such as "1024x768". */
export function placeholderShape(options: Record<string, unknown> | null | undefined): PlaceholderShape {
  const size = options?.size;
  if (typeof size !== "string") return "wide";
  const m = /^\s*(\d+)\s*[x×*]\s*(\d+)\s*$/i.exec(size);
  if (!m) return "wide";
  const w = Number(m[1]);
  const h = Number(m[2]);
  if (!(w > 0 && h > 0)) return "wide";
  if (w > h) return "wide";
  if (h > w) return "portrait";
  return "square";
}

/** Only `duration` is sent for a video extension: an integer 2..10, else 6. */
export function extensionOptions(options: Record<string, unknown> | null | undefined): { duration: number } {
  const raw = options?.duration;
  const n = typeof raw === "string" && raw.trim() !== "" ? Number(raw) : raw;
  if (typeof n === "number" && Number.isInteger(n) && n >= 2 && n <= 10) return { duration: n };
  return { duration: 6 };
}
