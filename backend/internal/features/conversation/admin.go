package conversation

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// This file is the administrator's window onto the run trace events and the
// bulk export of every conversation. The routes are not behind the multi-user
// capability: they work in every deployment.

var (
	ErrInvalidEventID = apperr.Define("conversation_event.invalid_id", "invalid conversation event id").WithStatus(http.StatusBadRequest)
	ErrEventNotFound  = apperr.Define("conversation_event.not_found", "conversation event not found").WithStatus(http.StatusNotFound)
)

// maxInlineEventBytes is the largest JSON body the detail view returns inline.
const maxInlineEventBytes = 1 << 20

// AdminEvent is one run trace event as the administration console shows it.
type AdminEvent struct {
	ID                uint       `json:"id"`
	MessageID         uint       `json:"messageID"`
	ConversationID    uint       `json:"conversationID"`
	UserID            uint       `json:"userID"`
	Username          string     `json:"username"`
	UserDisplayName   string     `json:"userDisplayName"`
	UserLabel         string     `json:"userLabel"`
	RunID             string     `json:"runID"`
	ProviderProtocol  string     `json:"providerProtocol"`
	UpstreamName      string     `json:"upstreamName"`
	PlatformModelName string     `json:"platformModelName"`
	RoutedBindingCode string     `json:"routedBindingCode"`
	UpstreamModelName string     `json:"upstreamModelName"`
	EventScope        string     `json:"eventScope"`
	EventID           string     `json:"eventID"`
	EventType         string     `json:"eventType"`
	Phase             string     `json:"phase"`
	Stage             string     `json:"stage"`
	RoundID           string     `json:"roundID"`
	ParentEventID     string     `json:"parentEventID"`
	Status            string     `json:"status"`
	Title             string     `json:"title"`
	Summary           string     `json:"summary"`
	ContentMarkdown   string     `json:"contentMarkdown"`
	PayloadJSON       string     `json:"payloadJSON"`
	PayloadSizeBytes  int64      `json:"payloadSizeBytes"`
	PayloadOmitted    bool       `json:"payloadOmitted"`
	Seq               int        `json:"seq"`
	ToolCallID        string     `json:"toolCallID"`
	ToolName          string     `json:"toolName"`
	LatencyMS         int64      `json:"latencyMS"`
	InputJSON         string     `json:"inputJSON"`
	InputSizeBytes    int64      `json:"inputSizeBytes"`
	InputOmitted      bool       `json:"inputOmitted"`
	OutputJSON        string     `json:"outputJSON"`
	OutputSizeBytes   int64      `json:"outputSizeBytes"`
	OutputOmitted     bool       `json:"outputOmitted"`
	ErrorJSON         string     `json:"errorJSON"`
	ErrorSizeBytes    int64      `json:"errorSizeBytes"`
	ErrorOmitted      bool       `json:"errorOmitted"`
	StartedAt         time.Time  `json:"startedAt"`
	EndedAt           *time.Time `json:"endedAt"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// adminEventRow is a run event joined with the facts of its run.
type adminEventRow struct {
	RunEvent
	PayloadBytes      int64
	RunUserID         uint
	RunConversationID uint
	RunProtocol       string
	RunUpstreamName   string
	RunPlatformModel  string
	RunBindingCode    string
	RunUpstreamModel  string
}

// userName is the current name of an account.
type userName struct {
	ID          uint
	Username    string
	DisplayName string
}

func (u userName) label() string {
	if n := strings.TrimSpace(u.DisplayName); n != "" {
		return n
	}
	if n := strings.TrimSpace(u.Username); n != "" {
		return n
	}
	return strconv.FormatUint(uint64(u.ID), 10)
}

// userNames reads the names of live accounts. Without the identity table (the
// area running alone) nobody is known.
func (s *service) userNames(ctx context.Context, ids []uint) (map[uint]userName, error) {
	out := map[uint]userName{}
	if len(ids) == 0 || !s.db.WithContext(ctx).Migrator().HasTable("identity_users") {
		return out, nil
	}
	var rows []userName
	err := s.db.WithContext(ctx).Table("identity_users").Select("id, username, display_name").
		Where("id IN ? AND deleted_at IS NULL", ids).Scan(&rows).Error
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, err
}

func (s *service) fillAdminEvent(row adminEventRow, withBodies bool) AdminEvent {
	e := row.RunEvent
	started := e.CreatedAt
	if e.StartedAt != nil {
		started = *e.StartedAt
	}
	ev := AdminEvent{
		ID: e.ID, ConversationID: row.RunConversationID, UserID: row.RunUserID, RunID: e.RunID,
		ProviderProtocol: row.RunProtocol, UpstreamName: row.RunUpstreamName, PlatformModelName: row.RunPlatformModel,
		RoutedBindingCode: row.RunBindingCode, UpstreamModelName: row.RunUpstreamModel,
		EventScope: e.EventScope, EventID: e.EventID, EventType: e.EventType, Phase: e.Phase, Stage: e.Stage,
		RoundID: e.RoundID, ParentEventID: e.ParentEventID, Status: e.Status, Title: e.Title, Summary: e.Summary,
		Seq: e.Seq, ToolCallID: e.ToolCallID, ToolName: e.ToolName, LatencyMS: e.LatencyMS,
		StartedAt: started, EndedAt: e.EndedAt, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	size := func(text string) (int64, bool) {
		n := int64(len(text))
		return n, n > maxInlineEventBytes
	}
	if !withBodies {
		ev.PayloadSizeBytes = row.PayloadBytes
		ev.PayloadOmitted = row.PayloadBytes > maxInlineEventBytes
		return ev
	}
	ev.ContentMarkdown = e.ContentMarkdown
	body := func(text string, sz *int64, omitted *bool, dst *string) {
		*sz, *omitted = size(text)
		if !*omitted {
			*dst = text
		}
	}
	body(e.PayloadJSON, &ev.PayloadSizeBytes, &ev.PayloadOmitted, &ev.PayloadJSON)
	body(e.InputJSON, &ev.InputSizeBytes, &ev.InputOmitted, &ev.InputJSON)
	body(e.OutputJSON, &ev.OutputSizeBytes, &ev.OutputOmitted, &ev.OutputJSON)
	body(e.ErrorJSON, &ev.ErrorSizeBytes, &ev.ErrorOmitted, &ev.ErrorJSON)
	return ev
}

// AdminEventFilter selects run events. The zero value matches everything.
type AdminEventFilter struct {
	Query          string
	EventScope     string
	EventType      string
	Status         string
	UserID         uint
	ConversationID uint
	From, To       *time.Time
	Sort           string
}

const eventSelect = `e.*, r.user_id AS run_user_id, r.conversation_id AS run_conversation_id,
	r.provider_protocol AS run_protocol, r.upstream_name AS run_upstream_name, r.platform_model_name AS run_platform_model,
	r.routed_binding_code AS run_binding_code, r.upstream_model_name AS run_upstream_model`

func (s *service) eventQuery(ctx context.Context, f AdminEventFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Table("chat_run_events AS e").
		Joins("LEFT JOIN chat_runs r ON r.run_id = e.run_id AND r.deleted_at IS NULL").
		Where("e.deleted_at IS NULL")
	if text := strings.ToLower(strings.TrimSpace(f.Query)); text != "" {
		pat := "%" + text + "%"
		q = q.Where(`(LOWER(e.run_id) LIKE ? OR LOWER(e.event_id) LIKE ? OR LOWER(e.event_type) LIKE ? OR LOWER(e.phase) LIKE ?
			OR LOWER(e.stage) LIKE ? OR LOWER(e.title) LIKE ? OR LOWER(e.summary) LIKE ? OR LOWER(e.tool_name) LIKE ?
			OR e.run_id IN (SELECT run_id FROM chat_runs WHERE request_id = ?))`,
			pat, pat, pat, pat, pat, pat, pat, pat, strings.TrimSpace(f.Query))
	}
	if v := strings.TrimSpace(f.EventScope); v != "" {
		q = q.Where("e.event_scope = ?", v)
	}
	if v := strings.TrimSpace(f.EventType); v != "" {
		q = q.Where("e.event_type = ?", v)
	}
	if v := strings.TrimSpace(f.Status); v != "" {
		q = q.Where("e.status = ?", v)
	}
	if f.UserID > 0 {
		q = q.Where("r.user_id = ?", f.UserID)
	}
	if f.ConversationID > 0 {
		q = q.Where("r.conversation_id = ?", f.ConversationID)
	}
	if f.From != nil {
		q = q.Where("e.created_at >= ?", f.From.UTC())
	}
	if f.To != nil {
		q = q.Where("e.created_at <= ?", f.To.UTC())
	}
	return q
}

func eventOrder(sort string) string {
	switch strings.TrimSpace(sort) {
	case "created_asc":
		return "e.created_at ASC, e.id ASC"
	case "latency_desc":
		return "e.latency_ms DESC, e.id DESC"
	case "seq_asc":
		return "e.run_id ASC, e.seq ASC, e.id ASC"
	}
	return "e.created_at DESC, e.id DESC"
}

func (s *service) payloadSizeExpr() string {
	if database.Dialect(s.db) == "postgres" {
		return "OCTET_LENGTH(e.payload_json)"
	}
	return "LENGTH(CAST(e.payload_json AS BLOB))"
}

// adminEvents lists events without their heavy bodies.
func (s *service) adminEvents(ctx context.Context, f AdminEventFilter, p pagination.Page) ([]AdminEvent, int64, error) {
	var total int64
	if err := s.eventQuery(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []adminEventRow
	sel := `e.id, e.created_at, e.updated_at, e.run_id, e.event_scope, e.event_id, e.parent_event_id, e.event_type, e.phase,
		e.stage, e.round_id, e.title, e.summary, e.status, e.seq, e.tool_call_id, e.tool_name, e.latency_ms, e.started_at, e.ended_at,
		COALESCE(` + s.payloadSizeExpr() + `, 0) AS payload_bytes,
		r.user_id AS run_user_id, r.conversation_id AS run_conversation_id, r.provider_protocol AS run_protocol,
		r.upstream_name AS run_upstream_name, r.platform_model_name AS run_platform_model,
		r.routed_binding_code AS run_binding_code, r.upstream_model_name AS run_upstream_model`
	if err := s.eventQuery(ctx, f).Select(sel).Order(eventOrder(f.Sort)).Scopes(pagination.Scope(p)).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	out, err := s.completeEvents(ctx, rows, false)
	return out, total, err
}
