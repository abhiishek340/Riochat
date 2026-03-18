// Package integration connects the feature areas to each other. Areas never
// import one another; each declares the small interfaces it needs and this
// package supplies the implementations from the other areas' exported
// services. The registry (internal/features) asks a Hub for one mount function
// per area; the Hub remembers the services the areas publish so that later
// adapters can reach them.
//
// Adapters resolve the collaborating service lazily, on first use, so the
// order in which areas are mounted never decides what works.
package integration

import (
	"sync"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/contentmoderation"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/knowledgebase"
	"github.com/riochat/riochat/backend/internal/features/mcp"
	"github.com/riochat/riochat/backend/internal/features/memory"
	"github.com/riochat/riochat/backend/internal/features/objectstore"
	"github.com/riochat/riochat/backend/internal/features/personalprovider"
	"github.com/riochat/riochat/backend/internal/features/ragindex"
	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/features/skill"
	"github.com/riochat/riochat/backend/internal/features/uicomponent"
	"github.com/riochat/riochat/backend/internal/features/user"
)

// Hub holds the services published by the mounted areas. A Hub is used by one
// application instance; build a fresh one per instance.
type Hub struct {
	settings *settings.Service
	billing  *billing.Service
	channel  *channel.Service
	personal *personalprovider.Service
	kb       *knowledgebase.Service
	memory   *memory.Service
	mcp      *mcp.Service
	skills   *skill.Service
	ui       *uicomponent.Service
	mod      *contentmoderation.Service
	files    *conversation.Files
	rag      *ragindex.Service

	// modBridge is how the moderation engine reaches runs, messages, files and
	// the live streams of the conversation area.
	modBridge *conversation.ModerationBridge
	storeMu   sync.Mutex
	store     *objectstore.Manager

	extras sync.Map // run id -> *runExtras

	accounts *user.Accounts
	userKV   *user.SettingsCache
	rec      *audit.Recorder
}

// New returns an empty Hub.
func New() *Hub { return &Hub{} }

// recorder returns the shared audit recorder, creating it on first use.
func (h *Hub) recorder(d deps.Deps) *audit.Recorder {
	if h.rec == nil {
		h.rec = audit.NewRecorder(d.DB, d.Logger, d.Clock)
	}
	return h.rec
}

// userAccounts returns the shared account reader.
func (h *Hub) userAccounts(d deps.Deps) *user.Accounts {
	if h.accounts == nil {
		h.accounts = user.NewAccounts(d.DB, d.Clock, nil)
	}
	return h.accounts
}

// userSettings returns the per-user setting cache (shared with the user area
// through the application cache).
func (h *Hub) userSettings(d deps.Deps) *user.SettingsCache {
	if h.userKV == nil {
		h.userKV = user.NewSettingsCache(d.DB, d.Cache, d.Logger)
	}
	return h.userKV
}

// putExtras records billing facts for a run until the billing adapter settles it.
func (h *Hub) putExtras(runID string, e *runExtras) {
	if runID != "" && e != nil {
		h.extras.Store(runID, e)
	}
}

// takeExtras removes and returns the facts of a run (never nil).
func (h *Hub) takeExtras(runID string) runExtras {
	if v, ok := h.extras.LoadAndDelete(runID); ok {
		return *v.(*runExtras)
	}
	return runExtras{}
}
