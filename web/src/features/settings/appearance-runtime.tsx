"use client";

import { AppearanceProvider, AppearanceSync } from "./lib/appearance-device";

/**
 * Mounted once for the whole app: keeps the font preferences applied to the
 * document and adopts the appearance stored on the signed-in account.
 */
export function AppearanceRuntime() {
  return (
    <AppearanceProvider>
      <AppearanceSync />
    </AppearanceProvider>
  );
}
