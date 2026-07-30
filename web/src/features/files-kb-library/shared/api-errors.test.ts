import { describe, expect, it } from "vitest";
import { ApiError } from "@/lib/api/errors";
import { enUS } from "@/lib/i18n/catalog/en-US";
import { formatError } from "@/lib/i18n/errors";
import { flatten } from "@/lib/i18n/keys";
import { AREA_ERROR_KEYS, formatAreaError } from "./api-errors";
import { lookup } from "./testing/i18n-mock";
import "./api-errors";

const err = (errorCode: string, status = 400) => new ApiError({ message: "english from the server", status, errorCode });
const ctx = { locale: "en-US" as const, lookup };

describe("area error codes", () => {
  it("shows localized text instead of the server's English message", () => {
    expect(formatError(err("knowledge_base.not_found", 404), ctx)).toBe("That knowledge base no longer exists.");
    expect(formatError(err("skill_trigger.already_exists", 409), ctx)).toBe("A skill with that name already exists. Pick another name.");
    expect(formatError(err("prompt_preset_trigger.already_exists", 409), ctx)).toContain("prompt with that name");
    expect(formatError(err("embedding.too_many_files"), ctx)).toBe("You can index at most 100 files at a time.");
    expect(formatError(err("file.extract_not_ready", 409), ctx)).toBe("Text extraction is not finished yet.");
  });

  it("leaves codes it does not own to the shared rules", () => {
    expect(formatAreaError(err("file.too_large", 413), ctx)).toBeUndefined();
    expect(formatError(err("file.too_large", 413), ctx)).toBe("That file is too large.");
    expect(formatError(err("quota.exceeded", 409), ctx)).toBe("You have reached a usage quota.");
  });

  it("has a message for every code it maps", () => {
    const en = flatten(enUS);
    const keys = Object.values(AREA_ERROR_KEYS).map((name) => `filesWs.apiErrors.${name}`);
    expect(keys.length).toBeGreaterThan(20);
    for (const key of keys) {
      expect(en[key], key).toBeTruthy();
    }
  });
});
