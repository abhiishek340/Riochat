package conversation

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// service holds everything the area's logic needs.
type service struct {
	db     *gorm.DB
	cfg    *config.Holder
	log    *logx.Logger
	cache  cache.Cache
	clock  clock.Clock
	lc     *lifecycle.Manager
	ports  Ports
	hub    *runHub
	timing timings

	uploadGate keyedGate
}

func newService(d deps.Deps, ports Ports) *service {
	s := &service{
		db: d.DB, cfg: d.Config, log: d.Logger, cache: d.Cache, clock: d.Clock, lc: d.Lifecycle,
		ports: ports.withDefaults(d.Logger), timing: defaultTimings(),
	}
	s.hub = newRunHub(s)
	return s
}

func (s *service) now() time.Time { return s.clock.Now() }

func (s *service) settings(ctx context.Context) RuntimeSettings {
	rs := s.ports.Settings.Runtime(ctx)
	d := DefaultRuntimeSettings()
	if rs.MaxUploadBytes <= 0 {
		rs.MaxUploadBytes = d.MaxUploadBytes
	}
	if rs.MaxMessageFiles <= 0 {
		rs.MaxMessageFiles = d.MaxMessageFiles
	}
	if rs.MaxSelectedTools <= 0 {
		rs.MaxSelectedTools = d.MaxSelectedTools
	}
	if rs.MaxLLMCalls <= 0 {
		rs.MaxLLMCalls = d.MaxLLMCalls
	}
	if rs.MaxLLMCalls < 2 {
		rs.MaxLLMCalls = 2
	}
	if rs.MaxLLMCalls > 32 {
		rs.MaxLLMCalls = 32
	}
	if rs.ContextMaxTurns <= 0 {
		rs.ContextMaxTurns = d.ContextMaxTurns
	}
	if rs.ContextWindowFallbackTokens <= 0 {
		rs.ContextWindowFallbackTokens = d.ContextWindowFallbackTokens
	}
	if rs.ContextCompactTriggerPct <= 0 {
		rs.ContextCompactTriggerPct = d.ContextCompactTriggerPct
	}
	if rs.ContextPreserveRecentTurns <= 0 {
		rs.ContextPreserveRecentTurns = d.ContextPreserveRecentTurns
	}
	if rs.FullContextMaxBytes <= 0 {
		rs.FullContextMaxBytes = d.FullContextMaxBytes
	}
	if rs.FullContextMaxTokens <= 0 {
		rs.FullContextMaxTokens = d.FullContextMaxTokens
	}
	if rs.FullContextPDFMaxPages <= 0 {
		rs.FullContextPDFMaxPages = d.FullContextPDFMaxPages
	}
	if rs.TaskModel == "" {
		rs.TaskModel = "follow"
	}
	return rs
}

// userFlag reads a boolean per-user setting where only "false" disables.
func (s *service) userFlag(ctx context.Context, userID uint, key string) bool {
	v, ok, err := s.ports.Settings.UserSetting(ctx, userID, key)
	if err != nil || !ok {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(v), "false")
}

// getConversation loads an owned, non-deleted conversation by public id.
func (s *service) getConversation(ctx context.Context, userID uint, publicID string) (*Conversation, error) {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return nil, ErrConversationInvalidID
	}
	var c Conversation
	err := s.db.WithContext(ctx).Where("public_id = ? AND user_id = ?", publicID, userID).First(&c).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return &c, nil
}

// getMessage loads an owned message by public id.
func (s *service) getMessage(ctx context.Context, userID uint, publicID string) (*Message, error) {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return nil, ErrMessageInvalidID
	}
	var m Message
	err := s.db.WithContext(ctx).Where("public_id = ? AND user_id = ?", publicID, userID).First(&m).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrMessageNotFound
		}
		return nil, err
	}
	// A message of a deleted conversation is unreachable.
	var n int64
	if err := s.db.WithContext(ctx).Model(&Conversation{}).Where("id = ?", m.ConversationID).Count(&n).Error; err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrMessageNotFound
	}
	return &m, nil
}

// lockRows applies a row lock on PostgreSQL; SQLite serialises writers anyway.
func lockRows(tx *gorm.DB) *gorm.DB {
	if database.Dialect(tx) == "postgres" {
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return tx
}

// estimateTokens is a rough token count (about three characters per token).
func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	return (n + 2) / 3
}

// escapeLike escapes the LIKE wildcards with '!' (use together with ESCAPE '!').
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	return strings.ReplaceAll(s, "_", "!_")
}

// inferProvider derives the provider label from a model name.
func inferProvider(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "gpt-"):
		return "openai"
	case strings.HasPrefix(m, "claude-"):
		return "anthropic"
	default:
		return "internal"
	}
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func dedupeUints(in []uint, dropZero bool) []uint {
	seen := map[uint]bool{}
	out := make([]uint, 0, len(in))
	for _, v := range in {
		if dropZero && v == 0 {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func chunk[T any](in []T, size int) [][]T {
	var out [][]T
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}
