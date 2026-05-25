package uicomponent

import (
	"bytes"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Audit vocabulary for administrator changes.
const (
	auditResource     = "ui_components"
	actionCreatePlatf = "ui_component.create_platform"
	actionUpdate      = "ui_component.update"
	actionDeletePlatf = "ui_component.delete_platform"
)

type handlers struct {
	svc   *Service
	audit *audit.Recorder
}

// createRequest is the body of the two create endpoints. Size limits are
// enforced again, on the trimmed values and in bytes, by the service.
type createRequest struct {
	Name           string `json:"name" binding:"required,max=64"`
	Version        int    `json:"version" binding:"omitempty,min=1,max=2147483647"`
	Description    string `json:"description" binding:"required,max=256"`
	PropsSummary   string `json:"propsSummary" binding:"required,max=1024"`
	PropsSchema    string `json:"propsSchema" binding:"max=16384"`
	RendererSource string `json:"rendererSource" binding:"required,max=262144"`
	Enabled        bool   `json:"enabled"`
	SortOrder      int    `json:"sortOrder" binding:"min=-2147483648,max=2147483647"`
}

func (r createRequest) raw() rawInput {
	return rawInput{
		Name: r.Name, Version: r.Version, Description: r.Description,
		PropsSummary: r.PropsSummary, PropsSchema: r.PropsSchema,
		RendererSource: r.RendererSource, Enabled: r.Enabled, SortOrder: r.SortOrder,
	}
}

// patchRequest distinguishes absent fields (nil) from zero values.
type patchRequest struct {
	Name           *string `json:"name" binding:"omitempty,max=64"`
	Version        *int    `json:"version" binding:"omitempty,max=2147483647"`
	Description    *string `json:"description" binding:"omitempty,max=256"`
	PropsSummary   *string `json:"propsSummary" binding:"omitempty,max=1024"`
	PropsSchema    *string `json:"propsSchema" binding:"omitempty,max=16384"`
	RendererSource *string `json:"rendererSource" binding:"omitempty,max=262144"`
	Enabled        *bool   `json:"enabled"`
	SortOrder      *int    `json:"sortOrder" binding:"omitempty,min=-2147483648,max=2147483647"`
}

func (r patchRequest) raw() rawPatch {
	return rawPatch{
		Name: r.Name, Version: r.Version, Description: r.Description,
		PropsSummary: r.PropsSummary, PropsSchema: r.PropsSchema,
		RendererSource: r.RendererSource, Enabled: r.Enabled, SortOrder: r.SortOrder,
	}
}

// fail maps a service error to a response. Anything not recognised is a 500
// whose cause is only logged.
func fail(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrBuiltinProtected), errors.Is(err, ErrInvalidID):
		status = http.StatusBadRequest
	case errors.Is(err, ErrAlreadyExists):
		status = http.StatusConflict
	}
	if status == http.StatusInternalServerError {
		if _, typed := apperr.Find(err); !typed {
			err = apperr.ErrInternal.WithCause(err)
		}
	}
	httpx.Fail(c, status, err)
}

// bindPatch reads a patch body. A zero-length body is the same as `{}`: every
// field absent, which only records the acting user.
func bindPatch(c *gin.Context, dst *patchRequest) bool {
	if c.Request.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, httpx.DefaultBodyLimit))
		if err == nil && len(bytes.TrimSpace(raw)) == 0 {
			raw = []byte("{}")
		}
		if err == nil {
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		} else {
			c.Request.Body = io.NopCloser(&failingReader{err: err})
		}
	}
	return httpx.BindJSON(c, dst)
}

// failingReader replays a read error so BindJSON reports it the usual way.
type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

func caller(c *gin.Context) uint { return middleware.MustPrincipal(c).UserID }

// pathID parses a positive identifier; on failure the response is written.
func pathID(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 || n > math.MaxInt64 {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidID)
		return 0, false
	}
	return uint(n), true
}

// lenientBool reads an optional boolean filter; empty or unparsable means none.
func lenientBool(raw string) *bool {
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &v
}
