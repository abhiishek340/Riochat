/**
 * The provider callback, minus React. Runs once per page load and reports what
 * happened; the page decides which panel to show or where to navigate.
 *
 * Order matters for safety: the stored hand-off is removed before anything is
 * compared, so a grant can be exchanged at most once from it, and the state is
 * compared in constant time.
 */
import { isApiError } from "@/lib/api/errors";
import type { AuthPayload } from "@/lib/api/types";
import { safeNextPath } from "@/lib/utils/navigation";
import { isRecord, str } from "@/lib/utils/guards";
import { dropBridgeRequest, saveChallenge, takeBridgeRequest } from "../lib/handoff-storage";
import { constantTimeEqual } from "../lib/pkce";
import { CLIENT_ID_DESKTOP, CLIENT_ID_WEB, type ExchangeInput } from "./api";
import type { SettledAuth } from "./sign-in";

export type CallbackResult =
  | { kind: "provider-error"; detail: string }
  | { kind: "expired" }
  | { kind: "missing" }
  | { kind: "failed"; error: unknown }
  | { kind: "conflict"; providerSlug: string; email: string }
  | { kind: "challenge-stored"; next: string }
  | { kind: "challenge-lost" }
  | { kind: "signed-in"; next: string }
  | { kind: "empty" }
  | { kind: "bind-session" }
  | { kind: "bind-failed"; error: unknown }
  | { kind: "bound"; next: string };

export interface CallbackDeps {
  desktop: boolean;
  exchangeLogin: (slug: string, input: ExchangeInput) => Promise<AuthPayload>;
  exchangeBind: (slug: string, input: ExchangeInput) => Promise<unknown>;
  hasAccessToken: () => Promise<boolean>;
  settle: (payload: AuthPayload) => Promise<SettledAuth>;
  now?: () => number;
}

export async function processCallback(query: URLSearchParams, deps: CallbackDeps): Promise<CallbackResult> {
  const slug = (query.get("provider") ?? "").trim();
  const providerError = query.get("error");
  if (providerError) {
    // The hand-off is over whatever the reason; its verifier must not linger in the tab.
    if (slug) dropBridgeRequest(slug);
    return { kind: "provider-error", detail: providerError };
  }

  const grant = query.get("grant") ?? "";
  if (!slug || !grant) {
    if (slug) dropBridgeRequest(slug);
    return { kind: "missing" };
  }

  const stored = takeBridgeRequest(slug, deps.now?.());
  const returnedState = query.get("state") ?? "";
  if (!stored || !constantTimeEqual(stored.state, returnedState)) return { kind: "expired" };

  const next = safeNextPath(stored.next, "/chat");
  const input: ExchangeInput = { clientID: deps.desktop ? CLIENT_ID_DESKTOP : CLIENT_ID_WEB, grant, codeVerifier: stored.verifier };

  if (stored.intent === "bind") {
    if (!(await deps.hasAccessToken())) return { kind: "bind-session" };
    try {
      await deps.exchangeBind(slug, input);
      return { kind: "bound", next };
    } catch (error) {
      return { kind: "bind-failed", error };
    }
  }

  let payload: AuthPayload;
  try {
    payload = await deps.exchangeLogin(slug, input);
  } catch (error) {
    const conflict = conflictOf(error);
    return conflict ?? { kind: "failed", error };
  }
  const outcome = await deps.settle(payload);
  if (outcome.kind === "challenge") {
    return saveChallenge(outcome.token, outcome.methods, deps.now?.()) ? { kind: "challenge-stored", next } : { kind: "challenge-lost" };
  }
  if (outcome.kind === "empty") return { kind: "empty" };
  return { kind: "signed-in", next };
}

function conflictOf(error: unknown): CallbackResult | null {
  if (!isApiError(error) || error.errorCode !== "auth.provider_email_conflict") return null;
  const details = isRecord(error.details) ? error.details : {};
  if (details.action !== "sign_in_then_bind") return null;
  return { kind: "conflict", providerSlug: str(details.providerSlug), email: str(details.email) };
}
