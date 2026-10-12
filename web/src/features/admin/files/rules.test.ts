import { describe, expect, it } from "vitest";
import { bytesToMb, mbToBytes } from "../lib/settings-form";
import {
  appendUnique,
  changesVectorSpace,
  companionKeys,
  embeddingReady,
  F,
  keepPolling,
  mergeMimes,
  missingMineruMimes,
  normalizeRetrieval,
  retrievalChangeProblem,
  shouldPoll,
  validateEmbedding,
  validateExtraction,
} from "./rules";

describe("retrieval switches", () => {
  const ready = { [F.embeddingOn]: "true", [F.embeddingHost]: "https://e.test", [F.embeddingModel]: "m" };
  it("forces rag, message embedding and semantic context off without a ready service", () => {
    const out = normalizeRetrieval({ [F.embeddingOn]: "false", [F.rag]: "true", [F.messageEmbedding]: "true", [F.semantic]: "true" });
    expect([out[F.rag], out[F.messageEmbedding], out[F.semantic]]).toEqual(["false", "false", "false"]);
  });
  it("also turns them off when the model is cleared", () => {
    expect(embeddingReady({ ...ready, [F.embeddingModel]: "" })).toBe(false);
    expect(normalizeRetrieval({ ...ready, [F.embeddingModel]: "", [F.rag]: "true" })[F.rag]).toBe("false");
  });
  it("keeps them when ready, but semantic context needs message embedding", () => {
    const out = normalizeRetrieval({ ...ready, [F.rag]: "true", [F.messageEmbedding]: "false", [F.semantic]: "true" });
    expect(out[F.rag]).toBe("true");
    expect(out[F.semantic]).toBe("false");
  });
  it("refuses switching one on while the service is not ready", () => {
    expect(retrievalChangeProblem({ [F.embeddingOn]: "false" }, F.rag, "true")?.key).toBe("adminFiles.problems.needsEmbedding");
    expect(retrievalChangeProblem(ready, F.rag, "true")).toBeNull();
    expect(retrievalChangeProblem({ [F.embeddingOn]: "false" }, F.rag, "false")).toBeNull();
    expect(retrievalChangeProblem({ [F.embeddingOn]: "false" }, F.fullLimit, "true")).toBeNull();
  });
  it("flags model and dimension changes as stale-making", () => {
    expect(changesVectorSpace([F.embeddingModel])).toBe(true);
    expect(changesVectorSpace([F.embeddingDims])).toBe(true);
    expect(changesVectorSpace([F.embeddingHost])).toBe(false);
  });
});

describe("validation", () => {
  it("needs the base url of the selected engine", () => {
    expect(validateExtraction({ [F.engine]: "tika", [F.tikaUrl]: "" }, new Set())?.key).toBe("adminFiles.problems.engineUrl");
    expect(validateExtraction({ [F.engine]: "tika", [F.tikaUrl]: "http://t" }, new Set())).toBeNull();
    expect(validateExtraction({ [F.engine]: "builtin" }, new Set())).toBeNull();
  });
  it("needs the OCR engine's required fields once OCR is on", () => {
    const draft = { [F.engine]: "builtin", [F.imageOcr]: "true", [F.ocrEngine]: "tencent", "extract.tencent_ocr_secret_id": "", "extract.tencent_ocr_region": "r" };
    expect(validateExtraction(draft, new Set())?.args).toMatchObject({ engine: "tencent", field: "adminFiles.fieldNames.tencent_ocr_secret_id" });
    const ok = { ...draft, "extract.tencent_ocr_secret_id": "id" };
    // the stored secret key satisfies the secret requirement
    expect(validateExtraction(ok, new Set(["extract.tencent_ocr_secret_key"]))).toBeNull();
    expect(validateExtraction(ok, new Set())?.args).toMatchObject({ field: "adminFiles.fieldNames.tencent_ocr_secret_key" });
  });
  it("ignores OCR requirements while OCR is off", () => {
    expect(validateExtraction({ [F.engine]: "builtin", [F.ocrEngine]: "tencent" }, new Set())).toBeNull();
  });
  it("needs host and model when embedding is on", () => {
    expect(validateEmbedding({ [F.embeddingOn]: "true", [F.embeddingHost]: "", [F.embeddingModel]: "m" })?.key).toBe("adminFiles.problems.embeddingHost");
    expect(validateEmbedding({ [F.embeddingOn]: "true", [F.embeddingHost]: "h", [F.embeddingModel]: "" })?.key).toBe("adminFiles.problems.embeddingModel");
    expect(validateEmbedding({ [F.embeddingOn]: "false" })).toBeNull();
  });
  it("always sends the companion values of the selected engines", () => {
    const keys = companionKeys({ [F.engine]: "tika", [F.imageOcr]: "true", [F.ocrEngine]: "mistral" });
    expect(keys).toContain("extract.tika_base_url");
    expect(keys).toContain("extract.mistral_ocr_model");
    expect(keys).toContain("extract.tika_source");
    expect(keys).not.toContain("extract.docling_base_url");
  });
});

describe("megabyte fields", () => {
  it("shows 2097152 bytes as 2 and saves 1.5 as 1572864", () => {
    expect(bytesToMb("2097152")).toBe("2");
    expect(mbToBytes("1.5")).toBe("1572864");
    expect(mbToBytes("")).toBe("");
    expect(mbToBytes("x")).toBeUndefined();
  });
});

describe("MinerU allowlist", () => {
  it("lists what a non-blank allowlist lacks", () => {
    const missing = missingMineruMimes("application/pdf", "self_hosted", "pdf,word");
    expect(missing).toEqual(["application/vnd.openxmlformats-officedocument.wordprocessingml.document"]);
  });
  it("needs the legacy formats only for the cloud service", () => {
    expect(missingMineruMimes("application/pdf", "cloud", "word")).toHaveLength(2);
    expect(missingMineruMimes("application/pdf", "self_hosted", "word")).toHaveLength(1);
  });
  it("says nothing for a blank allowlist", () => {
    expect(missingMineruMimes("  ", "cloud", "pdf")).toEqual([]);
  });
  it("matches without regard to case and merges without duplicates", () => {
    expect(missingMineruMimes("Application/PDF", "cloud", "pdf")).toEqual([]);
    expect(mergeMimes("application/pdf, text/plain", ["APPLICATION/PDF", "application/msword"])).toBe("application/pdf,text/plain,application/msword");
  });
});

describe("embedding status helpers", () => {
  it("polls while a rebuild runs or files are active", () => {
    expect(shouldPoll(null)).toBe(false);
    expect(shouldPoll({ reindexRunning: false, activeCount: 0 })).toBe(false);
    expect(shouldPoll({ reindexRunning: true, activeCount: 0 })).toBe(true);
    expect(shouldPoll({ reindexRunning: false, activeCount: 2 })).toBe(true);
  });
  it("keeps polling when a tick was replaced by a newer load while work is still running", () => {
    const busy = { reindexRunning: false, activeCount: 3 };
    const idle = { reindexRunning: false, activeCount: 0 };
    expect(keepPolling(null, busy as never)).toBe(true);
    expect(keepPolling(null, idle as never)).toBe(false);
    expect(keepPolling(null, null)).toBe(false);
    expect(keepPolling(busy as never, idle)).toBe(true);
    expect(keepPolling(idle as never, busy)).toBe(false);
  });
  it("appends pages without duplicates and reports an empty gain", () => {
    const first = appendUnique([], [{ id: "a" }, { id: "b" }], (x) => x.id);
    expect(first.added).toBe(true);
    const second = appendUnique(first.items, [{ id: "b" }, { id: "c" }], (x) => x.id);
    expect(second.items.map((x) => x.id)).toEqual(["a", "b", "c"]);
    expect(appendUnique(second.items, [{ id: "a" }], (x) => x.id).added).toBe(false);
  });
});
