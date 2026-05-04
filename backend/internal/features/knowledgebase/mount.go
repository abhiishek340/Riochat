package knowledgebase

import "github.com/riochat/riochat/backend/internal/deps"

// Mount registers the area with default collaborators: the feature switch is
// read from system_settings, audit records go to the log, and everything that
// needs another subsystem (file content, cleanup, upload, embedding) answers
// with its documented "unavailable" error until MountWith supplies one.
func Mount(d deps.Deps) error { return MountWith(Ports{})(d) }

// MountWith returns a Mount function that uses the given collaborators.
func MountWith(ports Ports) func(deps.Deps) error {
	return func(d deps.Deps) error {
		return MountService(d, NewService(d.DB, d.Clock, d.Logger, ports))
	}
}

// MountService registers the routes for an already built service, so the
// integrator can share one Service with the areas that call its exported
// methods.
func MountService(d deps.Deps, svc *Service) error {
	h := &handlers{svc: svc}

	// User side: authenticated, behind the feature switch. The literal
	// "mine" segment takes precedence over the :id parameter.
	u := d.Routes.Auth.Group("", h.requireEnabled)
	u.GET("/knowledge-bases", h.listVisible)
	u.GET("/knowledge-bases/mine", h.listMine)
	u.POST("/knowledge-bases/mine", h.createMine)
	u.PATCH("/knowledge-bases/mine/:id", h.updateMine)
	u.DELETE("/knowledge-bases/mine/:id", h.deleteMine)
	u.GET("/knowledge-bases/mine/:id/available-files", h.availableMine)
	u.POST("/knowledge-bases/mine/:id/files", h.addMine)
	u.DELETE("/knowledge-bases/mine/:id/files/:file_id", h.removeMine)
	u.GET("/knowledge-bases/:id", h.getVisible)
	u.GET("/knowledge-bases/:id/files", h.filesVisible)
	u.POST("/knowledge-bases/:id/files/processing/statuses", h.statusesVisible)
	u.POST("/knowledge-bases/:id/files/processing/snapshot", h.snapshotVisible)
	u.GET("/knowledge-bases/:id/files/:file_id/content", h.contentVisible)

	// Administrator side: not behind the feature switch. The literal "files"
	// segment (the platform pool) takes precedence over the :id parameter.
	a := d.Routes.Admin
	a.GET("/admin/knowledge-bases", h.adminList)
	a.POST("/admin/knowledge-bases", h.adminCreate)
	a.GET("/admin/knowledge-bases/files", h.poolList)
	a.POST("/admin/knowledge-bases/files", h.poolUpload)
	a.POST("/admin/knowledge-bases/files/embeddings", h.poolEmbeddings)
	a.GET("/admin/knowledge-bases/files/:file_id/content", h.poolContent)
	a.DELETE("/admin/knowledge-bases/files/:file_id", h.poolDelete)
	a.GET("/admin/knowledge-bases/:id", h.adminGet)
	a.PATCH("/admin/knowledge-bases/:id", h.adminUpdate)
	a.DELETE("/admin/knowledge-bases/:id", h.adminDelete)
	a.GET("/admin/knowledge-bases/:id/files", h.adminFiles)
	a.POST("/admin/knowledge-bases/:id/files/processing/statuses", h.adminStatuses)
	a.POST("/admin/knowledge-bases/:id/files/processing/snapshot", h.adminSnapshot)
	a.GET("/admin/knowledge-bases/:id/files/:file_id/content", h.adminContent)
	a.GET("/admin/knowledge-bases/:id/available-files", h.adminAvailable)
	a.POST("/admin/knowledge-bases/:id/files", h.adminAddFiles)
	a.DELETE("/admin/knowledge-bases/:id/files/:file_id", h.adminRemoveFile)
	return nil
}
