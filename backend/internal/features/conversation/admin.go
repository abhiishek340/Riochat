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

// adminEvent returns one event in full.
func (s *service) adminEvent(ctx context.Context, id uint) (*AdminEvent, error) {
	var rows []adminEventRow
	if err := s.eventQuery(ctx, AdminEventFilter{}).Select(eventSelect).Where("e.id = ?", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrEventNotFound
	}
	out, err := s.completeEvents(ctx, rows, true)
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// completeEvents adds the message ids and user names to event rows.
func (s *service) completeEvents(ctx context.Context, rows []adminEventRow, bodies bool) ([]AdminEvent, error) {
	out := make([]AdminEvent, 0, len(rows))
	runSet, userSet := map[string]bool{}, map[uint]bool{}
	for _, r := range rows {
		runSet[r.RunID] = true
		if r.RunUserID != 0 {
			userSet[r.RunUserID] = true
		}
	}
	messages := map[string]uint{}
	if len(runSet) > 0 {
		runs := make([]string, 0, len(runSet))
		for id := range runSet {
			runs = append(runs, id)
		}
		var found []struct {
			RunID string
			ID    uint
		}
		err := s.db.WithContext(ctx).Model(&Message{}).Select("run_id, MIN(id) AS id").
			Where("run_id IN ? AND role = ?", runs, roleAssistant).Group("run_id").Scan(&found).Error
		if err != nil {
			return nil, err
		}
		for _, m := range found {
			messages[m.RunID] = m.ID
		}
	}
	ids := make([]uint, 0, len(userSet))
	for id := range userSet {
		ids = append(ids, id)
	}
	names, err := s.userNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		ev := s.fillAdminEvent(r, bodies)
		ev.MessageID = messages[r.RunID]
		if n, ok := names[r.RunUserID]; ok {
			ev.Username, ev.UserDisplayName, ev.UserLabel = n.Username, n.DisplayName, n.label()
		}
		out = append(out, ev)
	}
	return out, nil
}

// ---- export ------------------------------------------------------------------------------

const blockedMarker = "[blocked by content moderation"

func blockedText(eventID string) string {
	if eventID = strings.TrimSpace(eventID); eventID != "" {
		return blockedMarker + "; event " + eventID + "]"
	}
	return blockedMarker + "]"
}

// redactBlocked replaces what a blocked message carried. runEvents maps a run
// id to its moderation event id, used when the message has none of its own.
func redactBlocked(msgs []MessageDTO, runEvents map[string]string) {
	for i := range msgs {
		m := &msgs[i]
		if !strings.EqualFold(strings.TrimSpace(m.Status), msgBlocked) {
			continue
		}
		eventID := ""
		if m.Moderation != nil {
			eventID = m.Moderation.EventID
		}
		if eventID == "" {
			eventID = runEvents[m.RunID]
		}
		switch m.Role {
		case roleUser:
			m.Content = blockedText(eventID)
			m.Attachments = "[]"
		case roleAssistant:
			m.Content = ""
			m.Attachments = "[]"
			m.ProcessTrace = nil
			m.KnowledgeSources = nil
			m.UIState = nil
			if strings.TrimSpace(m.ErrorMessage) == "" {
				m.ErrorMessage = blockedText(eventID)
			}
		}
	}
}

const adminExportNotes = "Admin export redacts blocked content. Originals are only available via content moderation event APIs."

// adminExportDoc builds the export document of one conversation of any user.
func (s *service) adminExportDoc(ctx context.Context, conv *Conversation) (*ExportDoc, error) {
	msgs, err := s.loadConversationMessages(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	dtos, err := s.enrich(ctx, msgs, enrichOpts{userID: conv.UserID, conv: conv})
	if err != nil {
		return nil, err
	}
	runs := []Run{}
	if conv.UserID != 0 {
		if err := s.db.WithContext(ctx).Where("conversation_id = ?", conv.ID).Order("id ASC").Find(&runs).Error; err != nil {
			runs = []Run{}
		}
	}
	events := map[string]string{}
	runDTOs := make([]RunDTO, 0, len(runs))
	for _, r := range runs {
		events[r.RunID] = r.ModerationEventID
		runDTOs = append(runDTOs, toRunDTO(r))
	}
	redactBlocked(dtos, events)
	cd, err := s.conversationDTO(ctx, conv)
	if err != nil {
		return nil, err
	}
	return &ExportDoc{
		Version: 1, ExportScope: "full", ExportedAt: s.now().UTC(), Conversation: *cd, Messages: dtos, Runs: runDTOs,
		TotalMessages: len(dtos), TotalRuns: len(runDTOs), DefaultMessagePublicIDs: latestVisibleBranch(msgs),
		Compatibility: ExportCompat{Format: "riochat.conversation.export", Notes: adminExportNotes},
	}, nil
}

// streamAdminExport writes one line per conversation of every user, then the
// manifest. write returns false once the client is gone.
func (s *service) streamAdminExport(ctx context.Context, write func(line []byte) bool) {
	man := exportManifest{Type: "export_manifest", Complete: true}
	var last uint
	for {
		if ctx.Err() != nil {
			return
		}
		var batch []Conversation
		if err := s.db.WithContext(ctx).Where("id > ?", last).Order("id ASC").Limit(exportBatch).Find(&batch).Error; err != nil {
			if ctx.Err() != nil {
				return
			}
			man.Complete, man.Error = false, "failed to list conversations"
			break
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			conv := batch[i]
			last = conv.ID
			doc, err := s.adminExportDoc(ctx, &conv)
			var raw []byte
			if err == nil {
				raw, err = json.Marshal(doc)
			}
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				man.Failed++
				man.FailedIDs = append(man.FailedIDs, conv.ID)
				s.log.Warn("conversation_admin_export_item_failed", logErr(err))
				continue
			}
			if !write(raw) {
				return
			}
			man.Exported++
		}
	}
	raw, _ := json.Marshal(man)
	write(raw)
}

// ---- handlers ------------------------------------------------------------------------------

func (a *api) adminEventList(c *gin.Context) {
	q := c.Request.URL.Query()
	f := AdminEventFilter{
		Query: q.Get("query"), EventScope: q.Get("event_scope"), EventType: q.Get("event_type"),
		Status: q.Get("status"), Sort: q.Get("sort"),
	}
	positive := func(name string) (uint, bool) {
		raw := strings.TrimSpace(q.Get(name))
		if raw == "" {
			return 0, true
		}
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || n == 0 || uint64(uint(n)) != n {
			httpx.InvalidQuery(c, name)
			return 0, false
		}
		return uint(n), true
	}
	moment := func(name string) (*time.Time, bool) {
		raw := strings.TrimSpace(q.Get(name))
		if raw == "" {
			return nil, true
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.InvalidQuery(c, name)
			return nil, false
		}
		return &t, true
	}
	var ok bool
	if f.UserID, ok = positive("user_id"); !ok {
		return
	}
	if f.ConversationID, ok = positive("conversation_id"); !ok {
		return
	}
	if f.From, ok = moment("created_from"); !ok {
		return
	}
	if f.To, ok = moment("created_to"); !ok {
		return
	}
	list, total, err := a.s.adminEvents(c.Request.Context(), f, pagination.FromRequest(c.Request))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.Paged(c, total, list)
}

func (a *api) adminEventGet(c *gin.Context) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 || uint64(uint(n)) != n {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidEventID)
		return
	}
	ev, err := a.s.adminEvent(c.Request.Context(), uint(n))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, ev)
}

func (a *api) adminExport(c *gin.Context) {
	a.s.auditEntry(c, uid(c), "admin_export_conversations", "conversation", "", nil)
	h := c.Writer.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Content-Disposition", `attachment; filename="conversations-export-`+a.s.now().UTC().Format("20060102-150405")+`.jsonl"`)
	h.Set("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	w := &lineWriter{c: c}
	a.s.streamAdminExport(c.Request.Context(), func(line []byte) bool { return w.write(line) })
}
