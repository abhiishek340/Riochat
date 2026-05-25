package uicomponent

import (
	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
)

// Mount registers the routes with the default wiring: administrator changes are
// audited through a recorder on the same database and the global switch is on.
func Mount(d deps.Deps) error {
	return MountWith(audit.NewRecorder(d.DB, d.Logger, d.Clock), nil)(d)
}

// MountWith returns a Mount function using the given audit recorder (nil
// disables auditing) and settings source (nil leaves the switch on). The
// integrator passes the settings-area adapter here.
func MountWith(rec *audit.Recorder, settings Settings) func(deps.Deps) error {
	return func(d deps.Deps) error {
		return MountService(NewService(d.DB, d.Clock, settings), rec)(d)
	}
}

// MountService mounts the routes on an already built Service, so the chat area
// and the HTTP layer can share one instance.
func MountService(svc *Service, rec *audit.Recorder) func(deps.Deps) error {
	return func(d deps.Deps) error {
		h := &handlers{svc: svc, audit: rec}

		u := d.Routes.Auth
		u.GET("/ui-components", h.listVisible)
		u.GET("/ui-components/mine", h.listMine)
		u.POST("/ui-components/mine", h.createMine)
		u.PATCH("/ui-components/mine/:id", h.updateMine)
		u.DELETE("/ui-components/mine/:id", h.deleteMine)

		a := d.Routes.Admin
		a.GET("/admin/ui-components", h.adminList)
		a.POST("/admin/ui-components", h.adminCreate)
		a.PATCH("/admin/ui-components/:id", h.adminUpdate)
		a.DELETE("/admin/ui-components/:id", h.adminDelete)
		return nil
	}
}
