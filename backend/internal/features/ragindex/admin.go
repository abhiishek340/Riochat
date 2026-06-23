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
