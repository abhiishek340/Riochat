"use client";

import { useEffect, useState, useSyncExternalStore } from "react";
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from "lucide-react";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { Spinner } from "./spinner";
import {
  DEFAULT_TOAST_POSITION,
  TOAST_POSITION_EVENT,
  TOAST_POSITION_KEY,
  dismiss,
  getToasts,
  readToastPosition,
  subscribeToasts,
  type ToastItem,
  type ToastKind,
  type ToastPosition,
} from "./toast-store";

const ICONS: Record<ToastKind, React.ReactNode> = {
  info: <Info className="size-4 text-info" />,
  success: <CheckCircle2 className="size-4 text-success" />,
  warning: <AlertTriangle className="size-4 text-warning" />,
  error: <XCircle className="size-4 text-destructive" />,
  loading: <Spinner className="text-muted-foreground" />,
};

const PLACEMENT: Record<ToastPosition, string> = {
  "top-left": "top-4 left-4 items-start",
  "top-center": "top-4 left-1/2 -translate-x-1/2 items-center",
  "top-right": "top-4 right-4 items-end",
  "bottom-left": "bottom-4 left-4 items-start flex-col-reverse",
  "bottom-center": "bottom-4 left-1/2 -translate-x-1/2 items-center flex-col-reverse",
  "bottom-right": "bottom-4 right-4 items-end flex-col-reverse",
};

const serverSnapshot: ToastItem[] = [];

/** Mount once near the root. Placement is a per-device preference synced across tabs. */
export function Toaster() {
  const t = useT();
  const items = useSyncExternalStore(subscribeToasts, getToasts, () => serverSnapshot);
  const [position, setPosition] = useState<ToastPosition>(DEFAULT_TOAST_POSITION);

  useEffect(() => {
    const sync = () => setPosition(readToastPosition());
    sync();
    const onStorage = (e: StorageEvent) => {
      if (e.key === null || e.key === TOAST_POSITION_KEY) sync();
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(TOAST_POSITION_EVENT, sync);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(TOAST_POSITION_EVENT, sync);
    };
  }, []);

  const fromBottom = position.startsWith("bottom");
  return (
    <div
      aria-live="polite"
      aria-atomic="false"
      style={{ ["--rio-toast-from" as string]: fromBottom ? "8px" : "-8px" }}
      className={cn("pointer-events-none fixed z-[100] flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-2", PLACEMENT[position])}
    >
      {items.map((item) => (
        <div
          key={item.id}
          role={item.kind === "error" ? "alert" : "status"}
          className="pointer-events-auto flex w-full animate-[rio-toast-in_200ms_cubic-bezier(0.22,1,0.36,1)] items-start gap-3 rounded-lg border bg-popover p-3.5 text-popover-foreground shadow-lg"
        >
          <span className="mt-0.5 shrink-0">{ICONS[item.kind]}</span>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium leading-snug">{item.title}</p>
            {item.description ? <p className="mt-0.5 text-[13px] text-muted-foreground">{item.description}</p> : null}
            {item.action ? (
              <button
                type="button"
                className="mt-1.5 text-[13px] font-medium text-primary hover:underline"
                onClick={() => {
                  item.action?.onClick();
                  dismiss(item.id);
                }}
              >
                {item.action.label}
              </button>
            ) : null}
          </div>
          <button
            type="button"
            aria-label={t("common.actions.close")}
            onClick={() => dismiss(item.id)}
            className="-mr-1 -mt-1 inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
          >
            <X className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}
