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
