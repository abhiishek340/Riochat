/**
 * Module-level toast store so non-React code (API callbacks, event handlers)
 * can raise toasts: `toast.success("Saved")`.
 */
import { readStorage, writeStorage } from "@/lib/utils/storage";

export type ToastKind = "info" | "success" | "error" | "warning" | "loading";

export interface ToastItem {
  id: string;
  kind: ToastKind;
  title: string;
  description?: string;
  /** Milliseconds; 0 keeps it until dismissed. */
  duration: number;
  action?: { label: string; onClick: () => void };
}

export type ToastOptions = Partial<Pick<ToastItem, "id" | "description" | "duration" | "action">>;

const DEFAULT_DURATION: Record<ToastKind, number> = { info: 4000, success: 3000, warning: 5000, error: 6000, loading: 0 };
const MAX_VISIBLE = 4;

let items: ToastItem[] = [];
const listeners = new Set<() => void>();
const timers = new Map<string, ReturnType<typeof setTimeout>>();
let counter = 0;

function emit(): void {
  for (const l of [...listeners]) l();
}

function schedule(item: ToastItem): void {
  const existing = timers.get(item.id);
  if (existing) clearTimeout(existing);
  timers.delete(item.id);
  if (item.duration > 0) timers.set(item.id, setTimeout(() => dismiss(item.id), item.duration));
}

function push(kind: ToastKind, title: string, options: ToastOptions = {}): string {
  const id = options.id ?? `toast-${++counter}`;
  const item: ToastItem = {
    id,
    kind,
    title,
    description: options.description,
    action: options.action,
    duration: options.duration ?? DEFAULT_DURATION[kind],
  };
  const at = items.findIndex((t) => t.id === id);
  items = at >= 0 ? items.map((t) => (t.id === id ? item : t)) : [...items, item].slice(-MAX_VISIBLE);
  schedule(item);
  emit();
  return id;
}

export function dismiss(id?: string): void {
  if (id === undefined) {
    for (const t of timers.values()) clearTimeout(t);
    timers.clear();
    items = [];
  } else {
    const t = timers.get(id);
    if (t) clearTimeout(t);
    timers.delete(id);
    items = items.filter((i) => i.id !== id);
  }
  emit();
}

export const toast = {
  info: (title: string, options?: ToastOptions) => push("info", title, options),
  success: (title: string, options?: ToastOptions) => push("success", title, options),
  warning: (title: string, options?: ToastOptions) => push("warning", title, options),
  error: (title: string, options?: ToastOptions) => push("error", title, options),
  /** Persistent until dismissed or replaced by reusing the returned id. */
  loading: (title: string, options?: ToastOptions) => push("loading", title, options),
  dismiss,
};

export function subscribeToasts(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getToasts(): ToastItem[] {
  return items;
}

// --- placement preference ----------------------------------------------------

export const TOAST_POSITIONS = ["top-left", "top-center", "top-right", "bottom-left", "bottom-center", "bottom-right"] as const;
export type ToastPosition = (typeof TOAST_POSITIONS)[number];
export const DEFAULT_TOAST_POSITION: ToastPosition = "top-right";
export const TOAST_POSITION_KEY = "riochat:toast-position";
export const TOAST_POSITION_EVENT = "riochat:toast-position-updated";

export function readToastPosition(): ToastPosition {
  const v = readStorage(TOAST_POSITION_KEY);
  return (TOAST_POSITIONS as readonly string[]).includes(v ?? "") ? (v as ToastPosition) : DEFAULT_TOAST_POSITION;
}

export function setToastPosition(position: ToastPosition): void {
  writeStorage(TOAST_POSITION_KEY, position);
  window.dispatchEvent(new CustomEvent(TOAST_POSITION_EVENT));
}
