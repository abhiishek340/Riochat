package integration

import (
	"context"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/features/auth"
	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/contentmoderation"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/knowledgebase"
	"github.com/riochat/riochat/backend/internal/features/settings"
)

// Each area describes an audit record in its own words; these adapters turn
// them into rows of the audit area's recorder. Writing is best effort.

type authAudit struct{ rec *audit.Recorder }

func (a authAudit) WriteAudit(ctx context.Context, e auth.AuditEntry) {
	a.rec.Record(ctx, audit.Entry{
		RequestID: e.RequestID, ActorUserID: e.UserID, Action: e.Action, Resource: e.ResourceType, ResourceID: e.ResourceID,
		IP: e.ClientIP, UserAgent: e.UserAgent, Detail: e.Detail,
	})
}

type billingAudit struct{ rec *audit.Recorder }

func (a billingAudit) Record(ctx context.Context, e billing.AuditEntry) {
	a.rec.Record(ctx, audit.Entry{
		RequestID: e.RequestID, ActorUserID: e.ActorUserID, Action: e.Action, Resource: e.Resource, ResourceID: e.ResourceID,
		IP: e.ClientIP, UserAgent: e.UserAgent, Detail: e.Detail,
	})
}

type convAudit struct{ rec *audit.Recorder }

func (a convAudit) Record(ctx context.Context, e conversation.AuditEntry) error {
	a.rec.Record(ctx, audit.Entry{
		RequestID: e.RequestID, ActorUserID: e.UserID, Action: e.Action, Resource: e.Resource, ResourceID: e.ResourceID,
		IP: e.ClientIP, UserAgent: e.UserAgent, Detail: e.Detail,
	})
	return nil
}

type kbAudit struct{ rec *audit.Recorder }

func (a kbAudit) Record(ctx context.Context, e knowledgebase.AuditEntry) {
	a.rec.Record(ctx, audit.Entry{
		RequestID: e.RequestID, ActorUserID: e.ActorUserID, Action: e.Action, Resource: e.Resource, ResourceID: e.ResourceID,
		IP: e.ClientIP, UserAgent: e.UserAgent, Detail: e.Detail,
	})
}

type moderationAudit struct{ rec *audit.Recorder }

func (a moderationAudit) Record(ctx context.Context, e contentmoderation.AuditEntry) error {
	a.rec.Record(ctx, audit.Entry{
		RequestID: e.RequestID, ActorUserID: e.ActorID, Action: e.Action, Resource: e.ResourceType, ResourceID: e.ResourceID,
		IP: e.ClientIP, UserAgent: e.UserAgent, Detail: e.Detail,
	})
	return nil
}

type settingsAudit struct{ rec *audit.Recorder }

func (a settingsAudit) Record(ctx context.Context, e settings.AuditEntry) {
	a.rec.Record(ctx, audit.Entry{
		RequestID: e.RequestID, ActorUserID: e.ActorUserID, Action: e.Action, Resource: e.Resource, ResourceID: e.ResourceID,
		IP: e.ClientIP, UserAgent: e.UserAgent, Detail: e.Detail,
	})
}
