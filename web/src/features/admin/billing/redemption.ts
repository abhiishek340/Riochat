/** Redemption codes: validation, availability and the forms' bodies. */
import { problem, type Problem } from "../lib/problem";
import type { BillingMode } from "../lib/wire";

export interface RedemptionCode {
  id: number;
  code?: string;
  codeHint: string;
  mode: "usage" | "period" | (string & {});
  rewardType: string;
  creditUSD: number;
  creditNanousd: number;
  planID: number;
  durationDays: number;
  maxRedemptions: number | null;
  perUserLimit: number;
  redeemedCount: number;
  remainingRedemptions: number | null;
  status: "active" | "inactive" | (string & {});
  expiresAt: string | null;
  description: string;
  createdByUserID: number;
  createdAt: string;
  updatedAt: string;
}

export type Unavailable = "inactive" | "expired" | "exhausted" | "selfMode" | "modeMismatch";

/**
 * Why a code cannot be redeemed right now, by precedence: inactive, expired,
 * exhausted, billing off, then a mode the billing setup does not accept (in
 * `period` mode both kinds are fine, in `usage` mode only usage codes).
 */
export function unavailableReason(code: Pick<RedemptionCode, "status" | "expiresAt" | "remainingRedemptions" | "mode">, billing: BillingMode, now: number = Date.now()): Unavailable | null {
  if (code.status !== "active") return "inactive";
  if (code.expiresAt && Date.parse(code.expiresAt) <= now) return "expired";
  if (code.remainingRedemptions !== null && code.remainingRedemptions <= 0) return "exhausted";
  if (billing === "self") return "selfMode";
  if (billing === "usage" && code.mode !== "usage") return "modeMismatch";
  return null;
}

export interface CodeForm {
  code: string;
  quantity: string;
  mode: "usage" | "period";
  creditUSD: string;
  planId: string;
  durationDays: string;
  maxRedemptions: string;
  perUserLimit: string;
  expiresAt: string;
  description: string;
}

export function blankCodeForm(mode: "usage" | "period" = "usage"): CodeForm {
  return { code: "", quantity: "1", mode, creditUSD: "", planId: "", durationDays: "30", maxRedemptions: "", perUserLimit: "1", expiresAt: "", description: "" };
}

const wholeNumber = (text: string): number | null => (/^\d+$/.test(text.trim()) ? Number(text.trim()) : null);

export const CUSTOM_CODE = /^[A-Za-z0-9_-]{3,64}$/;

/** Max redemptions: blank means unlimited. */
function checkLimits(form: Pick<CodeForm, "maxRedemptions" | "perUserLimit" | "expiresAt">, now: number): Problem | null {
  const max = form.maxRedemptions.trim() === "" ? null : wholeNumber(form.maxRedemptions);
  if (form.maxRedemptions.trim() !== "" && (max === null || max < 1)) return problem("adminBilling.codes.problems.maxRedemptions");
  const per = wholeNumber(form.perUserLimit);
  if (per === null || per < 1) return problem("adminBilling.codes.problems.perUser");
  if (max !== null && per > max) return problem("adminBilling.codes.problems.perUserOverMax");
  if (form.expiresAt.trim() !== "") {
    const at = Date.parse(form.expiresAt);
    if (!Number.isFinite(at)) return problem("adminBilling.codes.problems.expiryInvalid");
    if (at <= now) return problem("adminBilling.codes.problems.expiryPast");
  }
  return null;
}

export function validateCreate(form: CodeForm, now: number = Date.now()): Problem | null {
  const custom = form.code.trim();
  if (custom && !CUSTOM_CODE.test(custom)) return problem("adminBilling.codes.problems.customCode");
  const quantity = wholeNumber(form.quantity);
  if (quantity === null || quantity < 1) return problem("adminBilling.codes.problems.quantity");
  if (custom && quantity !== 1) return problem("adminBilling.codes.problems.customQuantity");
  const limits = checkLimits(form, now);
  if (limits) return limits;
  if (form.mode === "usage") {
    const credit = Number(form.creditUSD.trim());
    if (!Number.isFinite(credit) || credit <= 0) return problem("adminBilling.codes.problems.credit");
  } else {
    const plan = wholeNumber(form.planId);
    const days = wholeNumber(form.durationDays);
    if (plan === null || plan < 1) return problem("adminBilling.codes.problems.plan");
    if (days === null || days < 1) return problem("adminBilling.codes.problems.days");
  }
  return null;
}

export interface CreateBody {
  code?: string;
  quantity: number;
  mode: string;
  creditUSD?: number;
  planID?: number;
  durationDays?: number;
  maxRedemptions?: number;
  perUserLimit: number;
  expiresAt: string | null;
  description?: string;
}

export function createBody(form: CodeForm): CreateBody {
  const body: CreateBody = {
    quantity: Number(form.quantity.trim()),
    mode: form.mode,
    perUserLimit: Number(form.perUserLimit.trim()),
    expiresAt: form.expiresAt.trim() ? new Date(form.expiresAt).toISOString() : null,
  };
  if (form.code.trim()) body.code = form.code.trim();
  if (form.maxRedemptions.trim()) body.maxRedemptions = Number(form.maxRedemptions.trim());
  if (form.description.trim()) body.description = form.description.trim();
  if (form.mode === "usage") body.creditUSD = Number(form.creditUSD.trim());
  else {
    body.planID = Number(form.planId.trim());
    body.durationDays = Number(form.durationDays.trim());
  }
  return body;
}

export interface EditForm {
  status: string;
  maxRedemptions: string;
  perUserLimit: string;
  expiresAt: string;
  description: string;
}

export function editFormOf(code: RedemptionCode): EditForm {
  return {
    status: code.status,
    maxRedemptions: code.maxRedemptions === null ? "" : String(code.maxRedemptions),
    perUserLimit: String(code.perUserLimit),
    expiresAt: localInput(code.expiresAt),
    description: code.description,
  };
}

/** `datetime-local` text for an instant, in local time. */
export function localInput(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function validateEdit(form: EditForm, now: number = Date.now()): Problem | null {
  return checkLimits({ ...form }, now);
}

export interface EditBody {
  status?: string;
  maxRedemptions?: number | null;
  perUserLimit?: number;
  expiresAt?: string | null;
  description?: string;
}

/** Only fields that changed. An expiry that was kept as it was is not re-validated against the clock. */
export function editBody(code: RedemptionCode, form: EditForm): EditBody {
  const body: EditBody = {};
  if (form.status !== code.status) body.status = form.status;
  const max = form.maxRedemptions.trim() === "" ? null : Number(form.maxRedemptions.trim());
  if (max !== code.maxRedemptions) body.maxRedemptions = max;
  const per = Number(form.perUserLimit.trim());
  if (per !== code.perUserLimit) body.perUserLimit = per;
  if (form.expiresAt !== localInput(code.expiresAt)) body.expiresAt = form.expiresAt.trim() ? new Date(form.expiresAt).toISOString() : null;
  if (form.description.trim() !== code.description) body.description = form.description.trim();
  return body;
}

export function validateEditChange(code: RedemptionCode, form: EditForm, now: number = Date.now()): Problem | null {
  const keepsExpiry = form.expiresAt === localInput(code.expiresAt);
  return checkLimits({ ...form, expiresAt: keepsExpiry ? "" : form.expiresAt }, now);
}

export interface ExportRecord {
  id: number;
  code: string;
  codeHint: string;
  mode: string;
  rewardType: string;
  creditUSD: number;
  planID: number;
  durationDays: number;
  maxRedemptions: number | null;
  perUserLimit: number;
  redeemedCount: number;
  remainingRedemptions: number | null;
  status: string;
  expiresAt: string | null;
  description: string;
  createdAt: string;
  updatedAt: string;
}

export function exportRecord(code: RedemptionCode, plain: string): ExportRecord {
  return {
    id: code.id,
    code: plain,
    codeHint: code.codeHint,
    mode: code.mode,
    rewardType: code.rewardType,
    creditUSD: code.creditUSD,
    planID: code.planID,
    durationDays: code.durationDays,
    maxRedemptions: code.maxRedemptions,
    perUserLimit: code.perUserLimit,
    redeemedCount: code.redeemedCount,
    remainingRedemptions: code.remainingRedemptions,
    status: code.status,
    expiresAt: code.expiresAt,
    description: code.description,
    createdAt: code.createdAt,
    updatedAt: code.updatedAt,
  };
}
