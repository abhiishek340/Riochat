package ragindex

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

// scored is a chunk with a relevance score.
type scored struct {
	ChunkID uint
	Score   float64
}

// readableFiles returns the files, among ids, that hold chunks of the vector
// space which userID may read: chunks written for the user, or files that are
// members of an enabled platform-scope knowledge base.
func (s *Service) readableFiles(ctx context.Context, userID uint, ids []uint, sig string) ([]uint, error) {
	if userID == 0 || len(ids) == 0 || sig == "" {
		return nil, nil
	}
	var owned, withChunks []uint
	if err := s.db.WithContext(ctx).Model(&Chunk{}).Distinct().
		Where("file_obj_id IN ? AND embedding_signature = ? AND user_id = ?", ids, sig, userID).
		Pluck("file_obj_id", &owned).Error; err != nil {
		return nil, err
	}
	have := make(map[uint]bool, len(owned))
	for _, id := range owned {
		have[id] = true
	}
	out := append([]uint(nil), owned...)
	if s.shared == nil {
		return out, nil
	}
	if err := s.db.WithContext(ctx).Model(&Chunk{}).Distinct().
		Where("file_obj_id IN ? AND embedding_signature = ?", ids, sig).Pluck("file_obj_id", &withChunks).Error; err != nil {
		return nil, err
	}
	var rest []uint
	for _, id := range withChunks {
		if !have[id] {
			rest = append(rest, id)
		}
	}
	if len(rest) == 0 {
		return out, nil
	}
	shared, err := s.shared.EnabledBuiltin(ctx, rest)
	if err != nil {
		return nil, err
	}
	for _, id := range rest {
		if shared[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// topN keeps the n best-scoring items, best first (ties by chunk id).
type topN struct {
	n     int
	items []scored
}

func (t *topN) add(it scored) {
	better := func(a, b scored) bool {
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.ChunkID < b.ChunkID
	}
	if len(t.items) == t.n && !better(it, t.items[len(t.items)-1]) {
		return
	}
	i := sort.Search(len(t.items), func(i int) bool { return better(it, t.items[i]) })
	t.items = append(t.items, scored{})
	copy(t.items[i+1:], t.items[i:])
	t.items[i] = it
	if len(t.items) > t.n {
		t.items = t.items[:t.n]
	}
}

// vectorSearch ranks the chunks of the given files in the vector space by
// cosine similarity to the query vector, best first.
func (s *Service) vectorSearch(ctx context.Context, files []uint, sig string, query []float32, limit int) ([]scored, error) {
	if len(files) == 0 || len(query) == 0 || sig == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.WithContext(ctx).Model(&ChunkVector{}).Select("chunk_id", "embedding").
		Where("file_obj_id IN ? AND embedding_signature = ?", files, sig).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	top := &topN{n: limit}
	for rows.Next() {
		var id uint
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		v := decodeVector(blob)
		if len(v) != len(query) {
			continue
		}
		top.add(scored{ChunkID: id, Score: cosine(query, v)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return top.items, nil
}

// keywordTokens splits a query for PostgreSQL full-text search: every character
// above U+2E7F (CJK and wide forms) is a token of its own, runs of ASCII
// letters and digits form words, everything else separates.
func keywordTokens(q string) []string {
	var out []string
	var word []rune
	flush := func() {
		if len(word) > 0 {
			out = append(out, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	for _, r := range q {
		switch {
		case r > 0x2E7F:
			flush()
			out = append(out, string(r))
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			word = append(word, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// keywordSearch finds chunks by words. PostgreSQL ranks with full-text search
// (tokens joined with OR); SQLite requires every whitespace-separated term to
// occur as a case-insensitive substring and scores every hit 0.5, in chunk id
// order.
func (s *Service) keywordSearch(ctx context.Context, files []uint, sig, query string, limit int) ([]scored, error) {
	if len(files) == 0 || sig == "" || limit <= 0 {
		return nil, nil
	}
	if s.db.Dialector.Name() == "postgres" {
		toks := keywordTokens(query)
		if len(toks) == 0 {
			return nil, nil
		}
		rows, err := s.db.WithContext(ctx).Raw(`SELECT c.id, ts_rank_cd(to_tsvector('simple', c.content), q.query) AS score
FROM file_chunks c, to_tsquery('simple', ?) AS q(query)
WHERE c.file_obj_id IN ? AND c.embedding_signature = ? AND to_tsvector('simple', c.content) @@ q.query
ORDER BY score DESC, c.id ASC LIMIT ?`, strings.Join(toks, " | "), files, sig, limit).Rows()
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []scored
		for rows.Next() {
			var it scored
			if err := rows.Scan(&it.ChunkID, &it.Score); err != nil {
				return nil, err
			}
			out = append(out, it)
		}
		return out, rows.Err()
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, nil
	}
	q := s.db.WithContext(ctx).Model(&Chunk{}).Where("file_obj_id IN ? AND embedding_signature = ?", files, sig)
	for _, t := range terms {
		q = q.Where(`LOWER(content) LIKE ? ESCAPE '\'`, "%"+escapeLike(t)+"%")
	}
	var ids []uint
	if err := q.Order("id").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	out := make([]scored, len(ids))
	for i, id := range ids {
		out[i] = scored{ChunkID: id, Score: 0.5}
	}
	return out, nil
}

const rrfK = 60

// fuse merges two ranked lists by reciprocal rank fusion. Each list gives a
// chunk 1/(60+rank) (rank from 1); the sum is divided by the best possible
// score (first place in both lists, 2/61) and capped at 1. The result is
// ordered by score, then chunk id, and cut to limit.
func fuse(vector, keyword []scored, limit int) []scored {
	sum := map[uint]float64{}
	for i, it := range vector {
		sum[it.ChunkID] += 1.0 / float64(rrfK+i+1)
	}
	for i, it := range keyword {
		sum[it.ChunkID] += 1.0 / float64(rrfK+i+1)
	}
	max := 2.0 / float64(rrfK+1)
	out := make([]scored, 0, len(sum))
	for id, v := range sum {
		sc := v / max
		if sc > 1 {
			sc = 1
		}
		out = append(out, scored{ChunkID: id, Score: sc})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
