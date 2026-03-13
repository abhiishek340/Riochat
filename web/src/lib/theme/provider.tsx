"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { readStorage, writeStorage } from "@/lib/utils/storage";
import {
  ACCENT_STORAGE_KEY,
  DEFAULT_ACCENT,
  DEFAULT_MODE,
  MODE_STORAGE_KEY,
  isAccent,
  isMode,
  type Accent,
  type ThemeMode,
} from "./config";

interface ThemeValue {
  mode: ThemeMode;
  accent: Accent;
  /** What is actually painted: "system" resolved against the OS setting. */
  resolved: "light" | "dark";
  setMode: (mode: ThemeMode) => void;
  setAccent: (accent: Accent) => void;
}

// Outside a provider the API is inert.
const Ctx = createContext<ThemeValue>({
  mode: DEFAULT_MODE,
  accent: DEFAULT_ACCENT,
  resolved: "light",
  setMode: () => undefined,
  setAccent: () => undefined,
});

function systemPrefersDark(): boolean {
  return typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function applyToDocument(resolved: "light" | "dark", accent: Accent): void {
  const root = document.documentElement;
  root.classList.remove("light", "dark");
  root.classList.add(resolved);
  root.setAttribute("data-accent", accent);
  root.style.colorScheme = resolved;
}

function readStored(): { mode: ThemeMode; accent: Accent } {
  const mode = readStorage(MODE_STORAGE_KEY);
  const accent = readStorage(ACCENT_STORAGE_KEY);
  return { mode: isMode(mode) ? mode : DEFAULT_MODE, accent: isAccent(accent) ? accent : DEFAULT_ACCENT };
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<ThemeMode>(DEFAULT_MODE);
  const [accent, setAccentState] = useState<Accent>(DEFAULT_ACCENT);
  const [systemDark, setSystemDark] = useState(false);
  const [hydrated, setHydrated] = useState(false);

  // Adopt what the bootstrap script already painted.
  useEffect(() => {
    const stored = readStored();
    setModeState(stored.mode);
    setAccentState(stored.accent);
    setSystemDark(systemPrefersDark());
    setHydrated(true);
  }, []);

  // `system` tracks the OS live.
  useEffect(() => {
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => setSystemDark(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);

  // Follow changes made by other tabs or documents on this origin.
  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== null && event.key !== MODE_STORAGE_KEY && event.key !== ACCENT_STORAGE_KEY) return;
      const stored = readStored();
      setModeState(stored.mode);
      setAccentState(stored.accent);
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const resolved: "light" | "dark" = mode === "system" ? (systemDark ? "dark" : "light") : mode;

  // Until hydrated, defaults would overwrite what the bootstrap script painted.
  useEffect(() => {
    if (hydrated) applyToDocument(resolved, accent);
  }, [hydrated, resolved, accent]);

  const setMode = useCallback((next: ThemeMode) => {
    setModeState(next);
    writeStorage(MODE_STORAGE_KEY, next);
  }, []);
  const setAccent = useCallback((next: Accent) => {
    setAccentState(next);
    writeStorage(ACCENT_STORAGE_KEY, next);
  }, []);

  const value = useMemo(() => ({ mode, accent, resolved, setMode, setAccent }), [mode, accent, resolved, setMode, setAccent]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTheme(): ThemeValue {
  return useContext(Ctx);
}
