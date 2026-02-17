/**
 * Turns any thrown value into text for the active language.
 *
 * Order of rules:
 *  1. API errors with an errorCode map to catalog keys derived from the code
 *     (`auth.account_locked` -> `errors.auth.accountLocked`), with dedicated
 *     handling for `feature.disabled` and `request.invalid_body` field errors;
 *     feature areas can add code families through `registerErrorFormatter`.
 *  2. Errors whose message already is a catalog key (`errors.*`) are translated.
 *  3. Other Error messages are shown as is.
 *  4. Otherwise the caller's fallback, else the generic message.
 * Server-side failures (5xx) with a request id get a reference suffix so a user
 * can quote it to support.
 */
import { ApiError, isMessageKey } from "../api/errors";
import { disabledFeatureOf } from "../capabilities/features";
import { isRecord } from "../utils/guards";
import type { Locale } from "./config";
import type { FormatArgs } from "./format";

export interface ErrorContext {
  locale: Locale;
  lookup: (key: string, args?: FormatArgs) => string | undefined;
}

export type ErrorFormatter = (error: ApiError, ctx: ErrorContext) => string | undefined;

const formatters = new Map<string, ErrorFormatter>();

/** Registers a formatter for error codes beginning with `prefix` (for example `settings.`). */
export function registerErrorFormatter(prefix: string, formatter: ErrorFormatter): () => void {
  formatters.set(prefix, formatter);
  return () => formatters.delete(prefix);
}

export function camelSegment(segment: string): string {
  return segment.replace(/_([a-z0-9])/g, (_m, c: string) => c.toUpperCase());
}

export function codeToKey(code: string): string {
  return `errors.${code.split(".").map(camelSegment).join(".")}`;
}

interface FieldError {
  field?: unknown;
  rule?: unknown;
  param?: unknown;
}

function invalidBodyText(error: ApiError, ctx: ErrorContext): string | undefined {
  const details = error.details;
  const list = isRecord(details) && Array.isArray(details.fieldErrors) ? (details.fieldErrors as FieldError[]) : [];
  const sentences: string[] = [];
  for (const item of list) {
    if (!isRecord(item) || typeof item.field !== "string") continue;
    const label = ctx.lookup(`errors.fields.${item.field}`) ?? item.field;
    const rule = typeof item.rule === "string" ? item.rule : "";
    const param = typeof item.param === "string" ? item.param : "";
    const key = rule === "required_without" ? "required" : rule;
    const text =
      ctx.lookup(`errors.fieldRules.${key}`, { field: label, param }) ??
      ctx.lookup("errors.fieldRules.invalid", { field: label, param });
    if (text) sentences.push(text);
  }
  if (!sentences.length) return undefined;
  return sentences.join(" ");
}

function withReference(text: string, error: ApiError, ctx: ErrorContext): string {
  if (error.status < 500 || !error.requestId || text.includes(error.requestId)) return text;
  const ref = ctx.lookup("common.errors.errorReference", { requestId: error.requestId });
  return ref ? `${text} ${ref}` : text;
}

function formatApiError(error: ApiError, ctx: ErrorContext): string | undefined {
  const code = error.errorCode;
  if (code) {
    if (code === "feature.disabled") {
      const feature = disabledFeatureOf(error);
      return (feature && ctx.lookup(`errors.feature.byFeature.${feature}`)) || ctx.lookup("errors.feature.disabled");
    }
    if (code === "request.invalid_body") {
      const text = invalidBodyText(error, ctx);
      if (text) return text;
    }
    for (const [prefix, formatter] of formatters) {
      if (code.startsWith(prefix)) {
        const text = formatter(error, ctx);
        if (text) return text;
      }
    }
    const byCode = ctx.lookup(codeToKey(code));
    if (byCode) return byCode;
  }
  if (isMessageKey(error.message)) return ctx.lookup(error.message);
  return error.message.trim() || undefined;
}

export function formatError(error: unknown, ctx: ErrorContext, fallback?: string): string {
  let text: string | undefined;
  if (error instanceof ApiError) {
    text = formatApiError(error, ctx);
    if (text) return withReference(text, error, ctx);
  } else if (error instanceof Error) {
    text = isMessageKey(error.message) ? ctx.lookup(error.message) : error.message.trim() || undefined;
    if (text) return text;
  }
  return fallback ?? ctx.lookup("errors.generic") ?? "Request failed.";
}
