package billing

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

// Service is the billing area's domain API. Other areas (chat, permissions,
// users, model catalog) consume it through the exported methods; the HTTP
// handlers are thin wrappers around it.
type Service struct {
	db      *gorm.DB
	cfg     *config.Holder
	log     *logx.Logger
	cache   cache.Cache
	clock   clock.Clock
	secrets *secretbox.KeyRing
	caps    capability.Provider
	ports   Ports
	loc     *time.Location
	http    *http.Client

	// officialMu serializes official price catalog requests.
	officialMu sync.Mutex
}

// NewService builds the service from the platform dependencies and ports.
func NewService(d deps.Deps, p Ports) *Service {
	if p.Settings == nil {
		p.Settings = NewMemorySettings(nil)
	}
	loc := p.Location
	if loc == nil {
		loc = time.Local
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Service{
		db: d.DB, cfg: d.Config, log: d.Logger, cache: d.Cache, clock: d.Clock,
		secrets: d.Secrets, caps: d.Capabilities, ports: p, loc: loc, http: client,
	}
}

// now is the current instant in UTC (all persisted times are UTC so string
// comparison in SQLite orders them correctly).
func (s *Service) now() time.Time { return s.clock.Now().UTC() }

// gatingOn reports whether the billingGating capability is enabled.
func (s *Service) gatingOn() bool { return s.caps != nil && s.caps.Enabled(capability.BillingGating) }

// monthWindow returns the server-local calendar month containing t.
func (s *Service) monthWindow(t time.Time) (start, end time.Time) {
	lt := t.In(s.loc)
	start = time.Date(lt.Year(), lt.Month(), 1, 0, 0, 0, 0, s.loc)
	end = start.AddDate(0, 1, 0)
	return start, end
}

func utcPtr(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}

// ensureAccount returns the user's account, creating a zero USD/active one when
// missing. It must run on the handle that will use the account.
func ensureAccount(tx *gorm.DB, userID uint) (*Account, error) {
	var acc Account
	err := tx.Where("user_id = ?", userID).First(&acc).Error
	if err == nil {
		return &acc, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	acc = Account{UserID: userID, Currency: CurrencyUSD, Status: "active"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&acc).Error; err != nil {
		return nil, err
	}
	if acc.ID == 0 {
		if err := tx.Where("user_id = ?", userID).First(&acc).Error; err != nil {
			return nil, err
		}
	}
	return &acc, nil
}

// lockAccount creates the account when needed and locks its row for the rest
// of the transaction. The account row serializes all per-user money movement.
func lockAccount(tx *gorm.DB, userID uint) (*Account, error) {
	if _, err := ensureAccount(tx, userID); err != nil {
		return nil, err
	}
	var acc Account
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&acc).Error; err != nil {
		return nil, err
	}
	return &acc, nil
}

// requestMeta pulls the audit metadata of an HTTP request.
func (s *Service) audit(c *gin.Context, actor uint, action, resource, resourceID string, detail map[string]any) {
	if s.ports.Audit == nil {
		return
	}
	e := AuditEntry{ActorUserID: actor, Action: action, Resource: resource, ResourceID: resourceID, Detail: detail}
	if c != nil {
		st := reqctx.FromRequest(c.Request)
		e.RequestID = st.RequestID()
		ac := middleware.AuditFromRequest(c.Request)
		e.ClientIP, e.UserAgent = ac.ClientIP, ac.UserAgent
	}
	ctx := context.Background()
	if c != nil {
		ctx = c.Request.Context()
	}
	s.ports.Audit.Record(ctx, e)
}

func (s *Service) auditSystem(ctx context.Context, actor uint, action, resource, resourceID string, detail map[string]any) {
	if s.ports.Audit == nil {
		return
	}
	s.ports.Audit.Record(ctx, AuditEntry{ActorUserID: actor, Action: action, Resource: resource, ResourceID: resourceID, Detail: detail})
}

func personalModelRef(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "personal:")
}

func requestIDOf(c *gin.Context) string { return reqctx.FromRequest(c.Request).RequestID() }
