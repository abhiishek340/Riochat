package ragindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Retrieval statuses and reasons.
const (
	StatusHit         = "rag_hit"
	StatusEmpty       = "rag_empty"
	StatusLowScore    = "rag_low_score"
	StatusUnavailable = "rag_unavailable"
	StatusError       = "rag_error"
	StatusTimeout     = "rag_timeout"

	ReasonMatched        = "matched"
	ReasonCacheHit       = "cache_hit"
	ReasonNoCandidates   = "no_candidates"
	ReasonBelowMin       = "below_min_similarity"
	ReasonBudget         = "token_budget_exhausted"
	ReasonRetrievalError = "retrieval_error"
	ReasonDeadline       = "deadline_exceeded"
)

const cacheVersion = "v1"

// SearchFile describes one file offered to a search, with the facts that make
// the cache key follow the file's state.
type SearchFile struct {
	ObjectID   uint
	FileID     string
	Name       string
	ChunkCount int
	State      string
	UpdatedAt  time.Time
}

// SearchRequest asks for the passages of files that answer a query.
type SearchRequest struct {
	UserID uint
	Query  string
	Files  []SearchFile
	// Ephemeral searches (temporary chats) never read or write the cache.
	Ephemeral bool
}

// Hit is one retrieved passage.
type Hit struct {
	ChunkID      uint    `json:"chunkId"`
	FileObjectID uint    `json:"fileObjectId"`
	FileID       string  `json:"fileId"`
	FileName     string  `json:"fileName"`
	ChunkIndex   int     `json:"chunkIndex"`
	Modality     string  `json:"modality"`
	Content      string  `json:"content"`
	TokenCount   int     `json:"tokenCount"`
	Score        float64 `json:"score"`
}

// SearchResult is the outcome of a search. Err is set for rag_error and
// rag_timeout.
type SearchResult struct {
	Status     string
	Reason     string
	Hits       []Hit
	Cached     bool
	Candidates int
	Filtered   int
	MaxScore   float64
	Err        error
}

// BuildQuery forms the search text: the current message, followed by up to
// turns earlier user messages (newest first when scanning backward, blank ones
// skipped, each cut to 240 characters) listed oldest first under a short
// heading. prior holds the earlier user messages oldest first, excluding the
// current one.
func BuildQuery(current string, prior []string, turns int) string {
	q := strings.TrimSpace(current)
	if turns <= 0 {
		return q
	}
	var picked []string
	for i := len(prior) - 1; i >= 0 && len(picked) < turns; i-- {
		m := strings.TrimSpace(prior[i])
		if m == "" {
			continue
		}
		picked = append(picked, truncateRunes(m, 240))
	}
	if len(picked) == 0 {
		return q
	}
	var b strings.Builder
	b.WriteString(q)
	b.WriteString("\n\nRecent user context:")
	for i := len(picked) - 1; i >= 0; i-- {
		b.WriteString("\n- ")
		b.WriteString(picked[i])
	}
	return b.String()
}

// selectHits chooses up to topK passages within the token budget. Pass one
// takes at most two chunks per file and only chunks scoring at least 75 percent
// of the best candidate (when the best score is positive); pass two fills the
// remaining slots by rank without those limits. A chunk that would overflow the
// budget is skipped, so later smaller ones can still fit. The chosen passages
// are returned in document order (file order given by order, then chunk index).
func selectHits(cands []Hit, topK, budget int, order map[uint]int) []Hit {
	if topK <= 0 {
		topK = 5
	}
	if budget <= 0 {
		budget = 2000
	}
	cost := func(h Hit) int {
		if h.TokenCount > 0 {
			return h.TokenCount
		}
		return EstimateTokens(h.Content)
	}
	best := 0.0
	for _, c := range cands {
		if c.Score > best {
			best = c.Score
		}
	}
	chosen := make([]bool, len(cands))
	perFile := map[uint]int{}
	used, count := 0, 0
	take := func(i int) {
		chosen[i] = true
		perFile[cands[i].FileObjectID]++
		used += cost(cands[i])
		count++
	}
	for i, c := range cands {
		if count >= topK {
			break
		}
		if perFile[c.FileObjectID] >= 2 {
			continue
		}
		if best > 0 && c.Score < 0.75*best {
			continue
		}
		if used+cost(c) > budget {
			continue
		}
		take(i)
	}
	for i, c := range cands {
		if count >= topK {
			break
		}
		if chosen[i] || used+cost(c) > budget {
			continue
		}
		take(i)
	}
	var out []Hit
	for i, c := range cands {
		if chosen[i] {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := order[out[i].FileObjectID], order[out[j].FileObjectID]
		if oi != oj {
			return oi < oj
		}
		if out[i].FileObjectID != out[j].FileObjectID {
			return out[i].FileObjectID < out[j].FileObjectID
		}
		return out[i].ChunkIndex < out[j].ChunkIndex
	})
	return out
}

// DedupHits drops passages already used in this request, identified by public
// file id and chunk index, and records the new ones in seen.
func DedupHits(seen map[string]bool, hits []Hit) []Hit {
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		key := h.FileID + "#" + strconv.Itoa(h.ChunkIndex)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	return out
}
