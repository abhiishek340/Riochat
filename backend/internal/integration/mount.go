package integration

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
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
	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/features/skill"
	"github.com/riochat/riochat/backend/internal/features/uicomponent"
	"github.com/riochat/riochat/backend/internal/features/user"
)

// The Mount* methods below are the registry's mount functions. Each passes the
// area its collaborators and keeps the services the area publishes.

// MountPlatformLLM mounts the outbound-integration layer.
func (h *Hub) MountPlatformLLM(d deps.Deps) error { return platformllm.Mount(d) }

// MountAudit mounts the audit trail, gives it the user lookup for listings and
// registers the shared log-cleanup endpoint.
func (h *Hub) MountAudit(d deps.Deps) error {
	audit.SetActorResolver(actorResolver(h.userAccounts(d)))
	if err := audit.Mount(d); err != nil {
		return err
	}
	h.mountLogCleanup(d)
	return nil
}

// MountSettings mounts the settings area. The per-user preference routes and
// the MCP policy route are served by the user and mcp areas.
func (h *Hub) MountSettings(d deps.Deps) error {
	svc, err := settings.MountWith(d, settings.Ports{
		Audit:       settingsAudit{rec: h.recorder(d)},
		SuperAdmins: superAdmins{db: d.DB},
	}, settings.WithoutUserSettingsRoutes(), settings.WithoutMCPPolicyRoute())
	if err != nil {
		return err
	}
	h.settings = svc
	return nil
}

type superAdmins struct{ db *gorm.DB }

func (s superAdmins) HasBoundSuperAdmin(ctx context.Context) (bool, error) {
	return auth.HasActiveSuperadminWithIdentity(ctx, s.db)
}

// MountErrorLog mounts the error log.
func (h *Hub) MountErrorLog(d deps.Deps) error { return errorlog.Mount(d) }

// MountAuth mounts sign-in, sessions and the profile.
func (h *Hub) MountAuth(d deps.Deps) error {
	h.userAccounts(d)
	return auth.MountWith(d, auth.Hooks{
		Settings:      authSettings{h: h},
		GeoIP:         geoResolver{},
		Audit:         authAudit{rec: h.recorder(d)},
		Subscriptions: authSubscriptions{h: h},
		AvatarFiles:   avatarChecker{h: h},
		Purgers:       []auth.AccountPurger{newAccountPurger(h)},
	})
}

// MountUser mounts per-user preferences, avatars and activity.
func (h *Hub) MountUser(d deps.Deps) error {
	h.userSettings(d)
	return user.MountWith(d, user.Ports{
		Files: userFiles{h: h}, Images: userImages{h: h}, Billing: userBilling{h: h}, Audit: h.recorder(d),
	})
}

// MountAnnouncement mounts announcements.
func (h *Hub) MountAnnouncement(d deps.Deps) error { return announcement.Mount(d) }

// MountChannel mounts upstreams, models and routing.
func (h *Hub) MountChannel(d deps.Deps) error {
	return channel.MountWith(d, channel.Options{
		Client:        modelClient{},
		Pricing:       pricingSource{h: h},
		Personal:      personalModels{h: h},
		Subscriptions: subscriptionGroups{h: h, db: d.DB},
		Users:         userCounter{db: d.DB},
		Plans:         planReferences{h: h},
		Verifier:      h.userAccounts(d),
		OnReady:       func(s *channel.Service) { h.channel = s },
	})
}

// MountBilling mounts metering, plans and payments.
func (h *Hub) MountBilling(d deps.Deps) error {
	return billing.MountWith(billing.Ports{
		Settings: billingStore{h: h},
		Audit:    billingAudit{rec: h.recorder(d)},
		Models:   modelCatalog{h: h},
		Rates:    rateResolver{h: h},
		Groups:   permissionGroups{db: d.DB},
		Users:    billingUsers{db: d.DB},
	}, func(s *billing.Service) { h.billing = s })(d)
}

// MountPersonalProvider mounts user-owned model providers.
func (h *Hub) MountPersonalProvider(d deps.Deps) error {
	svc, err := personalprovider.MountWith(d, personalprovider.Options{})
	if err != nil {
		return err
	}
	h.personal = svc
	return nil
}

// MountKnowledgeBase mounts knowledge bases.
func (h *Hub) MountKnowledgeBase(d deps.Deps) error {
	svc := knowledgebase.NewService(d.DB, d.Clock, d.Logger, knowledgebase.Ports{
		Auditor:    kbAudit{rec: h.recorder(d)},
		Reader:     kbReader{h: h},
		Cleaner:    kbCleaner{h: h},
		Uploader:   kbUploader{h: h},
		Embedder:   kbEmbedder{h: h},
		Vectorizer: kbVectorizer{h: h},
	})
	h.kb = svc
	return knowledgebase.MountService(d, svc)
}

// MountMemory mounts long-term user memory.
func (h *Hub) MountMemory(d deps.Deps) error {
	svc, err := memory.MountWith(d, memory.Options{Embedder: memoryEmbedder{h: h}})
	if err != nil {
		return err
	}
	h.memory = svc
	return nil
}

// MountMCP mounts tool-server administration and the run-time tool layer.
func (h *Hub) MountMCP(d deps.Deps) error {
	svc, err := mcp.MountService(d, mcp.Ports{Settings: mcpSettings{h: h}, BillingModes: billingModes{h: h}})
	if err != nil {
		return err
	}
	h.mcp = svc
	return nil
}

type billingModes struct{ h *Hub }

func (b billingModes) BillingMode(ctx context.Context) (string, error) {
	if b.h.billing == nil {
		return "self", nil
	}
	return b.h.billing.Mode(ctx), nil
}

// MountSkill mounts skills.
func (h *Hub) MountSkill(d deps.Deps) error {
	h.skills = skill.NewService(d.DB, d.Clock)
	return skill.Mount(d)
}

// MountUIComponent mounts interactive-component definitions.
func (h *Hub) MountUIComponent(d deps.Deps) error {
	svc := uicomponent.NewService(d.DB, d.Clock, uiSettings{h: h})
	h.ui = svc
	return uicomponent.MountService(svc, h.recorder(d))(d)
}

// MountContentModeration mounts content moderation.
func (h *Hub) MountContentModeration(d deps.Deps) error {
	svc, err := contentmoderation.MountWith(d, contentmoderation.Ports{
		Runs:     moderationRuns{h: h},
		Files:    moderationFiles{h: h},
		Objects:  moderationObjects{m: h.objectManager(d)},
		Notifier: moderationNotifier{h: h},
		Audit:    moderationAudit{rec: h.recorder(d)},
		Users:    moderationUsers{db: d.DB},
	})
	if err != nil {
		return err
	}
	h.mod = svc
	return nil
}

// MountPromptReset mounts prompt presets.
func (h *Hub) MountPromptReset(d deps.Deps) error { return promptreset.MountWith(h.recorder(d))(d) }

// MountConversation mounts conversations, runs and chat files.
func (h *Hub) MountConversation(d deps.Deps) error {
	h.userSettings(d)
	return conversation.MountWith(conversation.Ports{
		Generator:    newGenerator(h, d.Logger),
		Billing:      newConvBilling(h),
		Moderation:   convModeration{h: h},
		Catalog:      convCatalog{h: h},
		Settings:     convSettings{h: h},
		Auditor:      convAudit{rec: h.recorder(d)},
		Capabilities: convCapabilities{h: h, db: d.DB},
		OnFiles:      func(f *conversation.Files) { h.files = f },
		Objects:      chatObjects{m: h.objectManager(d)},
		OnModeration: func(b *conversation.ModerationBridge) { h.modBridge = b },
		Indexer:      ragIndexer{h: h},
		Knowledge:    ragKnowledge{h: h},
	})(d)
}
