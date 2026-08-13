"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { retryNetwork, runChunks, runPool } from "../shared/async";
import { markQueued } from "../shared/file-status";
import { hasSession } from "../shared/session";
import { planVectorize, summarizeEmbedding, MAX_VECTORIZE } from "../shared/vectorize";
import { summarizeUploads } from "../shared/upload-outcome";
import type { FileObject, StorageQuota } from "../shared/types";
import { deleteFile, patchFile, submitEmbeddings, uploadFile } from "./api";
import { mergeUploads, pruneChecked, selectionAfterDelete } from "./list-model";
import type { FileLibrary } from "./use-file-library";

const UPLOAD_CONCURRENCY = 4;
const DELETE_CHUNK = 10;
