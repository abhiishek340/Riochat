package announcement

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Mount registers the user and administrator routes.
//
// The user list is deliberately outside the capability gate: clients poll it
// and older ones treat a 404 as a failure, so while the capability is off it
// answers 200 with an empty array. Every other route is gated.
func Mount(d deps.Deps) error {
	h := &handlers{svc: newService(d.DB, d.Clock), caps: d.Capabilities}

	d.Routes.Auth.GET("/announcements", h.listForUser)

	gated := d.Gated(capability.Announcements)
	gated.Auth.POST("/announcements/:id/dismiss-today", h.dismissToday)
	gated.Auth.POST("/announcements/:id/close", h.close)

	gated.Admin.GET("/admin/announcements", h.adminList)
	gated.Admin.POST("/admin/announcements", h.adminCreate)
	gated.Admin.PATCH("/admin/announcements/:id", h.adminUpdate)
	gated.Admin.DELETE("/admin/announcements/:id", h.adminDelete)
	return nil
}

type handlers struct {
	svc  *service
	caps capability.Provider
}

// view is the wire shape of an announcement.
type view struct {
	ID              uint       `json:"id"`
	Title           string     `json:"title"`
	ContentMarkdown string     `json:"contentMarkdown"`
	Status          string     `json:"status"`
	Type            string     `json:"type"`
	Pinned          bool       `json:"pinned"`
	Priority        int        `json:"priority"`
	StartsAt        *time.Time `json:"startsAt"`
	ExpiresAt       *time.Time `json:"expiresAt"`
	CreatedByUserID uint       `json:"createdByUserID"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ClosedAt        *time.Time `json:"closedAt"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

func toView(a *Announcement, closedAt *time.Time) view {
	return view{
		ID: a.ID, Title: a.Title, ContentMarkdown: a.ContentMarkdown, Status: a.Status, Type: a.Type,
		Pinned: a.Pinned, Priority: a.Priority, StartsAt: utcPtr(a.StartsAt), ExpiresAt: utcPtr(a.ExpiresAt),
		CreatedByUserID: a.CreatedByUserID, CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(),
		ClosedAt: utcPtr(closedAt),
	}
}

// fail maps a service error to its HTTP status; anything unrecognized is a 500
// whose details stay in the logs.
func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Fail(c, http.StatusNotFound, err)
	case errors.Is(err, ErrInvalidID), errors.Is(err, ErrInvalidAnnouncement):
		httpx.Fail(c, http.StatusBadRequest, err)
	default:
		httpx.Fail(c, http.StatusInternalServerError, err)
	}
}

// pathID reads a positive announcement id from the route; it answers the 400
// itself and returns false when the id is unusable.
func pathID(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 || n > math.MaxInt64 {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidID)
		return 0, false
	}
	return uint(n), true
}

// parseBool accepts exactly the spellings strconv.ParseBool does; padding
// around them makes the value unparseable.
func parseBool(s string) (bool, bool) {
	v, err := strconv.ParseBool(s)
	return v, err == nil
}

// ---- user endpoints ----

func (h *handlers) listForUser(c *gin.Context) {
	if !h.caps.Enabled(capability.Announcements) {
		httpx.OK(c, []view{})
		return
	}
	include, _ := parseBool(c.Query("include_dismissed"))
	items, err := h.svc.ListForUser(c.Request.Context(), middleware.MustPrincipal(c).UserID, include)
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]view, 0, len(items))
	for i := range items {
		out = append(out, toView(&items[i].Announcement, items[i].ClosedAt))
	}
	httpx.OK(c, out)
}

// choiceRequest carries the version the client last saw.
type choiceRequest struct {
	UpdatedAt time.Time `json:"updatedAt" binding:"required"`
}

// bindChoice reads the version body. A year-1 instant counts as missing even
// when written with a non-UTC offset (which the zero-value check would miss).
func bindChoice(c *gin.Context) (time.Time, bool) {
	var req choiceRequest
	if !httpx.BindJSON(c, &req) {
		return time.Time{}, false
	}
	if req.UpdatedAt.UTC().Year() <= 1 {
		failField(c, "updatedAt", "required", "updatedAt is required")
		return time.Time{}, false
	}
	return req.UpdatedAt, true
}

func failField(c *gin.Context, field, rule, msg string) {
	httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.
		WithMessage("invalid request body: "+msg).
		WithDetails(map[string]any{
			"reason":      "validation",
			"fieldErrors": []httpx.FieldError{{Field: field, Rule: rule, Message: msg}},
		}))
}

func (h *handlers) dismissToday(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	version, ok := bindChoice(c)
	if !ok {
		return
	}
	if err := h.svc.DismissToday(c.Request.Context(), middleware.MustPrincipal(c).UserID, id, version); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"dismissed": true})
}

func (h *handlers) close(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	version, ok := bindChoice(c)
	if !ok {
		return
	}
	if err := h.svc.Close(c.Request.Context(), middleware.MustPrincipal(c).UserID, id, version); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"closed": true})
}

// ---- administrator endpoints ----

func (h *handlers) adminList(c *gin.Context) {
	f := adminFilter{
		Status:  c.Query("status"),
		Type:    c.Query("type"),
		Keyword: c.Query("q"),
	}
	if v, ok := parseBool(c.Query("pinned")); ok {
		f.Pinned = &v
	}
	rows, total, err := h.svc.AdminList(c.Request.Context(), f, pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]view, 0, len(rows))
	for i := range rows {
		out = append(out, toView(&rows[i], nil))
	}
	httpx.Paged(c, total, out)
}

type createRequest struct {
	Title           string     `json:"title" binding:"required,min=1,max=120"`
	ContentMarkdown string     `json:"contentMarkdown" binding:"required,min=1,max=20000"`
	Status          string     `json:"status" binding:"omitempty,oneof=active inactive"`
	Type            string     `json:"type" binding:"omitempty,oneof=critical warning info normal general"`
	Pinned          bool       `json:"pinned"`
	Priority        int        `json:"priority"`
	StartsAt        *time.Time `json:"startsAt"`
	ExpiresAt       *time.Time `json:"expiresAt"`
}

func (h *handlers) adminCreate(c *gin.Context) {
	var req createRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	row, err := h.svc.Create(c.Request.Context(), middleware.MustPrincipal(c).UserID, createInput{
		Title: req.Title, ContentMarkdown: req.ContentMarkdown, Status: req.Status, Type: req.Type,
		Pinned: req.Pinned, Priority: req.Priority, StartsAt: req.StartsAt, ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"announcement": toView(row, nil)})
}

// optionalTime decodes a timestamp field that distinguishes "absent" (the
// field is never visited), "cleared" (null or a blank string) and "set" (an
// RFC 3339 string).
type optionalTime struct {
	patch timePatch
}

func (o *optionalTime) UnmarshalJSON(b []byte) error {
	raw := bytes.TrimSpace(b)
	o.patch = timePatch{Set: true}
	if string(raw) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "non-string", Type: reflect.TypeOf(time.Time{})}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(time.Time{})}
	}
	o.patch.Value = &t
	return nil
}

type patchRequest struct {
	Title           *string      `json:"title" binding:"omitnil,min=1,max=120"`
	ContentMarkdown *string      `json:"contentMarkdown" binding:"omitnil,min=1,max=20000"`
	Status          *string      `json:"status" binding:"omitnil,oneof=active inactive"`
	Type            *string      `json:"type" binding:"omitnil,oneof=critical warning info normal general"`
	Pinned          *bool        `json:"pinned"`
	Priority        *int         `json:"priority"`
	StartsAt        optionalTime `json:"startsAt"`
	ExpiresAt       optionalTime `json:"expiresAt"`
}

func (h *handlers) adminUpdate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req patchRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	row, err := h.svc.Update(c.Request.Context(), id, updateInput{
		Title: req.Title, ContentMarkdown: req.ContentMarkdown, Status: req.Status, Type: req.Type,
		Pinned: req.Pinned, Priority: req.Priority, StartsAt: req.StartsAt.patch, ExpiresAt: req.ExpiresAt.patch,
	})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"announcement": toView(row, nil)})
}

func (h *handlers) adminDelete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}
