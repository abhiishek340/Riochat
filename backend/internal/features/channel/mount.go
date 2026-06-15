package channel

import (
	"context"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
)

// probeRatePerMinute bounds, per administrator, the endpoints that make the
// server call upstream addresses.
const probeRatePerMinute = 120

// Mount registers the channel area with no external collaborators: remote
// discovery and probes answer as "unavailable" until an adapter is supplied,
// and every model is listed without prices. The integrator normally calls
// MountWith from a registry-level closure to supply the collaborators.
func Mount(d deps.Deps) error { return MountWith(d, Options{}) }

// MountWith registers routes, startup hooks and workers with the given
// collaborators.
func MountWith(d deps.Deps, o Options) error {
	svc := NewService(d, o)
	h := &handlers{svc: svc}

	// User side.
	d.Routes.Auth.GET("/models", h.listModels)
	d.Routes.Public.GET("/llm/icon-assets/:public_id", h.readIcon)

	a := d.Routes.Admin
	probe := d.ProbeLimit(probeRatePerMinute)

	mountGroupAdmin(d, h)

	// Upstreams.
	a.GET("/admin/llm/upstreams", h.listUpstreams)
	a.POST("/admin/llm/upstreams", h.createUpstream)
	a.POST("/admin/llm/upstreams/batch-delete", h.batchDeleteUpstreams)
	a.PATCH("/admin/llm/upstreams/:id", h.updateUpstream)
	a.DELETE("/admin/llm/upstreams/:id", h.deleteUpstream)
	a.POST("/admin/llm/upstreams/:id/circuit/open", h.openUpstreamCircuit)
	a.POST("/admin/llm/upstreams/:id/circuit/reset", h.resetUpstreamCircuit)

	// Upstream models and their route bindings.
	a.GET("/admin/llm/upstreams/:id/models", h.listUpstreamModels)
	a.POST("/admin/llm/upstreams/:id/models", h.upsertBinding)
	a.GET("/admin/llm/upstreams/:id/models/remote", probe, h.previewRemote)
	a.POST("/admin/llm/upstreams/:id/models/sync", probe, h.syncRemote)
	a.POST("/admin/llm/upstreams/:id/models/import", probe, h.importRemote)
	a.POST("/admin/llm/upstreams/:id/models/batch-delete", h.batchDeleteRoutes)
	a.DELETE("/admin/llm/upstreams/:id/models/:route_id", h.deleteRoute())
	a.PATCH("/admin/llm/upstreams/:id/models/:route_id/disable", h.setRouteStatus(StatusInactive))
	a.PATCH("/admin/llm/upstreams/:id/models/:route_id/enable", h.setRouteStatus(StatusActive))
	a.POST("/admin/llm/upstreams/:id/models/:route_id/test", probe, h.probeRoute())
	a.POST("/admin/llm/upstreams/:id/models/:route_id/circuit/open", h.openBindingCircuit())
	a.POST("/admin/llm/upstreams/:id/models/:route_id/circuit/reset", h.resetBindingCircuit())

	// Platform models.
	a.GET("/admin/llm/models", h.listAdminModels)
	a.POST("/admin/llm/models", h.createModel)
	a.POST("/admin/llm/models/order", h.reorderModels)
	a.POST("/admin/llm/models/batch-delete", h.batchDeleteModels)
	a.PATCH("/admin/llm/models/display-group", h.assignDisplayGroup)
	a.PATCH("/admin/llm/models/:id", h.updateModel)
	a.PATCH("/admin/llm/models/:id/protocols", h.setModelProtocols)
	a.DELETE("/admin/llm/models/:id", h.deleteModel)
	a.POST("/admin/llm/models/:id/test", probe, h.probeModel)
	a.POST("/admin/llm/models/:id/test-all", probe, h.probeModelAll)
	a.GET("/admin/llm/models/:id/sources", h.listSources)
	a.POST("/admin/llm/models/:id/sources", h.bindSource)
	a.PATCH("/admin/llm/models/:id/sources/:route_id", h.updateSource)

	// Presentation metadata.
	a.GET("/admin/llm/model-vendors", h.listVendors)
	a.POST("/admin/llm/model-vendors", h.createVendor)
	a.PATCH("/admin/llm/model-vendors/:key", h.updateVendor)
	a.DELETE("/admin/llm/model-vendors/:key", h.deleteVendor)
	a.GET("/admin/llm/model-display-groups", h.listGroups)
	a.POST("/admin/llm/model-display-groups", h.createGroup)
	a.PATCH("/admin/llm/model-display-groups/:id", h.updateGroup)
	a.DELETE("/admin/llm/model-display-groups/:id", h.deleteGroup)

	// Icon assets.
	a.POST("/admin/llm/icon-assets", h.uploadIcon)
	a.GET("/admin/llm/icon-assets", h.listIcons)
	a.DELETE("/admin/llm/icon-assets/:public_id", h.deleteIcon)

	// models.dev catalog.
	a.GET("/admin/llm/model-catalog", h.catalogStatus)
	a.POST("/admin/llm/model-catalog/resolve", h.catalogResolve)
	a.POST("/admin/llm/model-catalog/refresh", h.catalogRefresh)

	// Global runtime settings.
	a.GET("/admin/llm/settings", h.listSettings)
	a.PATCH("/admin/llm/settings/:key", h.patchSetting)

	// The catalog is loaded from disk or the built-in snapshot before traffic.
	d.Lifecycle.OnStart("llm-catalog-load", func(ctx context.Context) error {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		svc.catalog.load(lctx)
		return nil
	})
	svc.startIconCleanup()
	if o.OnReady != nil {
		o.OnReady(svc)
	}
	return nil
}
