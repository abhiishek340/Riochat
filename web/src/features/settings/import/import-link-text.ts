import type { LinkError } from "../models/link-parser";

/** Message key that explains a rejected link. */
export function linkErrorKey(reason: LinkError["reason"]) {
  return `settings.import.errors.${reason}` as const;
}
