package contentmoderation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Moderation states of a run.
const (
	StateNotRequired = "not_required"
	StatePending     = "pending"
	StateModerating  = "moderating"
	StatePassed      = "passed"
	StateFailedOpen  = "failed_open"
	StateBlocked     = "blocked"
)

const (
	persistTimeout = 10 * time.Second
	graceAfterCtx  = 100 * time.Millisecond
)

// RunInfo identifies the turn being moderated. Zero ids are used for
// temporary (ephemeral) chats, whose records are never touched.
type RunInfo struct {
	UserID          uint
	ConversationID  uint
	RunID           string
	MessageID       uint
	MessagePublicID string
	Ephemeral       bool
	// PaidReservation marks a turn whose upstream usage is billed even when the
	// turn is blocked.
	PaidReservation bool
}

// RunOptions are the callbacks a turn hands to the engine.
type RunOptions struct {
	// CancelGeneration stops the upstream generation. It is called at most once,
	// as soon as an input check hits.
	CancelGeneration func()
}

// ImageRef names an image to check: a stored file, or raw bytes (temporary
// chats).
type ImageRef struct {
	FileID uint
	Bytes  []byte
	Name   string
	// SHA256 is the stored content hash of the file (hex). Images with the
	// same hash are checked once per turn; images without one are never
	// treated as duplicates of each other.
	SHA256 string
}

// Output is the model output of a turn.
type Output struct {
	AssistantText string
	ReasoningText string
	ThinkingText  string
	// Images are the assistant image attachments, or the generated files of an
	// image generation turn.
	Images []ImageRef
}

// Outcome is the verdict for a turn.
type Outcome struct {
	State      string
	Blocked    bool
	EventID    string
	Direction  string
	Categories []string
	// BilledReason is set on a blocked turn that has a paid reservation; the
	// stream event carries it immediately only for output-side blocks, input-side
	// blocks are labelled at settlement.
	BilledReason string
	// Embeddable is true when the turn may be indexed for retrieval.
	Embeddable bool
}

type blockInfo struct {
	EventID    string
	Direction  string
	Categories []string
}

// Run is the moderation tracking of one turn. Call the Check methods while the
// model generates, then Finish once, and always Close when done.
type Run struct {
	s    *Service
	info RunInfo
	opts RunOptions

	cfg           Config
	active        bool
	configMissing bool

	cancelOnce sync.Once
	wg         sync.WaitGroup

	mu        sync.Mutex
	finishing bool
	settled   bool
	failures  int
	inputHit  *blockInfo
	outputHit *blockInfo

	applyMu sync.Mutex
	applied *blockInfo
}

// BeginRun starts moderation tracking for a turn. When moderation is off the
// returned run is inert and Finish reports "not_required". When the
// configuration cannot be read the run fails open and says so.
func (s *Service) BeginRun(ctx context.Context, info RunInfo, opts RunOptions) *Run {
	r := &Run{s: s, info: info, opts: opts}
	if s.closed.Load() || (s.caps != nil && !s.caps.Enabled(capability.ContentModeration)) {
		return r
	}
	cfg, err := s.currentConfig(ctx)
	if err != nil {
		r.configMissing = true
		s.log.Error("content_moderation_config_unavailable", errField(err))
		r.recordConfigMissing(ctx)
		return r
	}
	if !cfg.Active() {
		return r
	}
	r.cfg, r.active = cfg, true
	s.track(r)
	if !info.Ephemeral && s.runs != nil {
		if err := s.runs.SetRunState(ctx, info.RunID, StatePending, "", ""); err != nil {
			s.log.Warn("content_moderation_run_state_failed", logx.F("state", StatePending), errField(err))
		}
	}
	return r
}

// Active reports whether this run is being moderated.
func (r *Run) Active() bool { return r != nil && r.active }

// Close releases the tracking of an abandoned run. Finish releases it itself.
func (r *Run) Close() {
	if r != nil {
		r.s.untrack(r)
	}
}

func (s *Service) track(r *Run) {
	if r.info.RunID == "" {
		return
	}
	s.trackMu.Lock()
	s.tracked[r.info.RunID] = r
	s.trackMu.Unlock()
}

func (s *Service) untrack(r *Run) {
	if r.info.RunID == "" {
		return
	}
	s.trackMu.Lock()
	if s.tracked[r.info.RunID] == r {
		delete(s.tracked, r.info.RunID)
	}
	s.trackMu.Unlock()
}

func (s *Service) isTracked(runID string) bool {
	s.trackMu.Lock()
	defer s.trackMu.Unlock()
	_, ok := s.tracked[runID]
	return ok
}

// ---- submitting checks ----------------------------------------------------------

type taskBase struct {
	r    *Run
	once sync.Once
}

func (b *taskBase) done() { b.once.Do(b.r.wg.Done) }

type textTask struct {
	taskBase
	direction string
	text      string
}

func (t *textTask) run(ctx context.Context) {
	defer t.done()
	t.r.runText(ctx, t.direction, t.text)
}

func (t *textTask) abort(code string) {
	defer t.done()
	t.r.recordFailure(context.Background(), t.direction, ModalityText, code, 1, 0, nil)
}

type imageTask struct {
	taskBase
	direction string
	refs      []ImageRef
}

func (t *imageTask) run(ctx context.Context) {
	defer t.done()
	t.r.runImages(ctx, t.direction, t.refs)
}

func (t *imageTask) abort(code string) {
	defer t.done()
	t.r.recordFailure(context.Background(), t.direction, ModalityImage, code, len(t.refs), 0, nil)
}

func (r *Run) submit(t checkTask) {
	r.wg.Add(1)
	if code := r.s.exec.submit(t); code != "" {
		// Never queued: resolve it here as a failed-open check.
		t.abort(code)
	}
}

func (r *Run) canSubmit() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active && !r.finishing
}

// CheckInputText queues the user's message text.
func (r *Run) CheckInputText(text string) {
	if r == nil || !r.canSubmit() || strings.TrimSpace(text) == "" {
		return
	}
	if len(r.cfg.Policy.Selected(DirectionInput, ModalityText)) == 0 {
		return
	}
	t := &textTask{taskBase: taskBase{r: r}, direction: DirectionInput, text: text}
	r.submit(t)
}

// CheckInputImages queues all attachments of the turn as one check. Callers
// pass the images the model will see: for video and image generation the
// input or reference pictures, never the source video of an extension (the
// moderation service cannot evaluate video). Non-image files are skipped
// without being reported.
func (r *Run) CheckInputImages(refs []ImageRef) {
	if r == nil || !r.canSubmit() || len(refs) == 0 {
		return
	}
	if len(r.cfg.Policy.Selected(DirectionInput, ModalityImage)) == 0 {
		return
	}
	t := &imageTask{taskBase: taskBase{r: r}, direction: DirectionInput, refs: refs}
	r.submit(t)
}
