import { describe, expect, it } from "vitest";
import { enUS } from "@/lib/i18n/catalog/en-US";
import { flatten } from "@/lib/i18n/keys";
import { fieldId } from "../lib/settings-form";
import { ALL_FILE_FIELDS, EXTRACTION_FIELDS } from "./fields";

describe("file settings definitions", () => {
  const en = flatten(enUS);
  it("only use message keys that exist in the catalog", () => {
    for (const field of ALL_FILE_FIELDS) {
      for (const key of [field.label, field.hint, ...(field.options ?? []).map((o) => o.labelKey)]) {
        if (!key) continue;
        expect(en[key], key).toBeDefined();
      }
    }
  });
  it("have unique ids", () => {
    const ids = ALL_FILE_FIELDS.map(fieldId);
    expect(new Set(ids).size).toBe(ids.length);
  });
  it("show only the selected extraction engine", () => {
    const tikaUrl = EXTRACTION_FIELDS.find((f) => f.key === "tika_base_url");
    expect(tikaUrl?.visible?.({ "extract.engine": "tika" })).toBe(true);
    expect(tikaUrl?.visible?.({ "extract.engine": "docling" })).toBe(false);
  });
  it("show OCR engine fields only while OCR is on", () => {
    const tencent = EXTRACTION_FIELDS.find((f) => f.key === "tencent_ocr_secret_id");
    expect(tencent?.visible?.({ "extract.ocr_engine": "tencent" })).toBe(false);
    expect(tencent?.visible?.({ "extract.image_ocr_enabled": "true", "extract.ocr_engine": "tencent" })).toBe(true);
    expect(tencent?.visible?.({ "extract.pdf_ocr_fallback_enabled": "true", "extract.ocr_engine": "tesseract" })).toBe(false);
  });
});
