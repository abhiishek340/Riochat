package ragindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Status summarises the index. The field order and names mirror the settings
// area's EmbeddingStatus so the integration layer can convert directly.
type Status struct {
	ModelSignature   string `json:"modelSignature"`
	ReadyCount       int64  `json:"readyCount"`
	StaleCount       int64  `json:"staleCount"`
	PendingCount     int64  `json:"pendingCount"`
	StalledCount     int64  `json:"stalledCount"`
	ActiveCount      int64  `json:"activeCount"`
	FailedCount      int64  `json:"failedCount"`
	EmptyCount       int64  `json:"emptyCount"`
	UnsupportedCount int64  `json:"unsupportedCount"`
	NeedsReindex     bool   `json:"needsReindex"`
	ReindexRunning   bool   `json:"reindexRunning"`
}

// Status counts active files of all users by index state (see the settings
// documentation of GET /admin/settings/embedding/status).
func (s *Service) Status(ctx context.Context) (Status, error) {
	cfg := s.config(ctx)
	now := s.now()
	st := Status{ModelSignature: cfg.Signature(), ReindexRunning: s.rebuilding.Load()}
	base := func() *queryDB { return s.activeFiles(ctx) }

	count := func(where string, args ...any) (int64, error) {
		var n int64
		err := base().Where(where, args...).Count(&n).Error
		return n, database.Translate(err)
	}
	var err error
	if st.ReadyCount, err = count("embed_status = ?", StateReady); err != nil {
		return st, err
	}
	if st.StaleCount, err = count("embed_status = ?", StateStale); err != nil {
		return st, err
	}
	if st.EmptyCount, err = count("embed_status = ?", StateEmpty); err != nil {
		return st, err
	}
	failedState, err := count("embed_status = ?", StateFailed)
	if err != nil {
		return st, err
	}
	if st.StalledCount, err = count("embed_status IN ? AND updated_at <= ?", inFlight, now.Add(-stalledAfter)); err != nil {
		return st, err
	}
	inflight, err := count("embed_status IN ?", inFlight)
	if err != nil {
		return st, err
	}
	st.ActiveCount = inflight - st.StalledCount

	emb, embArgs := s.embeddableClause(cfg)
	neverFailed, err := count(noneClause+" AND "+emb+" AND processing_status = ?", append(embArgs, procFailed)...)
	if err != nil {
		return st, err
	}
	neverPending, err := count(noneClause+" AND "+emb+" AND COALESCE(processing_status,'') <> ?", append(embArgs, procFailed)...)
	if err != nil {
		return st, err
	}
	if st.UnsupportedCount, err = count(noneClause+" AND NOT "+emb, embArgs...); err != nil {
		return st, err
	}
	st.FailedCount = failedState + neverFailed
	st.PendingCount = neverPending + inflight
	st.NeedsReindex = st.StaleCount > 0
	return st, nil
}

// Task buckets of the administration listing.
const (
	BucketReady       = "ready"
	BucketPending     = "pending"
	BucketFailed      = "failed"
	BucketStale       = "stale"
	BucketEmpty       = "empty"
	BucketUnsupported = "unsupported"
)

// TaskQuery selects a page of index tasks.
type TaskQuery struct {
	Bucket   string
	Query    string
	Page     int
	PageSize int
}

// Task is one row of the index administration listing.
type Task struct {
	FileID             string    `json:"fileID"`
	FileName           string    `json:"fileName"`
	MimeType           string    `json:"mimeType"`
	SizeBytes          int64     `json:"sizeBytes"`
	UserID             uint      `json:"userID"`
	UserLabel          string    `json:"userLabel"`
	EmbedStatus        string    `json:"embedStatus"`
	EmbedError         string    `json:"embedError"`
	Stalled            bool      `json:"stalled"`
	Retryable          bool      `json:"retryable"`
	RetryBlockedReason string    `json:"retryBlockedReason"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// bucketClause returns the WHERE fragment of a bucket.
func (s *Service) bucketClause(cfg Config, bucket string) (string, []any, bool) {
	emb, embArgs := s.embeddableClause(cfg)
	switch bucket {
	case BucketReady:
		return "embed_status = ?", []any{StateReady}, true
	case BucketStale:
		return "embed_status = ?", []any{StateStale}, true
	case BucketEmpty:
		return "embed_status = ?", []any{StateEmpty}, true
	case BucketPending:
		return "((" + noneClause + " AND " + emb + " AND COALESCE(processing_status,'') <> ?) OR embed_status IN ?)",
			append(append([]any{}, embArgs...), procFailed, inFlight), true
	case BucketFailed:
		return "(embed_status = ? OR (" + noneClause + " AND " + emb + " AND processing_status = ?))",
			append(append([]any{StateFailed}, embArgs...), procFailed), true
	case BucketUnsupported:
		return noneClause + " AND NOT " + emb, embArgs, true
	}
	return "", nil, false
}

// escapeLike makes %, _ and \ literal in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Tasks lists one bucket of files, most recently updated first.
func (s *Service) Tasks(ctx context.Context, q TaskQuery) ([]Task, int64, error) {
	cfg := s.config(ctx)
	clause, args, ok := s.bucketClause(cfg, q.Bucket)
	if !ok {
		return nil, 0, fmt.Errorf("ragindex: unknown bucket %q", q.Bucket)
	}
	db := s.activeFiles(ctx).Where(clause, args...)
	if kw := strings.ToLower(strings.TrimSpace(q.Query)); kw != "" {
		like := "%" + escapeLike(kw) + "%"
		db = db.Where(`(LOWER(file_id) LIKE ? ESCAPE '\' OR LOWER(file_name) LIKE ? ESCAPE '\')`, like, like)
	}
	db = db.Session(&gorm.Session{})
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	page, size := q.Page, q.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 1000 {
		size = 1000
	}
	var rows []File
	if err := db.Order("updated_at DESC, id DESC").Offset((page-1)*size).Limit(size).
		Select("id", "created_at", "updated_at", "file_id", "user_id", "file_name", "mime_type", "detected_mime", "file_category",
			"size_bytes", "storage_path", "status", "processing_status", "processing_ready", "processing_error_message",
			"extract_status", "embed_status", "embed_signature", "embed_error", "rag_opt_out").
		Find(&rows).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	labels := map[uint]string{}
	if s.users != nil && len(rows) > 0 {
		seen := map[uint]bool{}
		var ids []uint
		for _, r := range rows {
			if r.UserID != 0 && !seen[r.UserID] {
				seen[r.UserID] = true
				ids = append(ids, r.UserID)
			}
		}
		if m, err := s.users.Labels(ctx, ids); err == nil {
			labels = m
		} else {
			s.log.Warn("rag_task_labels_failed", logx.Err(err))
		}
	}
	sig := cfg.Signature()
	now := s.now()
	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		t := Task{
			FileID: r.FileID, FileName: r.FileName, MimeType: r.MIME(), SizeBytes: r.SizeBytes, UserID: r.UserID,
			UserLabel: labels[r.UserID], EmbedStatus: r.state(), EmbedError: r.EmbedError,
			Stalled: stalled(r, now), UpdatedAt: r.UpdatedAt,
		}
		if r.ProcessingStatus == procFailed {
			t.EmbedError = r.ProcessingErrMsg
			if strings.TrimSpace(t.EmbedError) == "" {
				t.EmbedError = r.EmbedError
			}
		}
		if d := s.decide(r, cfg, sig, now, true); d.reason != "" {
			t.RetryBlockedReason = d.reason
		}
		t.Retryable = t.RetryBlockedReason == ""
		out = append(out, t)
	}
	return out, total, nil
}

type queryDB = gormDB

// Retry submits files of any user for indexing (administrator action).
func (s *Service) Retry(ctx context.Context, ids []string) (SubmitResult, error) {
	return s.SubmitAsAdmin(ctx, ids)
}

// Probe embeds a short text through the normal path to check the provider.
func (s *Service) Probe(ctx context.Context, text string) error {
	cfg := s.config(ctx)
	if text = strings.TrimSpace(text); text == "" {
		text = "health check"
	}
	if s.emb == nil {
		return ErrUnavailable
	}
	if r := cfg.configReason(); r != "" {
		return ErrNotConfigured.WithCause(errors.New(r))
	}
	vecs, err := s.embedBatches(ctx, cfg, PurposeQuery, []EmbedInput{{Text: text}})
	if err != nil {
		return err
	}
	if len(vecs) != 1 {
		return errBatchMismatch
	}
	return nil
}

// ReindexResult is the outcome of a bulk rebuild request. Submitted counts the
// files queued for the background rebuild and Reprocessed those sent back
// through text extraction.
type ReindexResult struct {
	Submitted   int
	Reprocessed int
}

const reindexPage = 100

// rebuildWorkers is the number of files indexed concurrently by one rebuild.
const rebuildWorkers = 4

// candidateQuery selects files whose text is ready but whose index is missing,
// stale or failed, or queued for over 30 minutes without progress.
func (s *Service) candidateQuery(ctx context.Context, now time.Time) *queryDB {
	return s.activeFiles(ctx).
		Where("processing_status = ? AND COALESCE(extract_status,'') <> ?", procReady, extractEmpty).
		Where("("+noneClause+" OR embed_status IN ? OR (embed_status = ? AND updated_at <= ?))",
			[]string{StateStale, StateFailed}, StateQueued, now.Add(-stalledAfter))
}

// candidates pages through the rebuild candidates in id order and calls fn for
// each indexable one until fn returns false.
func (s *Service) candidates(ctx context.Context, cfg Config, now time.Time, fn func(File) bool) error {
	var last uint
	for {
		var rows []File
		if err := s.candidateQuery(ctx, now).Where("id > ?", last).Order("id").Limit(reindexPage).Find(&rows).Error; err != nil {
			return database.Translate(err)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, f := range rows {
			last = f.ID
			if !s.indexable(f, cfg) {
				continue
			}
			if !fn(f) {
				return nil
			}
		}
	}
}

// Reindex builds the missing indexes without touching ready files and sends
// files without usable text back through extraction. It returns
// ErrReindexRunning together with the outcome when a rebuild is already running
// in this process.
func (s *Service) Reindex(ctx context.Context, includeEmpty bool) (ReindexResult, error) {
	var out ReindexResult
	cfg := s.config(ctx)
	av := s.availabilityFor(ctx, cfg)
	if !av.OK {
		if av.Reason == ReasonStoreError {
			return out, errors.New("ragindex: vector store check failed")
		}
		return out, ErrNotConfigured.WithCause(errors.New(av.Reason))
	}
	sig := cfg.Signature()
	now := s.now()
	busy := s.rebuilding.Load()
	if !busy {
		count := 0
		if err := s.candidates(ctx, cfg, now, func(File) bool { count++; return true }); err != nil {
			return out, err
		}
		if count > 0 {
			if s.rebuilding.CompareAndSwap(false, true) {
				out.Submitted = count
				s.startRebuild(ctx, sig, cfg.host())
			} else {
				busy = true
			}
		}
	}
	n, err := s.reprocessWithoutText(ctx, cfg, sig, includeEmpty)
	out.Reprocessed = n
	if err != nil {
		return out, err
	}
	if busy {
		return out, ErrReindexRunning
	}
	return out, nil
}

// reprocessWithoutText resets files lacking usable text and queues them for
// extraction. When the request context runs out after some were submitted the
// partial count is returned.
func (s *Service) reprocessWithoutText(ctx context.Context, cfg Config, sig string, includeEmpty bool) (int, error) {
	now := s.now()
	submitted := 0
	var last uint
	for {
		var rows []File
		q := s.activeFiles(ctx).Where("id > ? AND COALESCE(storage_path,'') <> ?", last, "").
			Where("(processing_status = ? OR (processing_status IN ? AND updated_at <= ?)"+emptyOr(includeEmpty)+")",
				append([]any{procFailed, []string{procUploaded, procQueued}, now.Add(-stalledAfter)}, emptyArgs(includeEmpty)...)...)
		if err := q.Order("id").Limit(reindexPage).Find(&rows).Error; err != nil {
			if ctx.Err() != nil && submitted > 0 {
				return submitted, nil
			}
			return submitted, database.Translate(err)
		}
		if len(rows) == 0 {
			return submitted, nil
		}
		for _, f := range rows {
			last = f.ID
			if ctx.Err() != nil {
				if submitted > 0 {
					return submitted, nil
				}
				return submitted, ctx.Err()
			}
			if !s.indexable(f, cfg) {
				continue
			}
			if s.submitReextract(ctx, f, cfg, sig, now, includeEmpty) == "" {
				submitted++
			}
		}
	}
}

func emptyOr(include bool) string {
	if include {
		return " OR extract_status = ?"
	}
	return ""
}

func emptyArgs(include bool) []any {
	if include {
		return []any{extractEmpty}
	}
	return nil
}

// startRebuild runs the single background rebuild for the signature. It scans
// candidates in id order and feeds them to four workers; it gives up when the
// signature changes or the server shuts down.
func (s *Service) startRebuild(ctx context.Context, sig, host string) {
	run := func(rctx context.Context) {
		defer s.rebuilding.Store(false)
		defer s.rebuildWG.Done()
		s.rebuild(rctx, sig, host)
	}
	s.rebuildWG.Add(1)
	if s.life != nil {
		s.life.Detach(ctx, "rag-rebuild", 0, run)
		return
	}
	run(context.WithoutCancel(ctx))
}

// WaitRebuild blocks until the running rebuild (if any) ends.
func (s *Service) WaitRebuild() { s.rebuildWG.Wait() }

func (s *Service) rebuild(ctx context.Context, sig, host string) {
	stop := func() bool {
		if s.life != nil && s.life.Context().Err() != nil {
			return true
		}
		return ctx.Err() != nil || s.config(ctx).Signature() != sig
	}
	jobs := make(chan File)
	var wg sync.WaitGroup
	for i := 0; i < rebuildWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				if stop() {
					continue
				}
				s.rebuildOne(ctx, f.ID, sig, host)
			}
		}()
	}
	cfg := s.config(ctx)
	err := s.candidates(ctx, cfg, s.now(), func(f File) bool {
		if stop() {
			return false
		}
		jobs <- f
		return true
	})
	close(jobs)
	wg.Wait()
	if err != nil {
		s.log.Warn("rag_rebuild_failed", logx.Err(err))
	}
}

// rebuildOne indexes one file: claim, build, publish, with the run limit.
func (s *Service) rebuildOne(ctx context.Context, id uint, sig, host string) {
	if !s.acquire(ctx) {
		return
	}
	defer s.release()
	rctx, cancel := context.WithTimeout(ctx, runLimit)
	defer cancel()
	if _, err := s.runIndex(rctx, id, sig, host, runOpts{}); err != nil {
		s.log.Warn("rag_rebuild_file_failed", logx.F("file_obj_id", id), logx.Err(err))
		s.failFile(ctx, id, sig, err)
	}
}
