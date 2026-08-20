// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui";
import { dismiss, getToasts } from "@/components/ui/toast-store";
import type { FileObject } from "../shared/types";

const searchParams = new URLSearchParams();

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());
vi.mock("next/navigation", () => ({ useSearchParams: () => searchParams }));
vi.mock("@/hooks/use-media-query", () => ({ useIsMobile: () => false, useMediaQuery: () => false }));
vi.mock("../shared/session", () => ({ hasSession: async () => true }));
vi.mock("./api", () => ({
  listFiles: vi.fn(),
  locateFile: vi.fn(),
  fetchContent: vi.fn(),
  fetchExtract: vi.fn(),
  fetchStatuses: vi.fn(async () => []),
  patchFile: vi.fn(),
  deleteFile: vi.fn(),
  uploadFile: vi.fn(),
  submitEmbeddings: vi.fn(),
}));

import * as api from "./api";
import { FilesPage } from "./files-page";

const mocked = vi.mocked(api);

function makeFile(n: number, over: Partial<FileObject> = {}): FileObject {
  return {
    fileID: `f${n}`,
    fileName: `file-${n}.pdf`,
    mimeType: "application/pdf",
    detectedMIME: "application/pdf",
    fileCategory: "pdf",
    sizeBytes: 1000,
    sha256: "",
    purpose: "",
    status: "active",
    processingStatus: "ready",
    processingReady: true,
    processingErrorCode: "",
    processingErrorMessage: "",
    extractStatus: "ready",
    embedStatus: "none",
    embedError: "",
    chunkCount: 0,
    canVectorize: false,
    vectorizationReason: "",
    ragOptOut: false,
    createdAt: "2025-01-01T00:00:00Z",
    updatedAt: "2025-01-01T00:00:00Z",
    expiresAt: null,
    lastAccessedAt: null,
    ...over,
  };
}

const QUOTA = { quotaBytes: 0, usedBytes: 0 };
