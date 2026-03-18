package integration

import (
	"context"
	"math"
	"strings"

	"gorm.io/gorm"

	"errors"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/features/auth"
	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/contentmoderation"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	platformllm "github.com/riochat/riochat/backend/internal/features/platform-llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/embedding"
	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/features/user"
	"time"
)

// ---- user lookups for listings ----

// accountsByIDs reads display fields of users through the user area's model.
func accountsByIDs(ctx context.Context, db *gorm.DB, ids []uint) (map[uint]user.Account, error) {
	out := map[uint]user.Account{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []user.Account
	if err := db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// searchAccounts returns ids of users whose username, display name or email
// contains q.
func searchAccounts(ctx context.Context, db *gorm.DB, q string) ([]uint, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q)) + "%"
	var ids []uint
	err := db.WithContext(ctx).Model(&user.Account{}).
		Where(`LOWER(username) LIKE ? ESCAPE '\' OR LOWER(display_name) LIKE ? ESCAPE '\' OR LOWER(email) LIKE ? ESCAPE '\'`, like, like, like).
		Limit(500).Pluck("id", &ids).Error
	return ids, err
}

type billingUsers struct{ db *gorm.DB }

func (b billingUsers) Users(ctx context.Context, ids []uint) (map[uint]billing.UserInfo, error) {
	rows, err := accountsByIDs(ctx, b.db, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]billing.UserInfo, len(rows))
	for id, r := range rows {
		out[id] = billing.UserInfo{Username: r.Username, DisplayName: r.DisplayName, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

type moderationUsers struct{ db *gorm.DB }

func (m moderationUsers) Labels(ctx context.Context, ids []uint) (map[uint]contentmoderation.UserLabel, error) {
	rows, err := accountsByIDs(ctx, m.db, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]contentmoderation.UserLabel, len(rows))
	for id, r := range rows {
		label := r.DisplayName
		if strings.TrimSpace(label) == "" {
			label = r.Username
		}
		out[id] = contentmoderation.UserLabel{Label: label, Username: r.Username}
	}
	return out, nil
}

func (m moderationUsers) Search(ctx context.Context, q string) ([]uint, error) {
	return searchAccounts(ctx, m.db, q)
}

// actorResolver feeds the audit list with user names.
func actorResolver(accounts *user.Accounts) audit.ActorResolver {
	return audit.ActorResolverFunc(func(ctx context.Context, id uint) (audit.Actor, bool, error) {
		a, err := accounts.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, user.ErrNotFound) {
				return audit.Actor{}, false, nil
			}
			return audit.Actor{}, false, err
		}
		return audit.Actor{Username: a.Username, DisplayName: a.DisplayName}, true, nil
	})
}

// ---- GeoIP ----

type geoResolver struct{}

func (geoResolver) Lookup(ctx context.Context, ip string) (auth.GeoInfo, error) {
	p, ok := platformllm.Current()
	if !ok || p.GeoIP == nil {
		return auth.GeoInfo{}, errors.New("geoip is not configured")
	}
	loc, err := p.GeoIP.Lookup(ctx, ip)
	if err != nil {
		return auth.GeoInfo{}, err
	}
	return auth.GeoInfo(loc), nil
}

// ---- conversation: moderation ----

// convModeration checks one piece of text per call. Records of the turn itself
// are handled by the conversation area, so the run is tracked as ephemeral.
type convModeration struct{ h *Hub }

func (m convModeration) Check(ctx context.Context, req conversation.ModerationRequest) (conversation.ModerationVerdict, error) {
	if m.h.mod == nil || (strings.TrimSpace(req.Text) == "" && len(req.Images) == 0) {
		return conversation.ModerationVerdict{}, nil
	}
	run := m.h.mod.BeginRun(ctx, contentmoderation.RunInfo{UserID: req.UserID, ConversationID: req.ConversationID, Ephemeral: true}, contentmoderation.RunOptions{})
	defer run.Close()
	if !run.Active() {
		return conversation.ModerationVerdict{}, nil
	}
	var out contentmoderation.Outcome
	if req.Direction == "output" {
		out = run.Finish(ctx, contentmoderation.Output{AssistantText: req.Text})
	} else {
		run.CheckInputText(req.Text)
		if len(req.Images) > 0 {
			// Temporary-chat images travel as bytes; they have no stored file.
			refs := make([]contentmoderation.ImageRef, 0, len(req.Images))
			for _, img := range req.Images {
				refs = append(refs, contentmoderation.ImageRef{Bytes: img.Bytes, Name: img.Name})
			}
			run.CheckInputImages(refs)
		}
		out = run.Finish(ctx, contentmoderation.Output{})
	}
	return conversation.ModerationVerdict{Blocked: out.Blocked, EventID: out.EventID, Direction: out.Direction, Categories: out.Categories, BilledReason: out.BilledReason}, nil
}

// ---- memory: embeddings ----

type memoryEmbedder struct{ h *Hub }

func (m memoryEmbedder) Enabled() bool {
	f := m.h.runtime().File
	return f.EmbeddingEnabled && strings.TrimSpace(f.EmbeddingHost) != "" && strings.TrimSpace(f.RAGModel) != ""
}

func (m memoryEmbedder) Embed(ctx context.Context, text string) ([]float32, string, error) {
	p, ok := platformllm.Current()
	if !ok || p.Embeddings == nil {
		return nil, "", errors.New("embedding client is not available")
	}
	f := m.h.runtime().File
	vecs, err := p.Embeddings.Embed(ctx, embedding.Request{
		Protocol: f.EmbeddingProtocol, APIBase: f.EmbeddingHost, APIKey: f.EmbeddingKey, Model: f.RAGModel,
		Inputs: []embedding.Input{{Text: text}}, Purpose: embedding.PurposeDocument,
		Dimensions: f.EmbeddingOutputDimensions, OmitDimensions: f.EmbeddingDimensionsPolicy == "omit",
		Timeout: time.Duration(f.EmbeddingTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, "", err
	}
	if len(vecs) != 1 {
		return nil, "", errors.New("embedding service returned an unexpected number of vectors")
	}
	v := vecs[0]
	if f.EmbeddingNormalize {
		var sum float64
		for _, x := range v {
			sum += float64(x) * float64(x)
		}
		if n := math.Sqrt(sum); n > 0 {
			for i := range v {
				v[i] = float32(float64(v[i]) / n)
			}
		}
	}
	sig := strings.TrimSpace(f.EmbeddingModelSignature)
	if sig == "" {
		sig = settings.ModelSignature(f.RAGModel, f.EmbeddingOutputDimensions)
	}
	return v, sig, nil
}
