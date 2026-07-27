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
