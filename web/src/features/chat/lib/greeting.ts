import type { AuthUser } from "@/lib/api/types";
import { dayPeriod, type DayPeriod } from "./format";

export const TITLE_VARIANTS = 10;

/** Which of the interchangeable wordings uses which inputs. */
export type TitleUse = "both" | "greeting" | "name";

/** One wording with both greeting and name, five with greeting only, four with name only. */
export const TITLE_USES: readonly TitleUse[] = ["both", "greeting", "greeting", "greeting", "greeting", "greeting", "name", "name", "name", "name"];

export function displayNameOf(user: Pick<AuthUser, "displayName" | "username"> | null | undefined): string {
  return (user?.displayName ?? "").trim() || (user?.username ?? "").trim();
}

export function periodOf(user: Pick<AuthUser, "timezone"> | null | undefined, now: Date): DayPeriod {
  return dayPeriod(now, (user?.timezone ?? "").trim() || "Etc/UTC");
}
