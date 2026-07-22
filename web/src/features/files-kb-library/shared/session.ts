import { resolveAccessToken } from "@/lib/api/client";

/** True when a bearer token is available (cached, or obtainable by a refresh). */
export async function hasSession(): Promise<boolean> {
  return (await resolveAccessToken()) !== "";
}
