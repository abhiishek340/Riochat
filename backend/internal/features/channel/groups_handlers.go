package channel

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// groupBody is the editable part of a permission group.
type groupBody struct {
	Name                  string `json:"name" binding:"required,max=128"`
	Description           string `json:"description" binding:"max=512"`
	RateMultiplierPercent int    `json:"rateMultiplierPercent" binding:"min=0,max=10000"`
}

func (b groupBody) input() GroupInput {
	return GroupInput{Name: b.Name, Description: b.Description, RateMultiplierPercent: b.RateMultiplierPercent}
}

type ruleBody struct {
	Type  string `json:"type" binding:"required,max=32"`
	Value string `json:"value" binding:"max=128"`
}

type groupModelsBody struct {
	ModelIDs []uint     `json:"modelIDs"`
	Rules    []ruleBody `json:"rules" binding:"dive"`
}

type groupUsersBody struct {
	UserIDs []uint `json:"userIDs"`
}

type modelGroupsBody struct {
	GroupIDs []uint `json:"groupIDs"`
}

// mountGroupAdmin registers the permission-group administration routes. They
// exist only while the multi-user capability is on.
func mountGroupAdmin(d deps.Deps, h *handlers) {
	a := d.Gated(capability.MultiUser).Admin
	a.GET("/admin/permission-groups", h.listPermGroups)
	a.POST("/admin/permission-groups", h.createPermGroup)
	a.PATCH("/admin/permission-groups/:id", h.updatePermGroup)
	a.DELETE("/admin/permission-groups/:id", h.deletePermGroup)
	a.GET("/admin/permission-groups/:id/models", h.getPermGroupModels)
	a.PUT("/admin/permission-groups/:id/models", h.putPermGroupModels)
	a.GET("/admin/permission-groups/:id/users", h.getPermGroupUsers)
	a.PUT("/admin/permission-groups/:id/users", h.putPermGroupUsers)
	a.GET("/admin/models/:modelID/permission-groups", h.getModelPermGroups)
	a.PUT("/admin/models/:modelID/permission-groups", h.putModelPermGroups)
}

func (h *handlers) listPermGroups(c *gin.Context) {
	list, err := h.svc.AdminListGroups(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"results": list})
}

func (h *handlers) createPermGroup(c *gin.Context) {
	var req groupBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	g, err := h.svc.AdminCreateGroup(c.Request.Context(), req.input())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"group": g})
}

func (h *handlers) updatePermGroup(c *gin.Context) {
	id, ok := positiveID(c, "id", ErrInvalidPermGroupID)
	if !ok {
		return
	}
	var req groupBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	g, err := h.svc.AdminUpdateGroup(c.Request.Context(), id, req.input())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"group": g})
}

func (h *handlers) deletePermGroup(c *gin.Context) {
	id, ok := positiveID(c, "id", ErrInvalidPermGroupID)
	if !ok {
		return
	}
	sum, err := h.svc.AdminDeleteGroup(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true, "summary": sum})
}

func (h *handlers) getPermGroupModels(c *gin.Context) {
	id, ok := positiveID(c, "id", ErrInvalidPermGroupID)
	if !ok {
		return
	}
	set, err := h.svc.AdminGroupModels(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, set)
}

func (h *handlers) putPermGroupModels(c *gin.Context) {
	id, ok := positiveID(c, "id", ErrInvalidPermGroupID)
	if !ok {
		return
	}
	var req groupModelsBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	rules := make([]GroupRule, 0, len(req.Rules))
	for _, r := range req.Rules {
		rules = append(rules, GroupRule{Type: r.Type, Value: r.Value})
	}
	set, err := h.svc.AdminReplaceGroupModels(c.Request.Context(), id, req.ModelIDs, rules)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, set)
}

func (h *handlers) getPermGroupUsers(c *gin.Context) {
	id, ok := positiveID(c, "id", ErrInvalidPermGroupID)
	if !ok {
		return
	}
	ids, err := h.svc.AdminGroupMembers(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"userIDs": ids})
}

func (h *handlers) putPermGroupUsers(c *gin.Context) {
	id, ok := positiveID(c, "id", ErrInvalidPermGroupID)
	if !ok {
		return
	}
	var req groupUsersBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	ids, err := h.svc.AdminReplaceGroupMembers(c.Request.Context(), id, req.UserIDs)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"userIDs": ids})
}

func (h *handlers) getModelPermGroups(c *gin.Context) {
	id, ok := positiveID(c, "modelID", ErrInvalidModelID)
	if !ok {
		return
	}
	set, err := h.svc.AdminModelGroups(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, set)
}

func (h *handlers) putModelPermGroups(c *gin.Context) {
	id, ok := positiveID(c, "modelID", ErrInvalidModelID)
	if !ok {
		return
	}
	var req modelGroupsBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	set, err := h.svc.AdminReplaceModelGroups(c.Request.Context(), id, req.GroupIDs)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, set)
}

// positiveID is pathID that also rejects zero.
func positiveID(c *gin.Context, param string, bad *apperr.Error) (uint, bool) {
	id, ok := pathID(c, param, bad)
	if ok && id == 0 {
		httpx.Fail(c, http.StatusBadRequest, bad)
		return 0, false
	}
	return id, ok
}
