package skill

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// maxIDFilter is the largest number of id query values accepted by discovery.
const maxIDFilter = 128

// Audit action names for built-in skill administration.
const (
	auditResource = "skills"
	actionCreate  = "skill.create_builtin"
	actionUpdate  = "skill.update_builtin"
	actionDelete  = "skill.delete_builtin"
)

// auditSink records admin actions; a nil sink records nothing.
type auditSink interface {
	RecordRequest(c *gin.Context, action, resource, resourceID string, detail any)
}

type api struct {
	svc   *Service
	audit auditSink
}

type createRequest struct {
	Title       string `json:"title" binding:"required,max=64"`
	Trigger     string `json:"trigger" binding:"required,max=64"`
	Description string `json:"description" binding:"max=256"`
	Markdown    string `json:"markdown" binding:"required,max=10000"`
	Enabled     *bool  `json:"enabled"`
	SortOrder   int32  `json:"sortOrder"`
}

type patchRequest struct {
	Title       *string `json:"title" binding:"omitempty,max=64"`
	Trigger     *string `json:"trigger" binding:"omitempty,max=64"`
	Description *string `json:"description" binding:"omitempty,max=256"`
	Markdown    *string `json:"markdown" binding:"omitempty,max=10000"`
	Enabled     *bool   `json:"enabled"`
	SortOrder   *int32  `json:"sortOrder"`
}

// Mount registers the user and administrator routes.
func Mount(d deps.Deps) error {
	a := &api{svc: NewService(d.DB, d.Clock), audit: audit.NewRecorder(d.DB, d.Logger, d.Clock)}

	// "mine" is a static segment; gin matches it before the :id parameter.
	d.Routes.Auth.GET("/skills", a.discover)
	d.Routes.Auth.GET("/skills/mine", a.listMine)
	d.Routes.Auth.POST("/skills/mine", a.createMine)
	d.Routes.Auth.PATCH("/skills/mine/:id", a.patchMine)
	d.Routes.Auth.DELETE("/skills/mine/:id", a.deleteMine)
	d.Routes.Auth.GET("/skills/:id", a.detail)

	d.Routes.Admin.GET("/admin/skills", a.listBuiltin)
	d.Routes.Admin.POST("/admin/skills", a.createBuiltin)
	d.Routes.Admin.PATCH("/admin/skills/:id", a.patchBuiltin)
	d.Routes.Admin.DELETE("/admin/skills/:id", a.deleteBuiltin)
	return nil
}

// pathID parses the :id segment; zero, negative and non-numeric values fail.
func pathID(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || n <= 0 {
		httpx.FailErr(c, ErrInvalidID)
		return 0, false
	}
	return uint(n), true
}

// idFilter parses the repeatable id query. More than 128 values is a
// distinct error from a malformed value.
func idFilter(c *gin.Context) ([]uint, bool) {
	raw := c.Request.URL.Query()["id"]
	if len(raw) > maxIDFilter {
		httpx.FailErr(c, ErrTooManyIDs)
		return nil, false
	}
	ids := make([]uint, 0, len(raw))
	for _, v := range raw {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			httpx.FailErr(c, ErrInvalidID)
			return nil, false
		}
		ids = append(ids, uint(n))
	}
	return NormalizeIDs(ids), true
}

// enabledFilter reads the optional boolean query; anything that does not parse
// exactly means no filter.
func enabledFilter(c *gin.Context) *bool {
	v, err := strconv.ParseBool(c.Query("enabled"))
	if err != nil {
		return nil
	}
	return &v
}

// allowEmptyBody turns a missing or blank request body into "{}" so an empty
// patch is accepted like an empty JSON object.
func allowEmptyBody(c *gin.Context) {
	if c.Request.Body == nil || c.Request.Body == http.NoBody {
		c.Request.Body = io.NopCloser(strings.NewReader("{}"))
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, httpx.DefaultBodyLimit))
	if err != nil {
		// Let the binder report the read failure.
		c.Request.Body = io.NopCloser(&failingReader{err: err})
		return
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

func wrap(f Full) gin.H { return gin.H{"skill": f} }
