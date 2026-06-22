import { describe, expect, it } from "vitest";
import {
  buildDraft,
  buildPatch,
  bytesToMb,
  dedupeCaseInsensitive,
  dirtyFields,
  hasValue,
  mbToBytes,
  rebase,
  type FieldDef,
  type SettingsTree,
} from "./settings-form";

const fields: FieldDef[] = [
  { ns: "storage", key: "max_upload_file_bytes", type: "int", label: "adminCommon.settings.configured", unit: "mb" },
  { ns: "file", key: "embedding_enabled", type: "bool", label: "adminCommon.settings.configured", def: "false" },
  { ns: "file", key: "embedding_host", type: "string", label: "adminCommon.settings.configured", visible: (d) => d["file.embedding_enabled"] === "true" },
  { ns: "file", key: "embedding_key", type: "password", label: "adminCommon.settings.configured", secret: true },
];

const tree: SettingsTree = {
  storage: [{ key: "max_upload_file_bytes", value: "2097152" }],
  file: [
    { key: "embedding_enabled", value: "false" },
    { key: "embedding_host", value: "" },
    { key: "embedding_key", value: "", configured: true },
  ],
};

describe("megabyte conversion", () => {
  it("shows whole megabytes plainly and rounds back to bytes", () => {
    expect(bytesToMb("2097152")).toBe("2");
    expect(bytesToMb("1572864")).toBe("1.5");
    expect(bytesToMb("")).toBe("");
    expect(mbToBytes("1.5")).toBe("1572864");
    expect(mbToBytes("")).toBe("");
    expect(mbToBytes("abc")).toBeUndefined();
    expect(mbToBytes("-1")).toBeUndefined();
  });
});

describe("drafts", () => {
  it("builds display values and notes configured secrets", () => {
    const built = buildDraft(fields, tree);
    expect(built.saved["storage.max_upload_file_bytes"]).toBe("2");
    expect(built.saved["file.embedding_key"]).toBe("");
    expect(built.configured.has("file.embedding_key")).toBe(true);
    expect(hasValue(built.saved, built.configured, "file.embedding_key")).toBe(true);
  });

  it("applies defaults for absent values only", () => {
    const built = buildDraft(fields, { storage: [], file: [] });
    expect(built.saved["file.embedding_enabled"]).toBe("false");
    expect(built.saved["storage.max_upload_file_bytes"]).toBe("");
  });

  it("ignores hidden fields when finding dirty ones", () => {
    const { saved } = buildDraft(fields, tree);
    const draft = { ...saved, "file.embedding_host": "http://x" };
    expect(dirtyFields(fields, draft, saved)).toHaveLength(0);
    const shown = { ...draft, "file.embedding_enabled": "true" };
    expect(dirtyFields(fields, shown, saved).map((f) => f.key)).toEqual(["embedding_enabled", "embedding_host"]);
  });

  it("treats a typed secret as dirty and converts units in the patch", () => {
    const { saved } = buildDraft(fields, tree);
    const draft = { ...saved, "file.embedding_key": " k ", "storage.max_upload_file_bytes": "1.5" };
    const dirty = dirtyFields(fields, draft, saved);
    const { items, invalid } = buildPatch(dirty, draft);
    expect(invalid).toHaveLength(0);
    expect(items).toEqual([
      { namespace: "storage", key: "max_upload_file_bytes", value: "1572864" },
      { namespace: "file", key: "embedding_key", value: "k" },
    ]);
  });

  it("rebase keeps edits outside the saved group", () => {
    const prevSaved = { a: "1", b: "1" };
    const prevDraft = { a: "2", b: "9" };
    const next = { a: "2", b: "1" };
    expect(rebase(prevSaved, prevDraft, next, new Set(["a"]))).toEqual({ a: "2", b: "9" });
  });
});

describe("lists", () => {
  it("de-duplicates case-insensitively keeping order", () => {
    expect(dedupeCaseInsensitive(["PDF", "pdf", "doc"])).toEqual(["PDF", "doc"]);
  });
});
