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

// cacheKey hashes everything that decides a retrieval's result: the user, the
// whitespace-collapsed query, the embedding endpoint, model and retrieval
// settings, and for every file its identity and index state.
func cacheKey(cfg Config, userID uint, query string, files []SearchFile) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%t|%d|%g|%d|%d|%d", cacheVersion, userID, collapseSpace(query), cfg.host(), cfg.model(),
		cfg.Hybrid, cfg.topK(), cfg.minSimilarity(), cfg.tokenBudget(), cfg.fetchMultiplier(), cfg.Dimensions)
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, fmt.Sprintf("%d:%s:%d:%s:%d", f.ObjectID, f.FileID, f.ChunkCount, f.State, f.UpdatedAt.UnixNano()))
	}
	sort.Strings(parts)
	for _, p := range parts {
		h.Write([]byte("|"))
		h.Write([]byte(p))
	}
	return "ragindex:retrieval:" + hex.EncodeToString(h.Sum(nil))
}

// cachedResult is what the retrieval cache stores.
type cachedResult struct {
	Hits       []Hit   `json:"hits"`
	Candidates int     `json:"candidates"`
	Filtered   int     `json:"filtered"`
	MaxScore   float64 `json:"maxScore"`
}

// Retrieve searches the files for passages that answer the query. It never
// returns an error: failures are reported through the result status.
func (s *Service) Retrieve(ctx context.Context, req SearchRequest) SearchResult {
	cfg := s.config(ctx)
	if !cfg.RAGEnabled || !cfg.Enabled || cfg.model() == "" {
		return SearchResult{Status: StatusUnavailable}
	}
	query := strings.TrimSpace(req.Query)
	if query == "" || len(req.Files) == 0 {
		return SearchResult{Status: StatusEmpty}
	}
	useCache := !req.Ephemeral && cfg.CacheTTLSeconds > 0 && s.kv != nil
	var key string
	if useCache {
		key = cacheKey(cfg, req.UserID, query, req.Files)
		if raw, ok, err := s.kv.Get(ctx, key); err == nil && ok {
			var c cachedResult
			if json.Unmarshal(raw, &c) == nil && len(c.Hits) > 0 {
				return SearchResult{Status: StatusHit, Reason: ReasonCacheHit, Hits: c.Hits, Cached: true,
					Candidates: c.Candidates, Filtered: c.Filtered, MaxScore: c.MaxScore}
			}
		}
	}

	vecs, err := s.embedBatches(ctx, cfg, PurposeQuery, []EmbedInput{{Text: query}})
	if err == nil && (len(vecs) != 1 || len(vecs[0]) == 0) {
		err = errors.New("ragindex: empty query embedding")
	}
	if err != nil {
		return failedSearch(ctx, err)
	}
	qvec := vecs[0]

	sig := cfg.Signature()
	ids := make([]uint, 0, len(req.Files))
	order := make(map[uint]int, len(req.Files))
	names := make(map[uint]SearchFile, len(req.Files))
	for i, f := range req.Files {
		if f.ObjectID == 0 {
			continue
		}
		if _, dup := names[f.ObjectID]; dup {
			continue
		}
		ids = append(ids, f.ObjectID)
		order[f.ObjectID] = i
		names[f.ObjectID] = f
	}
	readable, err := s.readableFiles(ctx, req.UserID, ids, sig)
	if err != nil {
		return failedSearch(ctx, err)
	}
	topK, minSim := cfg.topK(), cfg.minSimilarity()
	fetch := topK * cfg.fetchMultiplier()

	var ranked []scored
	if cfg.Hybrid {
		ranked, err = s.hybridSearch(ctx, readable, sig, qvec, query, fetch, minSim)
	} else {
		ranked, err = s.vectorSearch(ctx, readable, sig, qvec, fetch)
	}
	if err != nil {
		return failedSearch(ctx, err)
	}
	res := SearchResult{Candidates: len(ranked)}
	if len(ranked) == 0 {
		res.Status, res.Reason = StatusEmpty, ReasonNoCandidates
		return res
	}
	for _, r := range ranked {
		if r.Score > res.MaxScore {
			res.MaxScore = r.Score
		}
	}
	kept := ranked
	if !cfg.Hybrid {
		kept = kept[:0:0]
		for _, r := range ranked {
			if r.Score >= minSim {
				kept = append(kept, r)
			}
		}
	}
	res.Filtered = len(kept)
	if len(kept) == 0 {
		res.Status, res.Reason = StatusLowScore, ReasonBelowMin
		return res
	}
	hits, err := s.loadHits(ctx, kept, names)
	if err != nil {
		return failedSearch(ctx, err)
	}
	picked := selectHits(hits, topK, cfg.tokenBudget(), order)
	if len(picked) == 0 {
		res.Status, res.Reason = StatusEmpty, ReasonBudget
		return res
	}
	res.Status, res.Reason, res.Hits = StatusHit, ReasonMatched, picked
	if useCache {
		if raw, err := json.Marshal(cachedResult{Hits: picked, Candidates: res.Candidates, Filtered: res.Filtered, MaxScore: res.MaxScore}); err == nil {
			if err := s.kv.Set(ctx, key, raw, time.Duration(cfg.CacheTTLSeconds)*time.Second); err != nil {
				s.log.Debug("rag_cache_set_failed", logx.Err(err))
			}
		}
	}
	return res
}

// failedSearch maps a failure to the error or timeout status.
func failedSearch(ctx context.Context, err error) SearchResult {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return SearchResult{Status: StatusTimeout, Reason: ReasonDeadline, Err: err}
	}
	return SearchResult{Status: StatusError, Reason: ReasonRetrievalError, Err: err}
}

// hybridSearch runs the vector and keyword searches in parallel and fuses
// them. Vector hits below the minimum similarity are dropped before fusion;
// keyword hits are not thresholded; a keyword failure leaves the vector side.
func (s *Service) hybridSearch(ctx context.Context, files []uint, sig string, qvec []float32, query string, fetch int, minSim float64) ([]scored, error) {
	var (
		wg           sync.WaitGroup
		vec, kw      []scored
		vecErr, kwEr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); vec, vecErr = s.vectorSearch(ctx, files, sig, qvec, fetch) }()
	go func() { defer wg.Done(); kw, kwEr = s.keywords(ctx, files, sig, query, fetch) }()
	wg.Wait()
	if vecErr != nil {
		return nil, vecErr
	}
	if kwEr != nil {
		s.log.Warn("rag_keyword_search_failed", logx.Err(kwEr))
		kw = nil
	}
	kept := vec[:0:0]
	for _, v := range vec {
		if v.Score >= minSim {
			kept = append(kept, v)
		}
	}
	return fuse(kept, kw, fetch), nil
}

// loadHits reads the chunk rows of the ranked ids, in rank order.
func (s *Service) loadHits(ctx context.Context, ranked []scored, files map[uint]SearchFile) ([]Hit, error) {
	ids := make([]uint, len(ranked))
	for i, r := range ranked {
		ids[i] = r.ChunkID
	}
	var rows []Chunk
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]Chunk, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]Hit, 0, len(ranked))
	for _, r := range ranked {
		c, ok := byID[r.ChunkID]
		if !ok {
			continue
		}
		f := files[c.FileObjID]
		out = append(out, Hit{
			ChunkID: c.ID, FileObjectID: c.FileObjID, FileID: f.FileID, FileName: f.Name, ChunkIndex: c.ChunkIndex,
			Modality: c.Modality, Content: c.Content, TokenCount: c.TokenCount, Score: r.Score,
		})
	}
	return out, nil
}
