// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeBaseFile } from "../types";

vi.mock("@/lib/i18n/provider", async () => (await import("../testing/i18n-mock")).providerModule());

import { ProcessingBadge, RetrievalBadge } from "./status-badge";

afterEach(cleanup);

const file = (over: Partial<KnowledgeBaseFile> = {}): KnowledgeBaseFile => ({
  fileID: "f",
  fileName: "a.pdf",
  mimeType: "application/pdf",
  detectedMIME: "",
  fileCategory: "pdf",
  sizeBytes: 1,
  processingStatus: "ready",
  processingReady: true,
  extractStatus: "ready",
  embedStatus: "none",
  embedError: "",
  chunkCount: 0,
  canVectorize: false,
  vectorizationReason: "",
  ragOptOut: false,
  createdAt: "",
  updatedAt: "",
  ...over,
});

describe("RetrievalBadge", () => {
  it("shows the label for each retrieval state (scenario 27)", () => {
    const { rerender } = render(<RetrievalBadge file={file({ embedStatus: "ready", chunkCount: 12 })} />);
    expect(screen.getByText("Searchable")).toBeTruthy();
    rerender(<RetrievalBadge file={file({ embedStatus: "ready", chunkCount: 12, ragOptOut: true })} />);
    expect(screen.getByText("Retrieval off")).toBeTruthy();
    rerender(<RetrievalBadge file={file({ embedStatus: "queued" })} />);
    expect(screen.getByText("Building index")).toBeTruthy();
    rerender(<RetrievalBadge file={file()} />);
    expect(screen.getByText("Index pending")).toBeTruthy();
  });
});

describe("ProcessingBadge", () => {
  it("explains a failure through the tooltip text", () => {
    render(<ProcessingBadge file={file({ processingStatus: "failed", processingErrorMessage: "Unreadable PDF" })} />);
    const badge = screen.getByText("Processing failed");
    expect(badge.getAttribute("title")).toBe("Unreadable PDF");
  });
  it("also exposes the failure reason as text, not only as a hover title", () => {
    render(<ProcessingBadge file={file({ processingStatus: "failed", processingErrorMessage: "Unreadable PDF" })} />);
    expect(screen.getByText(/Unreadable PDF/, { selector: ".sr-only" })).toBeTruthy();
  });
  it("falls back to the error code", () => {
    render(<ProcessingBadge file={file({ processingStatus: "failed", processingErrorCode: "file.bad" })} />);
    expect(screen.getByText("Processing failed").getAttribute("title")).toBe("Error code file.bad");
  });
});
