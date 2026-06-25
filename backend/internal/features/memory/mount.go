package memory

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

// Audit actions and resource written for memory changes.
const (
	auditResource = "memory"
	auditUpsert   = "upsert_user_memory"
	auditDelete   = "delete_user_memory"
)

// Auditor records one audited action of the authenticated caller. The audit
// area's *audit.Recorder satisfies it.
type Auditor interface {
	RecordRequest(c *gin.Context, action, resource, resourceID string, detail any)
}

// Mount registers the routes with no embedder; the audit trail goes to the
// shared audit writer. The integrator calls MountWith to supply an embedder and
// to get hold of the Service.
func Mount(d deps.Deps) error {
	_, err := MountWith(d, Options{})
	return err
}

// MountWith registers the routes using opts and returns the Service for other
// areas (chat for recall, account deletion for PurgeUser). Fields of opts left
// empty are filled from d: the detacher from the lifecycle manager and the
// logger.
func MountWith(d deps.Deps, opts Options) (*Service, error) {
	if opts.Detach == nil && d.Lifecycle != nil {
		opts.Detach = d.Lifecycle.Detach
	}
	if opts.Logger == nil {
		opts.Logger = d.Logger
	}
	svc := NewService(d.DB, d.Clock, opts)
	var auditor Auditor = audit.NewRecorder(d.DB, d.Logger, d.Clock)

	h := &handlers{svc: svc, audit: auditor}
	d.Routes.Auth.GET("/memories/profile", h.list)
	d.Routes.Auth.PUT("/memories/profile", h.upsert)
	d.Routes.Auth.DELETE("/memories/profile/:memory_key", h.remove)

	if d.Lifecycle != nil {
		d.Lifecycle.Go("memory-cache-sweep", func(ctx context.Context) {
			sweepLoop(ctx, svc)
		})
	}
	return svc, nil
}

type handlers struct {
	svc   *Service
	audit Auditor
}

type upsertRequest struct {
	MemoryKey string `json:"memoryKey" binding:"required,max=128"`
	Value     string `json:"value" binding:"required,max=10000"`
	Scope     string `json:"scope" binding:"required,oneof=profile preference custom"`
}

func (h *handlers) list(c *gin.Context) {
	p := middleware.MustPrincipal(c)
	entries, err := h.svc.List(c.Request.Context(), p.UserID)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, err)
		return
	}
	httpx.OK(c, entries)
}

func (h *handlers) upsert(c *gin.Context) {
	var req upsertRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	key, value := strings.TrimSpace(req.MemoryKey), strings.TrimSpace(req.Value)
	// A key made only of whitespace is rejected rather than stored as an empty
	// key. A value made only of whitespace is saved as an empty string.
	if key == "" {
		httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.
			WithMessage("invalid request body: memoryKey is required").
			WithDetails(map[string]any{"reason": "validation"}))
		return
	}
	p := middleware.MustPrincipal(c)
	ctx := c.Request.Context()
	_, err := h.svc.Upsert(ctx, p.UserID, key, value, strings.TrimSpace(req.Scope), UpdatedByUser)
	if err != nil {
		switch {
		case errors.Is(err, ErrLimitExceeded):
			httpx.Fail(c, http.StatusBadRequest, ErrLimitExceeded)
		case errors.Is(err, ErrInvalidInput):
			httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.WithCause(err))
		default:
			httpx.Fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	if h.audit != nil {
		// The audit key is the key exactly as the client sent it.
		h.audit.RecordRequest(c, auditUpsert, auditResource, req.MemoryKey, map[string]string{"scope": strings.TrimSpace(req.Scope)})
	}
	httpx.OK(c, gin.H{"saved": true})
}

func (h *handlers) remove(c *gin.Context) {
	key := strings.TrimSpace(c.Param("memory_key"))
	if key == "" {
		httpx.Fail(c, http.StatusBadRequest, ErrKeyRequired)
		return
	}
	p := middleware.MustPrincipal(c)
	if err := h.svc.Delete(c.Request.Context(), p.UserID, key); err != nil {
		httpx.Fail(c, http.StatusInternalServerError, err)
		return
	}
	if h.audit != nil {
		h.audit.RecordRequest(c, auditDelete, auditResource, key, nil)
	}
	httpx.OK(c, gin.H{"saved": true})
}
