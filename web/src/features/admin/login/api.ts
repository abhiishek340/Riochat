import { api, publicApi } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { isRecord } from "@/lib/utils/guards";
import type { IdentityProvider } from "./rules";

export async function listProviders(signal?: AbortSignal): Promise<IdentityProvider[]> {
  const out = await api.get<{ results?: IdentityProvider[] }>("/admin/auth/providers", { signal });
  return out?.results ?? [];
}

export function createProvider(body: Record<string, unknown>): Promise<IdentityProvider> {
  return api.post<IdentityProvider>("/admin/auth/providers", body);
}

export function updateProvider(publicId: string, body: Record<string, unknown>): Promise<IdentityProvider> {
  return api.patch<IdentityProvider>(`/admin/auth/providers/${seg(publicId)}`, body);
}

export async function deleteProvider(publicId: string, force: boolean): Promise<void> {
  await api.delete(`/admin/auth/providers/${seg(publicId)}`, { query: force ? { force: true } : undefined });
}

export async function reorderProviders(ids: string[]): Promise<void> {
  await api.patch("/admin/auth/provider-order", { providerIDs: ids });
}

export interface LoginBridge {
  enabled: boolean;
  callbackBaseURL: string;
}

/** The public login options tell whether the provider auth bridge works and where callbacks go. */
export async function loadBridge(signal?: AbortSignal): Promise<LoginBridge> {
  const out = await publicApi.get<unknown>("/auth/login-options", { signal });
  const bridge = isRecord(out) && isRecord(out.providerAuthBridge) ? out.providerAuthBridge : {};
  return { enabled: bridge.enabled === true, callbackBaseURL: typeof bridge.callbackBaseURL === "string" ? bridge.callbackBaseURL : "" };
}
