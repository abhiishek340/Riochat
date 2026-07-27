package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

const (
	maxServerNameChars = 128
	maxToolNameChars   = 160
	maxToolDescChars   = 4096
	syncFailureSummary = "Error while syncing MCP tools"
	redactedMarker     = "********"
)

// Service is the area's business logic. It is safe for concurrent use and is
// what the chat runtime calls to resolve and execute tools.
type Service struct {
	db       *gorm.DB
	secrets  *secretbox.KeyRing
	clock    clock.Clock
	settings Settings
	billing  BillingModes
	links    ProjectLinks
	client   Client
	signKey  func() string
	log      *logx.Logger
	limiter  *callLimiter
}

// NewService builds the service from the platform dependencies and ports.
func NewService(d deps.Deps, p Ports) *Service {
	s := &Service{
		db: d.DB, secrets: d.Secrets, clock: d.Clock, billing: p.BillingModes, client: p.Client,
		settings: p.Settings, links: p.ProjectLinks, log: d.Logger, limiter: &callLimiter{},
	}
	if s.settings == nil {
		s.settings = staticSettings{s: DefaultRuntimeSettings()}
	}
	if s.links == nil {
		s.links = sqlProjectLinks{}
	}
	if s.client == nil {
		s.client = NewHTTPClient()
	}
	if d.Config != nil {
		s.signKey = func() string { return d.Config.Load().Security.MCPUserContextSecret }
	}
	return s
}

func (s *Service) runtime(ctx context.Context) RuntimeSettings {
	return s.settings.MCPSettings(ctx).normalized()
}

// ---------------------------------------------------------------- servers

// ServerInput is the body of create and update.
type ServerInput struct {
	Name        string `json:"name"`
	BaseURL     string `json:"baseURL"`
	AuthToken   string `json:"authToken"`
	HeadersJSON string `json:"headersJSON"`
	Status      string `json:"status"`
}

// ServerRow is a server with its live derived values.
type ServerRow struct {
	Server
	ActiveToolCount                      int
	RequiresToolMetadataSyncConfirmation bool
}

type cleanServer struct {
	name, baseURL, headers, status, token string
}

func validateServerInput(in ServerInput) (cleanServer, error) {
	var c cleanServer
	c.name = strings.TrimSpace(in.Name)
	if c.name == "" || utf8.RuneCountInString(c.name) > maxServerNameChars {
		return c, ErrInvalidServerName
	}
	u, err := checkEndpoint(in.BaseURL)
	if err != nil {
		return c, ErrInvalidServerURL.WithCause(err)
	}
	c.baseURL = u
	c.status = strings.TrimSpace(in.Status)
	if c.status == "" {
		c.status = StatusActive
	}
	if c.status != StatusActive && c.status != StatusInactive {
		return c, ErrInvalidServerState
	}
	c.headers = strings.TrimSpace(in.HeadersJSON)
	if c.headers == "" {
		c.headers = "{}"
	}
	if !flatStringObject(c.headers) {
		return c, ErrInvalidHeaders
	}
	c.token = strings.TrimSpace(in.AuthToken)
	return c, nil
}

// flatStringObject reports whether text is a JSON object whose values are all
// strings (a null value is not a string).
func flatStringObject(text string) bool {
	var h map[string]any
	if err := json.Unmarshal([]byte(text), &h); err != nil || h == nil {
		return false
	}
	for _, v := range h {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

func (s *Service) encryptToken(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	enc, err := s.secrets.Encrypt(token)
	if err != nil {
		return "", fmt.Errorf("encrypt mcp token: %w", err)
	}
	return enc, nil
}

// CreateServer registers a server; the 101st is refused.
func (s *Service) CreateServer(ctx context.Context, in ServerInput) (*ServerRow, error) {
	c, err := validateServerInput(in)
	if err != nil {
		return nil, err
	}
	enc, err := s.encryptToken(c.token)
	if err != nil {
		return nil, err
	}
	var created Server
	err = database.WithAdvisoryLock(ctx, s.db, "mcp_servers_create", func(base *gorm.DB) error {
		return base.Transaction(func(tx *gorm.DB) error {
			var count int64
			if err := tx.Model(&Server{}).Count(&count).Error; err != nil {
				return database.Translate(err)
			}
			if count >= maxServers {
				return ErrServerLimit
			}
			var maxOrder int
			if err := tx.Model(&Server{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
				return database.Translate(err)
			}
			created = Server{
				Name: c.name, BaseURL: c.baseURL, AuthTokenEnc: enc, HeadersJSON: c.headers,
				Status: c.status, SortOrder: maxOrder + sortStep,
			}
			return database.Translate(tx.Create(&created).Error)
		})
	})
	if err != nil {
		return nil, err
	}
	return &ServerRow{Server: created}, nil
}

// UpdateServer replaces name, URL, headers and status wholesale; the token is
// replaced only when a non-empty one is supplied.
func (s *Service) UpdateServer(ctx context.Context, id uint, in ServerInput) (*ServerRow, error) {
	c, err := validateServerInput(in)
	if err != nil {
		return nil, err
	}
	enc, err := s.encryptToken(c.token)
	if err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	var srv Server
	if err := db.First(&srv, id).Error; err != nil {
		return nil, database.Translate(err)
	}
	changes := map[string]any{
		"name": c.name, "base_url": c.baseURL, "headers_json": c.headers, "status": c.status,
	}
	if enc != "" {
		changes["auth_token_enc"] = enc
	}
	if err := db.Model(&Server{}).Where("id = ?", id).Updates(changes).Error; err != nil {
		return nil, database.Translate(err)
	}
	return s.serverRow(ctx, id)
}

func (s *Service) serverRow(ctx context.Context, id uint) (*ServerRow, error) {
	var srv Server
	if err := s.db.WithContext(ctx).First(&srv, id).Error; err != nil {
		return nil, database.Translate(err)
	}
	rows, err := s.decorate(ctx, []Server{srv})
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// ListServers lists all servers in display order with live counts.
func (s *Service) ListServers(ctx context.Context) ([]ServerRow, error) {
	var servers []Server
	if err := s.db.WithContext(ctx).Order("sort_order ASC, id ASC").Find(&servers).Error; err != nil {
		return nil, database.Translate(err)
	}
	return s.decorate(ctx, servers)
}

func (s *Service) decorate(ctx context.Context, servers []Server) ([]ServerRow, error) {
	rows := make([]ServerRow, len(servers))
	if len(servers) == 0 {
		return rows, nil
	}
	ids := make([]uint, len(servers))
	for i := range servers {
		ids[i] = servers[i].ID
	}
	db := s.db.WithContext(ctx)
	var counts []struct {
		ServerID uint
		N        int
	}
	if err := db.Model(&Tool{}).Select("server_id, COUNT(*) AS n").
		Where("server_id IN ? AND status = ?", ids, StatusActive).Group("server_id").Scan(&counts).Error; err != nil {
		return nil, database.Translate(err)
	}
	active := make(map[uint]int, len(counts))
	for _, c := range counts {
		active[c.ServerID] = c.N
	}
	var flagged []uint
	if err := db.Model(&Tool{}).Distinct().
		Where("server_id IN ? AND (metadata_customized IS NULL OR metadata_customized = ?)", ids, true).
		Pluck("server_id", &flagged).Error; err != nil {
		return nil, database.Translate(err)
	}
	needs := make(map[uint]bool, len(flagged))
	for _, id := range flagged {
		needs[id] = true
	}
	for i := range servers {
		rows[i] = ServerRow{Server: servers[i], ActiveToolCount: active[servers[i].ID], RequiresToolMetadataSyncConfirmation: needs[servers[i].ID]}
	}
	return rows, nil
}

// DeleteServer removes a server, its tools and the project-default links to
// them in one transaction. Tool-call history is not touched.
func (s *Service) DeleteServer(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var srv Server
		if err := tx.First(&srv, id).Error; err != nil {
			// Deleting what is already gone succeeds, so a repeated request
			// (a retry, a double click) ends the same way as the first.
			if database.IsNotFound(database.Translate(err)) {
				return nil
			}
			return database.Translate(err)
		}
		var toolIDs []uint
		if err := tx.Model(&Tool{}).Where("server_id = ?", id).Pluck("id", &toolIDs).Error; err != nil {
			return database.Translate(err)
		}
		if err := s.links.RemoveToolLinks(ctx, tx, toolIDs); err != nil {
			return err
		}
		if err := tx.Where("server_id = ?", id).Delete(&Tool{}).Error; err != nil {
			return database.Translate(err)
		}
		return database.Translate(tx.Delete(&Server{}, id).Error)
	})
}

// redactHeaders hides the values of headers that look like credentials.
func redactHeaders(stored string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stored)), &raw); err != nil || raw == nil {
		return "{}"
	}
	for k, v := range raw {
		if _, isStr := v.(string); isStr && sensitiveHeader(k) {
			raw[k] = redactedMarker
		}
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return "{}"
	}
	return string(out)
}

func sensitiveHeader(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", "-"))
	if n == "cookie" || n == "set-cookie" {
		return true
	}
	for _, frag := range []string{"authorization", "api-key", "apikey", "token", "secret"} {
		if strings.Contains(n, frag) {
			return true
		}
	}
	return false
}

// lockRows asks for row locks where the database supports them.
func lockRows(tx *gorm.DB) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

// isNotFound is database.IsNotFound for readability at call sites.
func isNotFound(err error) bool {
	return errors.Is(err, database.ErrNotFound) || database.IsNotFound(err)
}
