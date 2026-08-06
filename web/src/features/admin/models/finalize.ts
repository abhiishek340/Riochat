/** What happens to the capabilities at save: the automatic context window is settled. */
import type { Caps } from "./capabilities";
import { removeWindow, resolveAutoWindow, sameIdentity, windowOf, writeAutoWindow } from "./context";

export interface FinalizeInput {
  caps: Caps;
  /** The catalog was consulted (the resolve call answered), and what it said. */
  catalog: { consulted: boolean; window: number | null };
  /** The OpenRouter match; looked up only when the catalog had nothing. */
  openRouter: number | null;
  /** The model as stored, when editing. */
  existing: { name: string; vendor: string } | null;
  form: { name: string; vendor: string };
}

/** A custom window is left alone; automatic mode stores the detected value with the marker, or nothing. */
export function finalizeCaps(input: FinalizeInput): Caps {
  const state = windowOf(input.caps);
  if (state.mode === "custom") return input.caps;
  const detected = resolveAutoWindow({
    catalog: input.catalog.window,
    openRouter: input.openRouter,
    previousAuto: state.mode === "auto" ? state.value : null,
    catalogConsulted: input.catalog.consulted,
    identityUnchanged: input.existing !== null && sameIdentity(input.existing, input.form),
  });
  return detected === null ? removeWindow(input.caps) : writeAutoWindow(input.caps, detected);
}

/** True when the catalog answer is not enough and OpenRouter should be asked. */
export function needsOpenRouter(caps: Caps, catalogWindow: number | null): boolean {
  return windowOf(caps).mode !== "custom" && catalogWindow === null;
}
