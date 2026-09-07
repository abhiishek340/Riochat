// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui";
import { dismiss, getToasts } from "@/components/ui/toast-store";
import type { KnowledgeBase, KnowledgeBaseFile } from "../shared/types";

const router = { replace: vi.fn(), push: vi.fn() };
const policy = { value: { policy: { knowledgeBaseEnabled: true }, loaded: true } };

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());
vi.mock("next/navigation", () => ({ useRouter: () => router }));
vi.mock("@/hooks/use-media-query", () => ({ useIsMobile: () => false, useMediaQuery: () => false }));
vi.mock("@/lib/session/feature-policy", () => ({ useFeaturePolicy: () => policy.value }));
vi.mock("../shared/session", () => ({ hasSession: async () => true }));
vi.mock("./api", async () => {
  const actual = (await vi.importActual("./api")) as Record<string, unknown>;
  return {
    ...actual,
    listKnowledgeBases: vi.fn(),
    getKnowledgeBase: vi.fn(),
    createKnowledgeBase: vi.fn(),
    updateKnowledgeBase: vi.fn(),
    deleteKnowledgeBase: vi.fn(),
    listKbFiles: vi.fn(),
    listAvailableFiles: vi.fn(),
    addFilesToKb: vi.fn(),
    removeFileFromKb: vi.fn(),
    fetchKbContent: vi.fn(),
    fetchKbSnapshot: vi.fn(),
    uploadPlatformFile: vi.fn(),
    deletePlatformFile: vi.fn(),
    submitKbEmbeddings: vi.fn(),
  };
});
vi.mock("../files/api", () => ({ uploadFile: vi.fn() }));

import * as api from "./api";
import { KnowledgeBaseWorkspace } from "./kb-workspace";

const m = vi.mocked(api);

function kb(id: string, over: Partial<KnowledgeBase> = {}): KnowledgeBase {
  return {
    publicID: id,
    name: `Base ${id}`,
    description: "",
    enabled: true,
    scope: "user",
    fileCount: 0,
    readyFileCount: 0,
    processingFileCount: 0,
    revision: 1,
    sortOrder: 0,
    createdAt: "",
    updatedAt: "",
    ...over,
  };
}

function kbFile(n: number, over: Partial<KnowledgeBaseFile> = {}): KnowledgeBaseFile {
  return {
    fileID: `file${n}`,
    fileName: `doc-${n}.pdf`,
    mimeType: "application/pdf",
    detectedMIME: "application/pdf",
    fileCategory: "pdf",
    sizeBytes: 2048,
    processingStatus: "ready",
    processingReady: true,
    extractStatus: "ready",
    embedStatus: "ready",
    embedError: "",
    chunkCount: 12,
    canVectorize: false,
    vectorizationReason: "",
    ragOptOut: false,
    createdAt: "",
    updatedAt: "",
    ...over,
  };
}
