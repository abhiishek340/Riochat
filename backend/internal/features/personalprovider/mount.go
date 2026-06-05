// Package personalprovider lets users connect their own LLM endpoint and API
// key ("bring your own key") and lets administrators control the feature.
package personalprovider

import (
	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
)

// probeLimitPerMinute is the per-user cap for endpoints that call a
// user-supplied address.
const probeLimitPerMinute = 10

// Mount registers the routes with the default ports: settings read from
// system_settings, the built-in HTTP model lister, owners read from the
// identity table and the shared audit recorder.
func Mount(d deps.Deps) error {
	_, err := MountWith(d, Options{})
	return err
}

// MountWith registers the routes using opts and returns the Service for other
// areas: the channel area lists ActiveModels, the conversation area calls
// Resolve, the account-deletion flow uses PurgeUserTx and the rotation job
// calls ReencryptKeys.
func MountWith(d deps.Deps, opts Options) (*Service, error) {
	if opts.Lister == nil {
		opts.Lister = NewHTTPLister(PublicOnlyPolicy{})
	}
	if opts.Audit == nil {
		opts.Audit = audit.NewRecorder(d.DB, d.Logger, d.Clock)
	}
	svc := NewService(d.DB, d.Clock, d.Secrets, d.Logger, opts)
	h := &handlers{svc: svc, audit: opts.Audit}
	registerRoutes(d.Routes, d.ProbeLimit(probeLimitPerMinute), h)
	return svc, nil
}

func registerRoutes(r deps.Routes, probe gin.HandlerFunc, h *handlers) {
	const base = "/me/model-providers"
	r.Auth.GET(base+"/access", h.access)
	r.Auth.GET(base, h.list)
	r.Auth.POST(base+"/probe", probe, h.probe)
	r.Auth.POST(base, probe, h.create)
	r.Auth.PATCH(base+"/:id", probe, h.update)
	r.Auth.DELETE(base+"/:id", h.remove)
	r.Auth.GET(base+"/:id/models", probe, h.models)

	const admin = "/admin/model-providers"
	r.Admin.GET(admin, h.adminList)
	r.Admin.POST(admin+"/suspend", h.adminSuspend)
	r.Admin.POST(admin+"/suspend-host", h.adminSuspendHost)
	r.Admin.POST(admin+"/batch-delete", h.adminBatchDelete)
}
