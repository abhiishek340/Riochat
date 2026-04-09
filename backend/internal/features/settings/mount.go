// Package settings owns the platform's dynamic configuration: the namespaced
// system_settings store with its administrator API, the runtime projection other
// modules read, the public policy and branding endpoints, and the per-user
// preference store.
package settings

import (
	"context"
	"fmt"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
)

// Mount registers the area with auditing wired to the audit area's recorder.
// Collaborators that live in other areas (embedding index, managed runtimes,
// super administrator check, vector store) are optional; see MountWith.
func Mount(d deps.Deps) error {
	_, err := MountWith(d, Ports{Audit: auditAdapter{rec: audit.NewRecorder(d.DB, d.Logger, d.Clock)}})
	return err
}

// MountWith registers the area with the given collaborators and returns the
// service so the integrator can wire more of them later (Service.WirePorts) and
// hand the runtime to other areas (Service.Runtime).
//
// Mounting seeds the registry defaults, projects the stored values into the
// runtime snapshot and publishes the platform-owned part of it to the
// configuration holder. Register this area before any area that reads settings
// while mounting.
//
// Options adjust what is registered; see WithoutUserSettingsRoutes.
func MountWith(d deps.Deps, p Ports, opts ...Option) (*Service, error) {
	var mc mountConfig
	for _, o := range opts {
		o(&mc)
	}
	svc := newService(d, p)
	ctx := context.Background()

	if err := svc.seed(ctx); err != nil {
		return nil, err
	}
	if err := svc.Reload(ctx); err != nil {
		return nil, fmt.Errorf("settings: load runtime: %w", err)
	}
	svc.backfillSignature(ctx)

	h := &handlers{svc: svc, cfg: d.Config}
	svc.routes(d, h, mc)
	return svc, nil
}

func (s *Service) routes(d deps.Deps, h *handlers, mc mountConfig) {
	pub := d.Routes.Public
	pub.GET("/branding", h.branding)
	pub.GET("/branding/manifest.webmanifest", h.manifest)
	// The manifest is also served from the site root, outside the API prefix:
	// the relative path climbs out of /api/v1 and gin cleans it.
	pub.GET("/../../manifest.webmanifest", h.manifest)
	pub.GET("/settings/login-page", h.loginPage)

	user := d.Routes.Auth
	user.GET("/settings/model-option-policy", h.modelOptionPolicy)
	if !mc.skipMCPPolicy {
		user.GET("/settings/mcp-policy", h.mcpPolicy)
	}
	user.GET("/settings/chat-context-policy", h.chatContextPolicy)
	user.GET("/settings/feature-policy", h.featurePolicy)
	if !mc.skipUserSettings {
		user.GET("/user/settings", h.getUserSettings)
		user.PATCH("/user/settings", h.patchUserSettings)
	}

	adm := d.Routes.Admin
	adm.GET("/admin/settings", h.listAll)
	adm.PATCH("/admin/settings", h.patch)
	adm.GET("/admin/settings/:namespace", h.listNamespace)

	adm.GET("/admin/settings/embedding/status", h.embeddingStatus)
	adm.POST("/admin/settings/embedding/reindex", h.embeddingReindex)
	adm.GET("/admin/settings/embedding/tasks", h.embeddingTasks)
	adm.POST("/admin/settings/embedding/tasks/retry", h.embeddingRetry)
	adm.GET("/admin/settings/embedding/runtime", h.embeddingRuntime)

	for _, name := range []string{RuntimeTika, RuntimeDocling, RuntimeTesseract, RuntimeRapidOCR, RuntimeMinerU} {
		adm.GET("/admin/settings/"+name+"/runtime", h.runtimeStatus(name))
	}
	for _, name := range []string{RuntimeTika, RuntimeRapidOCR} {
		for _, action := range []string{"start", "stop", "restart"} {
			adm.POST("/admin/settings/"+name+"/runtime/"+action, h.runtimeAction(name, action))
		}
	}
}

type mountConfig struct{ skipUserSettings, skipMCPPolicy bool }

// Option customises MountWith.
type Option func(*mountConfig)

// WithoutUserSettingsRoutes leaves GET and PATCH /user/settings unregistered,
// for deployments where the dedicated usersettings area serves them. The
// service methods (UserSettings, PatchUserSettings) stay available.
func WithoutUserSettingsRoutes() Option { return func(c *mountConfig) { c.skipUserSettings = true } }

// auditAdapter adapts the audit area's recorder to AuditWriter.
type auditAdapter struct{ rec *audit.Recorder }

func (a auditAdapter) Record(ctx context.Context, e AuditEntry) {
	a.rec.Record(ctx, audit.Entry{
		RequestID: e.RequestID, ActorUserID: e.ActorUserID, Action: e.Action,
		Resource: e.Resource, ResourceID: e.ResourceID, IP: e.ClientIP, UserAgent: e.UserAgent,
		Detail: e.Detail,
	})
}

// WithoutMCPPolicyRoute leaves GET /settings/mcp-policy unregistered, for
// deployments where the mcp area serves it.
func WithoutMCPPolicyRoute() Option { return func(c *mountConfig) { c.skipMCPPolicy = true } }
