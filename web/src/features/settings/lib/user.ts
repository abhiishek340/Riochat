import type { AuthUser } from "@/lib/api/types";
import { bool, num, str } from "@/lib/utils/guards";

/** Account fields the shared session type leaves untyped. */
export function usernameChangedAt(user: AuthUser): string {
  return str(user.usernameChangedAt);
}
export function emailSource(user: AuthUser): string {
  return str(user.emailSource);
}
export function emailBootstrapUsed(user: AuthUser): boolean {
  return str(user.emailBootstrapUsedAt) !== "";
}
export function recoveryCount(user: AuthUser): number {
  return num(user.twoFactorRecoveryCount, 0);
}
export function isEmailVerified(user: AuthUser): boolean {
  return Boolean(user.emailVerifiedAt);
}
export function hasPassword(user: AuthUser): boolean {
  return bool(user.passwordEnabled, false);
}
