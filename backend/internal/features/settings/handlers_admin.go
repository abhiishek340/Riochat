package settings

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

type patchRequest struct {
	Items []patchRequestItem `json:"items" binding:"required,min=1,dive"`
}

type patchRequestItem struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key" binding:"required"`
	Value     string `json:"value"`
	Clear     bool   `json:"clear"`
}

func (h *handlers) listAll(c *gin.Context) {
	out, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) listNamespace(c *gin.Context) {
	items, err := h.svc.ListNamespace(c.Request.Context(), c.Param("namespace"))
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, items)
}

func (h *handlers) patch(c *gin.Context) {
	var req patchRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	items := make([]PatchItem, len(req.Items))
	for i, it := range req.Items {
		items[i] = PatchItem{Namespace: it.Namespace, Key: it.Key, Value: it.Value, Clear: it.Clear}
	}
	out, err := h.svc.Patch(c.Request.Context(), actorOf(c), items)
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, out)
}

// ---- embedding index ------------------------------------------------------------------------

const (
	statusTimeout  = 10 * time.Second
	reindexTimeout = 15 * time.Second
	maxRetryFiles  = 100
)

var taskBuckets = map[string]bool{"ready": true, "pending": true, "failed": true, "stale": true, "empty": true, "unsupported": true}

func unavailable() error {
	return ErrEmbeddingServiceUnavailable.WithStatus(http.StatusServiceUnavailable)
}

// embeddingFailure maps a port error to the response contract. notConfigured is
// the status used when embedding is not set up (400 for reindex, 503 elsewhere).
func embeddingFailure(c *gin.Context, err error, notConfiguredStatus int) {
	switch {
	case errors.Is(err, ErrPortUnavailable):
		httpx.Fail(c, http.StatusServiceUnavailable, ErrEmbeddingServiceUnavailable.WithCause(err))
	case errors.Is(err, ErrNothingConfigured):
		httpx.Fail(c, notConfiguredStatus, ErrEmbeddingNotConfigured.WithCause(err))
	default:
		httpx.Fail(c, http.StatusInternalServerError, err)
	}
}

func (h *handlers) embeddingStatus(c *gin.Context) {
	idx := h.svc.port().Embedding
	if idx == nil {
		httpx.Fail(c, http.StatusServiceUnavailable, unavailable())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), statusTimeout)
	defer cancel()
	st, err := idx.Status(ctx)
	if err != nil {
		embeddingFailure(c, err, http.StatusServiceUnavailable)
		return
	}
	httpx.OK(c, st)
}

func (h *handlers) embeddingReindex(c *gin.Context) {
	includeEmpty := false
	if raw := strings.TrimSpace(c.Query("include_empty")); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			httpx.InvalidQuery(c, "include_empty")
			return
		}
		includeEmpty = b
	}
	idx := h.svc.port().Embedding
	if idx == nil {
		httpx.Fail(c, http.StatusServiceUnavailable, unavailable())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), reindexTimeout)
	defer cancel()
	out, err := idx.Reindex(ctx, includeEmpty)
	if err != nil {
		if !errors.Is(err, ErrReindexRunning) {
			embeddingFailure(c, err, http.StatusBadRequest)
			return
		}
		// A rebuild is already running. That is only a failure when no file
		// could be sent back through extraction either.
		if out.Reprocessed == 0 {
			httpx.Fail(c, http.StatusConflict, apperr.ErrConflict.WithCause(err))
			return
		}
	}
	submitted := out.Submitted + out.Reprocessed
	h.svc.record(c.Request.Context(), actorOf(c), "settings.embedding_reindex", map[string]any{
		"include_empty": includeEmpty, "submitted": submitted, "reprocessed": out.Reprocessed,
	})
	httpx.OK(c, gin.H{"submitted": submitted, "message": "reindex jobs submitted"})
}
