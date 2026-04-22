package knowledgebase

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// statusByCode records the HTTP status each of this area's codes is answered
// with. Errors that come back from collaborators (the file subsystem, the
// embedding queue) carry the same codes, so the lookup is by code and not by
// Go identity.
var statusByCode = map[string]int{}

func def(code, message string, status int) *apperr.Error {
	statusByCode[code] = status
	return apperr.Define(code, message)
}

// Error codes are part of the client contract (the web client maps them to
// translated texts), so codes and messages must not change.
var (
	ErrDisabled             = def("knowledge_base.disabled", "knowledge base feature is disabled", http.StatusForbidden)
	ErrInvalid              = def("knowledge_base.invalid", "invalid knowledge base request", http.StatusBadRequest)
	ErrNotFound             = def("knowledge_base.not_found", "knowledge base not found", http.StatusNotFound)
	ErrConflict             = def("knowledge_base.conflict", "knowledge base conflict", http.StatusConflict)
	ErrPlatformFileInUse    = def("knowledge_base.platform_file_in_use", "platform file is in use", http.StatusConflict)
	ErrCleanupUnavailable   = def("knowledge_base.file_cleanup_unavailable", "platform file cleanup unavailable", http.StatusServiceUnavailable)
	ErrInternal             = def("knowledge_base.internal", "knowledge base operation failed", http.StatusInternalServerError)
	ErrOwnerFileReference   = def("knowledge_base.owner_file_reference", "user owns files referenced by builtin knowledge bases", http.StatusConflict)
	ErrReferenceUnavailable = def("knowledge_base.invalid_reference", "invalid knowledge base reference", http.StatusBadRequest)
	ErrNotReady             = def("knowledge_base.not_ready", "knowledge base is not ready", http.StatusConflict)
	ErrRetrievalUnavailable = def("knowledge_base.unavailable", "knowledge base retrieval is unavailable", http.StatusServiceUnavailable)

	// Errors owned by neighbouring subsystems that this area passes through.
	ErrFileNotFound                = def("file.not_found", "file not found", http.StatusNotFound)
	ErrFileRequired                = def("file.required", "file is required", http.StatusBadRequest)
	ErrInvalidStream               = def("file.invalid_stream", "invalid file stream", http.StatusBadRequest)
	ErrInvalidFile                 = def("request.invalid_file", "invalid file", http.StatusBadRequest)
	ErrEmbeddingUnavailableForFile = def("file.embedding_unavailable", "embedding is unavailable for this file", http.StatusBadRequest)
	ErrQuotaExceeded               = def("quota.exceeded", "quota exceeded", http.StatusConflict)
	ErrFileTooLarge                = def("file.too_large", "file too large", http.StatusRequestEntityTooLarge)

	ErrTooManyEmbeddingFiles  = def("embedding.too_many_files", "too many files for embedding", http.StatusBadRequest)
	ErrEmbeddingNotConfigured = def("embedding.service_not_configured", "embedding service is not configured", http.StatusServiceUnavailable)
	ErrEmbeddingUnavailable   = def("embedding.service_unavailable", "embedding service is not available", http.StatusServiceUnavailable)
)

// statusFor picks the response status for an error: the status of its typed
// code when this area knows it, 500 otherwise.
func statusFor(err error) int {
	if te, ok := apperr.Find(err); ok {
		if st, known := statusByCode[te.Code]; known {
			return st
		}
	}
	return http.StatusInternalServerError
}
