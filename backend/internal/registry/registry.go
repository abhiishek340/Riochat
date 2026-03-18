// Package registry is the one list of feature areas the server mounts. It is a
// separate package from internal/features because every area's tests import
// the test kit, which imports internal/features: the list of areas must not
// live there or the area tests would import themselves.
package registry

import (
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/announcement"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/features/auth"
	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/contentmoderation"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/errorlog"
	"github.com/riochat/riochat/backend/internal/features/knowledgebase"
	"github.com/riochat/riochat/backend/internal/features/mcp"
	"github.com/riochat/riochat/backend/internal/features/memory"
	"github.com/riochat/riochat/backend/internal/features/personalprovider"
	platformllm "github.com/riochat/riochat/backend/internal/features/platform-llm"
	"github.com/riochat/riochat/backend/internal/features/promptreset"
	"github.com/riochat/riochat/backend/internal/features/ragindex"
	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/features/skill"
	"github.com/riochat/riochat/backend/internal/features/uicomponent"
	"github.com/riochat/riochat/backend/internal/features/user"
	"github.com/riochat/riochat/backend/internal/integration"
)

// Registered returns every feature area in mount order.
//
// THIS FILE IS EDITED ONLY BY THE INTEGRATOR. Feature engineers never touch it:
// they build and test their area in isolation (see ARCHITECTURE.md) and ask the
// integrator to add one line here.
//
// Each call builds a fresh integration hub, so two applications in one process
// never share services. The hub's Mount* methods give each area its
// collaborators (see internal/integration).
//
// Order matters in two places: migration steps of earlier areas run first inside
// each phase, and routes are registered in this order (put an area before any
// area whose URL it must shadow, though areas should own disjoint prefixes).
// The settings area comes before any area that reads settings while mounting.
//
// The usersettings package is deliberately not registered: the user area serves
// the same two routes (GET and PATCH /user/settings) together with the shared
// value cache, and two areas must never own one route.
func Registered() []features.Feature {
	h := integration.New()
	return []features.Feature{
		{Name: "platformllm", Migrations: platformllm.Migrations, Mount: h.MountPlatformLLM},
		{Name: "audit", Migrations: audit.Migrations, Mount: h.MountAudit},
		{Name: "settings", Migrations: settings.Migrations, Mount: h.MountSettings},
		{Name: "errorlog", Migrations: errorlog.Migrations, Mount: h.MountErrorLog},
		{Name: "auth", Migrations: auth.Migrations, Mount: h.MountAuth},
		{Name: "user", Migrations: user.Migrations, Mount: h.MountUser},
		{Name: "announcement", Migrations: announcement.Migrations, Mount: h.MountAnnouncement},
		{Name: "channel", Migrations: channel.Migrations, Mount: h.MountChannel},
		{Name: "permissiongroup", Migrations: integration.PermissionMigrations, Mount: h.MountPermissions},
		{Name: "billing", Migrations: billing.Migrations, Mount: h.MountBilling},
		{Name: "personalprovider", Migrations: personalprovider.Migrations, Mount: h.MountPersonalProvider},
		{Name: "knowledgebase", Migrations: knowledgebase.Migrations, Mount: h.MountKnowledgeBase},
		{Name: "ragindex", Migrations: ragindex.Migrations, Mount: h.MountRAGIndex},
		{Name: "memory", Migrations: memory.Migrations, Mount: h.MountMemory},
		{Name: "mcp", Migrations: mcp.Migrations, Mount: h.MountMCP},
		{Name: "skill", Migrations: skill.Migrations, Mount: h.MountSkill},
		{Name: "uicomponent", Migrations: uicomponent.Migrations, Mount: h.MountUIComponent},
		{Name: "contentmoderation", Migrations: contentmoderation.Migrations, Mount: h.MountContentModeration},
		{Name: "promptreset", Migrations: promptreset.Migrations, Mount: h.MountPromptReset},
		{Name: "conversation", Migrations: conversation.Migrations, Mount: h.MountConversation},
	}
}
