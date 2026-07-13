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

func (a *api) discover(c *gin.Context) {
	ids, ok := idFilter(c)
	if !ok {
		return
	}
	rows, total, err := a.svc.Discover(c.Request.Context(), middleware.MustPrincipal(c).UserID, ids,
		c.Query("q"), pagination.FromRequest(c.Request))
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.Paged(c, total, rows)
}

func (a *api) detail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	f, err := a.svc.Get(c.Request.Context(), middleware.MustPrincipal(c).UserID, id)
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.OK(c, wrap(f))
}

func (a *api) list(c *gin.Context, scope string, owner uint) {
	rows, total, err := a.svc.ListOwned(c.Request.Context(), scope, owner, c.Query("q"),
		enabledFilter(c), pagination.FromRequest(c.Request))
	if err != nil {
		httpx.FailErr(c, err)
		return
	}
	httpx.Paged(c, total, rows)
}

func (a *api) create(c *gin.Context, scope string, owner uint) (Full, bool) {
	var req createRequest
	if !httpx.BindJSON(c, &req) {
		return Full{}, false
	}
	f, err := a.svc.Create(c.Request.Context(), scope, owner, middleware.MustPrincipal(c).UserID, Input{
		Title: req.Title, Trigger: req.Trigger, Description: req.Description,
		Markdown: req.Markdown, SortOrder: int(req.SortOrder),
	})
	if err != nil {
		httpx.FailErr(c, err)
		return Full{}, false
	}
	return f, true
}

func (a *api) patch(c *gin.Context, scope string, owner uint) (Full, bool) {
	id, ok := pathID(c)
	if !ok {
		return Full{}, false
	}
	allowEmptyBody(c)
	var req patchRequest
	if !httpx.BindJSON(c, &req) {
		return Full{}, false
	}
	var order *int
	if req.SortOrder != nil {
		v := int(*req.SortOrder)
		order = &v
	}
	f, err := a.svc.Update(c.Request.Context(), scope, owner, middleware.MustPrincipal(c).UserID, id, Patch{
		Title: req.Title, Trigger: req.Trigger, Description: req.Description,
		Markdown: req.Markdown, Enabled: req.Enabled, SortOrder: order,
	})
	if err != nil {
		httpx.FailErr(c, err)
		return Full{}, false
	}
	return f, true
}

func (a *api) remove(c *gin.Context, scope string, owner uint) (uint, bool) {
	id, ok := pathID(c)
	if !ok {
		return 0, false
	}
	if err := a.svc.Delete(c.Request.Context(), scope, owner, id); err != nil {
		httpx.FailErr(c, err)
		return 0, false
	}
	return id, true
}

func (a *api) record(c *gin.Context, action string, id uint, detail any) {
	if a.audit == nil {
		return
	}
	a.audit.RecordRequest(c, action, auditResource, strconv.FormatUint(uint64(id), 10), detail)
}

func (a *api) listMine(c *gin.Context) { a.list(c, ScopeUser, middleware.MustPrincipal(c).UserID) }

func (a *api) createMine(c *gin.Context) {
	if f, ok := a.create(c, ScopeUser, middleware.MustPrincipal(c).UserID); ok {
		httpx.OK(c, wrap(f))
	}
}

func (a *api) patchMine(c *gin.Context) {
	if f, ok := a.patch(c, ScopeUser, middleware.MustPrincipal(c).UserID); ok {
		httpx.OK(c, wrap(f))
	}
}

func (a *api) deleteMine(c *gin.Context) {
	if _, ok := a.remove(c, ScopeUser, middleware.MustPrincipal(c).UserID); ok {
		httpx.OK(c, gin.H{"deleted": true})
	}
}

func (a *api) listBuiltin(c *gin.Context) { a.list(c, ScopeBuiltin, 0) }

func (a *api) createBuiltin(c *gin.Context) {
	if f, ok := a.create(c, ScopeBuiltin, 0); ok {
		a.record(c, actionCreate, f.ID, gin.H{"trigger": f.Trigger})
		httpx.OK(c, wrap(f))
	}
}

func (a *api) patchBuiltin(c *gin.Context) {
	if f, ok := a.patch(c, ScopeBuiltin, 0); ok {
		a.record(c, actionUpdate, f.ID, gin.H{"trigger": f.Trigger})
		httpx.OK(c, wrap(f))
	}
}

func (a *api) deleteBuiltin(c *gin.Context) {
	if id, ok := a.remove(c, ScopeBuiltin, 0); ok {
		a.record(c, actionDelete, id, nil)
		httpx.OK(c, gin.H{"deleted": true})
	}
}
