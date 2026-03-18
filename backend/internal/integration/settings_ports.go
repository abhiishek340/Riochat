package integration

import (
	"context"
	"strings"

	"github.com/riochat/riochat/backend/internal/features/auth"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/mcp"
	"github.com/riochat/riochat/backend/internal/features/settings"
)

// runtime returns the current settings snapshot, or the zero value before the
// settings area has been mounted.
func (h *Hub) runtime() settings.Runtime {
	if h.settings == nil {
		return settings.Runtime{}
	}
	if rt := h.settings.Runtime(); rt != nil {
		return *rt
	}
	return settings.Runtime{}
}

// ---- auth ----

type authSettings struct{ h *Hub }

func (a authSettings) AuthSettings(ctx context.Context) auth.Settings {
	if a.h.settings == nil {
		return auth.DefaultSettings()
	}
	r := a.h.runtime().Auth
	next, err := a.h.settings.LoginDefaultNextPath(ctx)
	if err != nil || next == "" {
		next = "/chat"
	}
	return auth.Settings{
		LoginMaxFailures: r.LoginMaxFailures, LoginLockMinutes: r.LoginLockMinutes, LoginDefaultNextPath: next,
		UsernameLoginEnabled: r.UsernameLoginEnabled, EmailLoginEnabled: r.EmailLoginEnabled,
		ThirdPartyLoginEnabled: r.ThirdPartyLoginEnabled, EmailRegistrationEnabled: r.EmailRegistrationEnabled,
		EmailVerificationEnabled: r.EmailVerificationEnabled, PasswordResetEnabled: r.PasswordResetEnabled,
		SMTPHost: r.SMTPHost, SMTPPort: r.SMTPPort, SMTPUsername: r.SMTPUsername, SMTPPassword: r.SMTPPassword,
		SMTPFrom: r.SMTPFrom, EmailAllowedDomains: r.EmailRegistrationAllowedDomains,
		EmailBlockPlusAlias: r.EmailRegistrationBlockPlusAlias, AutoLinkVerifiedEmail: r.AutoLinkVerifiedEmail,
		TurnstileRegistrationEnabled: r.TurnstileRegistrationEnabled, TurnstileSiteKey: r.TurnstileSiteKey,
		TurnstileSecretKey: r.TurnstileSecretKey,
	}
}

// ---- mcp ----

type mcpSettings struct{ h *Hub }

func (m mcpSettings) MCPSettings(context.Context) mcp.RuntimeSettings {
	r := m.h.runtime().MCP
	return mcp.RuntimeSettings{
		Enabled: r.Enable, ToolTimeoutSeconds: r.ToolTimeoutSeconds, RetryCount: r.ToolRetryCount,
		MaxConcurrentCalls: r.MaxConcurrentCalls, MaxSelectedToolsPerMessage: r.MaxSelectedToolsPerMessage,
		MaxLLMCallsPerRun: r.MaxLLMCallsPerRun, MaxToolCallsPerRun: r.MaxToolCallsPerRun, ToolPrompt: r.ToolPrompt,
	}
}

// ---- billing settings store ----

type billingStore struct{ h *Hub }

func (b billingStore) LoadBilling(ctx context.Context) (map[string]string, error) {
	return b.h.settings.LoadBilling(ctx)
}

func (b billingStore) UpdateBilling(ctx context.Context, values map[string]string) error {
	return b.h.settings.PatchValues(ctx, "billing", values)
}

// ---- conversation ----

type convSettings struct{ h *Hub }

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ';' || r == ' ' || r == '\t' })
}

func (c convSettings) Runtime(ctx context.Context) conversation.RuntimeSettings {
	rt := c.h.runtime()
	d := conversation.DefaultRuntimeSettings()
	if c.h.settings == nil {
		return d
	}
	ch, f, st := rt.Chat, rt.File, rt.Storage
	out := conversation.RuntimeSettings{
		DefaultNewConversationModel:  ch.ConversationDefaultModel,
		TaskModel:                    ch.ConversationTaskModel,
		TitlePrompt:                  ch.ConversationTitlePrompt,
		LabelsPrompt:                 ch.ConversationLabelsPrompt,
		UIComponentsEnabled:          ch.UIComponentsEnabled,
		KnowledgeBaseEnabled:         rt.KnowledgeBase.Enabled,
		MaxUploadBytes:               st.MaxUploadFileBytes,
		UserStorageQuotaBytes:        st.UserStorageQuotaBytes,
		MaxMessageFiles:              st.MaxMessageFiles,
		MaxSelectedTools:             rt.MCP.MaxSelectedToolsPerMessage,
		MaxLLMCalls:                  rt.MCP.MaxLLMCallsPerRun,
		ContextMaxTurns:              ch.ContextMaxTurns,
		ContextCompactionEnabled:     ch.ContextCompactEnabled,
		ContextWindowFallbackTokens:  ch.ContextWindowFallbackTokens,
		ContextCompactTriggerPct:     ch.ContextCompactTriggerPercent,
		ContextPreserveRecentTurns:   ch.ContextCompactPreserveRecent,
		ImageMaxBytes:                f.ImageMaxBytes,
		DocMaxBytes:                  f.DocMaxBytes,
		AllowedMIMETypes:             splitList(f.AllowedMimeTypes),
		FullContextMaxBytes:          f.FullContextMaxBytes,
		FullContextMaxTokens:         f.FullContextMaxTokens,
		FullContextPDFMaxPages:       f.FullContextPDFMaxPages,
		ContextTokenBudgetEnabled:    ch.ContextTokenBudgetEnabled,
		ContextArtifactRetentionDays: ch.ContextArtifactRetentionDays,
		RAGAvailable:                 false,
		RAGAvailabilityReason:        "embedding_not_configured",
	}
	if c.h.rag != nil {
		out.RAGAvailable, out.RAGAvailabilityReason = c.h.rag.RAGAvailable(ctx)
	}
	if strings.TrimSpace(out.TaskModel) == "" {
		out.TaskModel = d.TaskModel
	}
	return out
}

func (c convSettings) UserSetting(ctx context.Context, userID uint, key string) (string, bool, error) {
	if c.h.userKV != nil {
		return c.h.userKV.UserSetting(ctx, userID, key)
	}
	if c.h.settings != nil {
		return c.h.settings.UserSetting(ctx, userID, key)
	}
	return "", false, nil
}

// ---- ui components ----

type uiSettings struct{ h *Hub }

func (u uiSettings) UIComponentsEnabled(context.Context) bool {
	if u.h.settings == nil {
		return true
	}
	return u.h.runtime().Chat.UIComponentsEnabled
}
