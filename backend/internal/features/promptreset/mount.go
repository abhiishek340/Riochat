package promptreset

import (
	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
)

// Mount registers the routes. Administrator changes are written to the audit
// log through a recorder on the same database.
func Mount(d deps.Deps) error {
	return MountWith(audit.NewRecorder(d.DB, d.Logger, d.Clock))(d)
}

// MountWith returns a Mount function that uses the given audit recorder. A nil
// recorder disables auditing.
func MountWith(rec *audit.Recorder) func(deps.Deps) error {
	return func(d deps.Deps) error {
		h := &handlers{svc: newService(d.DB, d.Clock), audit: rec}

		u := d.Routes.Auth
		u.GET("/prompt-presets", h.listVisible)
		u.GET("/prompt-presets/mine", h.listMine)
		u.POST("/prompt-presets/mine", h.createMine)
		u.PATCH("/prompt-presets/mine/:id", h.updateMine)
		u.DELETE("/prompt-presets/mine/:id", h.deleteMine)

		a := d.Routes.Admin
		a.GET("/admin/prompt-presets", h.adminList)
		a.POST("/admin/prompt-presets", h.adminCreate)
		a.PATCH("/admin/prompt-presets/:id", h.adminUpdate)
		a.DELETE("/admin/prompt-presets/:id", h.adminDelete)
		return nil
	}
}
