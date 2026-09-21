/** Public surface of the files / knowledge-bases / library area. */
// Registers the localized text for this area's backend error codes.
import "./shared/api-errors";
export { FilesPage } from "./files/files-page";
export { KnowledgeBasesPage } from "./knowledge-bases/pages";
/** The knowledge-base workspace; `mode="admin"` is the administrator console's variant. */
export { KnowledgeBaseWorkspace } from "./knowledge-bases/kb-workspace";
export { LibraryPage } from "./library/library-page";
/** Other areas call this after deleting a conversation or project that owned files. */
export { announceFilesChanged, subscribeFilesChanged, type FilesChange, type FilesChangeReason } from "./shared/invalidation";
