/** Wire types shared by the files, knowledge-base and library workspaces. */

export interface FileObject {
  fileID: string;
  fileName: string;
  mimeType: string;
  detectedMIME: string;
  fileCategory: string;
  sizeBytes: number;
  sha256: string;
  purpose: string;
  status: string;
  processingStatus: string;
  processingReady: boolean;
  processingErrorCode: string;
  processingErrorMessage: string;
  extractStatus: string;
  embedStatus: string;
  embedError: string;
  chunkCount: number;
  canVectorize: boolean;
  vectorizationReason: string;
  ragOptOut: boolean;
  createdAt: string;
  updatedAt: string;
  expiresAt: string | null;
  lastAccessedAt: string | null;
  /** Present on some payloads: the server's own "still working" flag. */
  processing?: boolean;
}

export interface StorageQuota {
  quotaBytes: number;
  usedBytes: number;
  reservedBytes?: number;
}

export interface FileListResponse {
  results: FileObject[];
  total: number;
  quota: StorageQuota | null;
}

export interface UploadResponse {
  file: FileObject;
  quota: StorageQuota | null;
  reused: boolean;
}

export interface DeleteFileResponse {
  deleted: boolean;
  fileID: string;
  quota: StorageQuota | null;
}

export interface SkippedFile {
  fileID: string;
  reason: string;
}

export interface EmbeddingResponse {
  submittedFileIDs: string[];
  skipped: SkippedFile[];
}

export interface FileExtract {
  fileID: string;
  extractText: string;
  previewText: string;
  extractChars: number;
  extractPages: number;
  ocrUsed: boolean;
}

/** One entry of a processing-status poll (files or knowledge-base snapshot). */
export interface ProcessingStatus {
  fileID: string;
  detectedMIME?: string;
  fileCategory?: string;
  processingStatus?: string;
  processing?: boolean;
  processingReady?: boolean;
  errorCode?: string;
  errorMessage?: string;
  extractStatus?: string;
  embedStatus?: string;
  embedError?: string;
  chunkCount?: number;
  ragOptOut?: boolean;
  canVectorize?: boolean;
  vectorizationReason?: string;
  updatedAt?: string;
}

/** The status-bearing slice of a file, common to library files and knowledge-base files. */
export interface StatusBearing {
  fileID: string;
  fileName: string;
  mimeType: string;
  detectedMIME: string;
  fileCategory: string;
  processingStatus: string;
  processingReady: boolean;
  extractStatus: string;
  embedStatus: string;
  embedError: string;
  chunkCount: number;
  canVectorize: boolean;
  vectorizationReason: string;
  ragOptOut: boolean;
  processing?: boolean;
  processingErrorCode?: string;
  processingErrorMessage?: string;
}

export type KnowledgeBaseScope = "builtin" | "user";

export interface KnowledgeBase {
  publicID: string;
  name: string;
  description: string;
  enabled: boolean;
  scope: KnowledgeBaseScope;
  fileCount: number;
  readyFileCount: number;
  processingFileCount: number;
  revision: number;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}

export interface KnowledgeBaseFile extends StatusBearing {
  sizeBytes: number;
  createdAt: string;
  updatedAt: string;
}

export type LibraryScope = "user" | "builtin";

export interface Skill {
  id: number;
  scope: LibraryScope;
  title: string;
  trigger: string;
  description: string;
  /** Absent on list summaries. */
  markdown?: string;
  enabled: boolean;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}

export interface PromptPreset {
  id: number;
  scope: LibraryScope;
  title: string;
  trigger: string;
  description: string;
  content: string;
  enabled: boolean;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}
