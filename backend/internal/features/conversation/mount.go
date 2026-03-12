// Package conversation implements chat conversations: organising them into
// projects, message trees and branches, generation runs and their resumable
// streams, media generation, temporary chat, sharing, forking, exporting, UI
// state, feedback, traces and the chat-file endpoints.
package conversation

import (
	"context"
	"errors"
	"net/http"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/capability"
)

// Mount registers the area with default collaborators (see Ports).
func Mount(d deps.Deps) error { return mount(d, Ports{}) }

// MountWith returns a Mount function that uses the given collaborators. The
// integrator wires the real model router, billing and moderation here.
func MountWith(p Ports) func(deps.Deps) error {
	return func(d deps.Deps) error { return mount(d, p) }
}

func mount(d deps.Deps, ports Ports) error {
	if d.DB == nil {
		return errors.New("conversation: database is required")
	}
	s := newService(d, ports)
	a := &api{s: s}
	if ports.OnFiles != nil {
		ports.OnFiles(&Files{s: s})
	}
	if ports.OnModeration != nil {
		ports.OnModeration(&ModerationBridge{s: s})
	}
	registerRoutes(d, a)
	d.Lifecycle.OnStart("conversation-recover-runs", func(ctx context.Context) error {
		s.recoverOrphans(ctx)
		return nil
	})
	return nil
}

func registerRoutes(d deps.Deps, a *api) {
	au := d.Routes.Auth

	// Conversations.
	au.POST("/conversations", a.createConversation)
	au.GET("/conversations", a.listConversations)
	au.GET("/conversations/search", a.searchConversations)
	au.GET("/conversations/default-model-candidate", a.defaultModelCandidate)
	au.GET("/conversations/export", a.exportAll)
	au.POST("/conversations/project", a.batchSetProject)
	au.GET("/conversations/:id", a.getConversation)
	au.PATCH("/conversations/:id/title", a.rename)
	au.POST("/conversations/:id/title/regenerate", a.regenerateTitle)
	au.PATCH("/conversations/:id/labels", a.updateLabels)
	au.PATCH("/conversations/:id/star", a.star)
	au.PATCH("/conversations/:id/archive", a.archive)
	au.DELETE("/conversations/:id", a.deleteConversation)
	au.PATCH("/conversations/:id/project", a.setProject)
	au.GET("/conversations/:id/export", a.exportConversation)

	// Messages.
	au.GET("/conversations/:id/messages", a.listMessages)
	au.GET("/conversations/:id/messages/preview", a.previewMessages)
	au.POST("/conversations/:id/messages", a.sendMessage)
	au.POST("/conversations/:id/messages/stream", a.streamMessage)
	au.DELETE("/conversations/:id/messages/:message_id", a.deleteMessage)
	au.POST("/conversations/:id/messages/:message_id/fork", a.fork)
	au.PUT("/conversations/:id/messages/:message_id/ui-state", a.putUIState)
	au.PATCH("/messages/:id", a.editMessage)
	au.GET("/messages/:id/trace", a.messageTrace)
	au.PUT("/messages/:id/feedback", a.feedback)
	au.GET("/context-artifacts/:id", a.artifact)

	// Runs.
	au.GET("/conversations/:id/runs", a.listRuns)
	au.POST("/conversation-runs/statuses", a.runStatuses)
	au.GET("/conversation-runs/stream", a.userRunsStream)
	au.GET("/conversation-runs/:run_id/stream", a.resume)
	au.POST("/conversation-runs/:run_id/cancel", a.cancel)
	au.GET("/conversation-runs/:run_id/tool-calls/:tool_call_id", a.toolCall)

	// Media generation.
	au.POST("/conversations/:id/media/images/generations/stream", a.media(taskImageGeneration))
	au.POST("/conversations/:id/media/images/edits/stream", a.media(taskImageEdit))
	au.POST("/conversations/:id/media/videos/generations/stream", a.media(taskVideoGeneration))
	au.POST("/conversations/:id/media/videos/extensions/stream", a.media(taskVideoExtension))

	// Temporary chat.
	au.POST("/temporary-chat/messages/stream", a.temporaryStream)

	// Projects.
	au.GET("/conversation-projects", a.listProjects)
	au.POST("/conversation-projects", a.createProject)
	au.POST("/conversation-projects/reorder", a.reorderProjects)
	au.PATCH("/conversation-projects/:id", a.updateProject)
	au.DELETE("/conversation-projects/:id", a.deleteProject)

	// Sharing, hidden behind the sharing flag.
	g := d.Gated(capability.Sharing)
	g.Auth.GET("/conversations/:id/share", a.getShare)
	g.Auth.POST("/conversations/:id/share", a.createShare(false))
	g.Auth.POST("/conversations/:id/share/regenerate", a.createShare(true))
	g.Auth.DELETE("/conversations/:id/share", a.revokeShare)
	g.Auth.POST("/conversations/shares/revoke", a.revokeShares)
	g.Public.GET("/shared-conversations/:share_id", a.readShared)
	g.Public.GET("/shared-conversations/:share_id/files/:file_id/content", a.sharedFile)
	g.Auth.POST("/shared-conversations/:share_id/clone", a.cloneShared)

	d.Routes.Admin.GET("/admin/conversation-events", a.adminEventList)
	d.Routes.Admin.GET("/admin/conversation-events/:id", a.adminEventGet)
	d.Routes.Admin.GET("/admin/conversations/export", a.adminExport)

	// Chat files.
	au.POST("/files", a.uploadFile)
	au.GET("/files", a.listFiles)
	au.POST("/files/processing/statuses", a.fileStatuses)
	au.POST("/files/embeddings", a.submitEmbeddings)
	au.GET("/files/:file_id/content", a.fileContent)
	au.GET("/files/:file_id/extract", a.fileExtract)
	au.GET("/files/:file_id/processing", a.fileProcessing)
	au.PATCH("/files/:file_id", a.patchFile)
	au.DELETE("/files/:file_id", a.deleteFile)
	au.GET("/runtime/chat-file-policy", a.filePolicy)
}

// recoverOrphans closes runs left "running" by a crashed process: when no lease
// exists for a running run its pending messages become interrupted errors.
func (s *service) recoverOrphans(ctx context.Context) {
	var runs []Run
	if err := s.db.WithContext(ctx).Where("status = ?", runRunning).Find(&runs).Error; err != nil {
		s.log.Warn("conversation_recover_failed", logErr(err))
		return
	}
	for _, r := range runs {
		if s.hub.leaseAlive(ctx, r.RunID) {
			continue
		}
		s.markUnknownRunInterrupted(ctx, r.UserID, r.RunID)
	}
}

func asMaxBytes(err error, target **http.MaxBytesError) bool { return errors.As(err, target) }
