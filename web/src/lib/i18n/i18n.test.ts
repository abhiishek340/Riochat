import { describe, expect, it } from "vitest";
import { ApiError } from "../api/errors";
import { enUS } from "./catalog/en-US";
import { detectLocale, normalizeLocale } from "./config";
import { codeToKey, formatError, type ErrorContext } from "./errors";
import { formatMessage } from "./format";
import { flatten } from "./keys";

function context(locale: "en-US"): ErrorContext {
  const flat = flatten(enUS);
  return { locale, lookup: (key, args) => (flat[key] === undefined ? undefined : formatMessage(flat[key] as string, args, locale)) };
}

describe("catalog", () => {
  it("contains only string messages", () => {
    for (const [key, value] of Object.entries(flatten(enUS))) expect(typeof value, key).toBe("string");
  });
  it("contains only Latin-script text", () => {
    const nonLatin = /[\u2e80-\u9fff\uac00-\ud7af\uf900-\ufaff]/;
    for (const [key, value] of Object.entries(flatten(enUS))) expect(nonLatin.test(value), key).toBe(false);
  });
});

describe("formatMessage", () => {
  it("substitutes, pluralises and selects", () => {
    expect(formatMessage("Hi {name}", { name: "Ada" }, "en-US")).toBe("Hi Ada");
    const plural = "{n, plural, =0 {none} one {# item} other {# items}}";
    expect(formatMessage(plural, { n: 0 }, "en-US")).toBe("none");
    expect(formatMessage(plural, { n: 1 }, "en-US")).toBe("1 item");
    expect(formatMessage(plural, { n: 1234 }, "en-US")).toBe("1,234 items");
    expect(formatMessage("{k, select, a {A} other {?}}", { k: "a" }, "en-US")).toBe("A");
    expect(formatMessage("{k, select, a {A} other {?}}", { k: "z" }, "en-US")).toBe("?");
  });
});

describe("locale", () => {
  it("resolves every input to the only supported locale", () => {
    for (const v of ["en-GB", "en", "en-US", "fr", "", null, undefined]) expect(normalizeLocale(v)).toBe("en-US");
  });
  it("falls back to the default outside a browser", () => {
    expect(detectLocale()).toBe("en-US");
  });
});

describe("formatError", () => {
  it("maps codes to catalog keys", () => {
    expect(codeToKey("auth.account_locked")).toBe("errors.auth.accountLocked");
    const err = new ApiError({ message: "locked", status: 429, errorCode: "auth.account_locked" });
    expect(formatError(err, context("en-US"))).toBe(enUS.errors.auth.accountLocked);
  });
  it("appends a reference only to 5xx errors with a request id", () => {
    const e500 = new ApiError({ message: "boom", status: 502, requestId: "r9" });
    expect(formatError(e500, context("en-US"))).toContain("r9");
    const e400 = new ApiError({ message: "bad", status: 400, requestId: "r9" });
    expect(formatError(e400, context("en-US"))).toBe("bad");
  });
  it("builds sentences from invalid-body field errors", () => {
    const err = new ApiError({
      message: "x",
      status: 400,
      errorCode: "request.invalid_body",
      details: { fieldErrors: [{ field: "username", rule: "min", param: "3" }] },
    });
    expect(formatError(err, context("en-US"))).toBe("Username must be at least 3 characters.");
  });
  it("names the feature for feature.disabled", () => {
    const err = new ApiError({ message: "x", status: 403, errorCode: "feature.disabled", details: { feature: "sharing" } });
    expect(formatError(err, context("en-US"))).toBe(enUS.errors.feature.byFeature.sharing);
  });
  it("translates message keys and falls back for non-errors", () => {
    expect(formatError(new Error("errors.network.unavailable"), context("en-US"))).toBe(enUS.errors.network.unavailable);
    expect(formatError(new Error("boom"), context("en-US"))).toBe("boom");
    expect(formatError("weird", context("en-US"))).toBe(enUS.errors.generic);
    expect(formatError(null, context("en-US"), "custom")).toBe("custom");
  });
});
