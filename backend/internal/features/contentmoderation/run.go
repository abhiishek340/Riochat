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

// composeOutputText joins the distinct, non-blank output parts with blank lines.
func composeOutputText(o Output) string {
	var parts []string
	seen := map[string]bool{}
	for _, p := range []string{o.AssistantText, o.ReasoningText, o.ThinkingText} {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		parts = append(parts, p)
	}
	return strings.Join(parts, "\n\n")
}

// ReportOutputImageFailure records that an image the reply was supposed to
// carry could not be loaded (missing file, empty data, storage error). It is a
// failed-open event of the output-image surface and never counts as a pass.
// Nothing is recorded when the output-image scope has no selected category or
// when the outcome of the turn has already been settled.
func (r *Run) ReportOutputImageFailure(ctx context.Context) {
	if r == nil || !r.active {
		return
	}
	if len(r.cfg.Policy.Selected(DirectionOutput, ModalityImage)) == 0 {
		return
	}
	r.mu.Lock()
	settled := r.settled
	r.mu.Unlock()
	if settled {
		return
	}
	r.recordFailure(ctx, DirectionOutput, ModalityImage, CodeServiceError, 1, 0, ErrFileEmpty)
}

// Finish runs the post-generation checks, waits for every outstanding check
// and settles the turn. Input checks already queued still complete after a
// cancelled or failed generation, so callers always call Finish.
func (r *Run) Finish(ctx context.Context, out Output) Outcome {
	if r == nil {
		return Outcome{State: StateNotRequired, Embeddable: true}
	}
	if r.configMissing {
		r.s.setRunState(ctx, r, StateFailedOpen, "", "")
		return Outcome{State: StateFailedOpen, Embeddable: true}
	}
	if !r.active {
		return Outcome{State: StateNotRequired, Embeddable: true}
	}
	defer r.Close()
	s := r.s

	s.setRunState(ctx, r, StateModerating, "", "")
	s.emit(ctx, r.info.RunID, StreamEvent{Type: EventTypeChecking})

	r.mu.Lock()
	r.finishing = true
	r.mu.Unlock()

	if text := composeOutputText(out); text != "" && len(r.cfg.Policy.Selected(DirectionOutput, ModalityText)) > 0 {
		r.submit(&textTask{taskBase: taskBase{r: r}, direction: DirectionOutput, text: text})
	}
	if len(out.Images) > 0 && len(r.cfg.Policy.Selected(DirectionOutput, ModalityImage)) > 0 {
		r.submit(&imageTask{taskBase: taskBase{r: r}, direction: DirectionOutput, refs: out.Images})
	}

	timedOut := r.waitAll(ctx)

	r.mu.Lock()
	r.settled = true
	blk := r.effectiveBlockLocked()
	failed := r.failures > 0 || timedOut
	r.mu.Unlock()

	if blk != nil {
		s.applyBlock(ctx, r, *blk)
		return r.blockedOutcome(*blk)
	}
	state := StatePassed
	if failed {
		state = StateFailedOpen
	}
	s.setRunState(ctx, r, state, "", "")
	return Outcome{State: state, Embeddable: true}
}

func (r *Run) blockedOutcome(b blockInfo) Outcome {
	o := Outcome{State: StateBlocked, Blocked: true, EventID: b.EventID, Direction: b.Direction, Categories: b.Categories}
	if r.info.PaidReservation {
		o.BilledReason = BilledReasonUpstreamUsage
	}
	return o
}

// waitAll waits for all outstanding checks, bounded by the wait budget and the
// caller's context. It reports whether it gave up.
func (r *Run) waitAll(ctx context.Context) (timedOut bool) {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(r.s.budgetFn(r.cfg.TimeoutSeconds))
	defer timer.Stop()
	select {
	case <-done:
		return false
	case <-timer.C:
		return true
	case <-ctx.Done():
		grace := time.NewTimer(graceAfterCtx)
		defer grace.Stop()
		select {
		case <-done:
			return false
		case <-grace.C:
			return true
		}
	}
}

// effectiveBlockLocked picks the block to report: an input hit wins over an
// output hit. r.mu must be held.
func (r *Run) effectiveBlockLocked() *blockInfo {
	switch {
	case r.inputHit != nil:
		b := *r.inputHit
		return &b
	case r.outputHit != nil:
		b := *r.outputHit
		return &b
	}
	return nil
}

// registerHit notes a hit. An input hit cancels generation; a hit that
// arrives after the turn settled is applied immediately.
func (r *Run) registerHit(b blockInfo, ctx context.Context) {
	r.mu.Lock()
	if b.Direction == DirectionInput {
		if r.inputHit == nil {
			r.inputHit = &b
		}
	} else if r.outputHit == nil {
		r.outputHit = &b
	}
	settled := r.settled
	blk := r.effectiveBlockLocked()
	r.mu.Unlock()

	if b.Direction == DirectionInput {
		r.cancelGeneration()
	}
	if settled && blk != nil {
		r.s.applyBlock(ctx, r, *blk)
	}
}

func (r *Run) cancelGeneration() {
	r.cancelOnce.Do(func() {
		if r.opts.CancelGeneration != nil {
			r.opts.CancelGeneration()
		}
	})
}

// ---- executing checks -------------------------------------------------------------

func (s *Service) persistCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return detachedCtx(ctx, persistTimeout)
}

func (r *Run) newEvent(direction, modality, result string) *Event {
	now := r.s.clock.Now()
	return &Event{
		PublicID: newPublicID(), UserID: r.info.UserID, ConversationID: r.info.ConversationID,
		RunID: r.info.RunID, MessageID: r.info.MessageID, MessagePublicID: r.info.MessagePublicID,
		Direction: direction, Modality: modality, ModelName: r.cfg.Model, PolicyVersion: r.cfg.Policy.Version,
		Result: result, CategoriesJSON: "[]", CategoryScoresJSON: "{}", ContentLocationJSON: "{}",
		ImageMetaJSON: "[]", ContentExpiresAt: now, MetadataExpiresAt: now.Add(metadataRetention),
	}
}

func (r *Run) runText(ctx context.Context, direction, text string) {
	s := r.s
	if s.provider == nil {
		r.recordFailure(ctx, direction, ModalityText, CodeServiceError, 1, 0, errors.New("no provider"))
		return
	}
	selected := r.cfg.Policy.Selected(direction, ModalityText)
	start := s.clock.Now()
	resp, err := s.provider.CheckText(ctx, r.cfg.clientConfig(), text, Scope{Direction: direction, Modality: ModalityText, Selected: selected})
	latency := s.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		r.recordFailure(ctx, direction, ModalityText, errorCodeOf(err), 1, latency, err)
		return
	}
	model := r.cfg.Model
	if resp != nil && resp.Model != "" {
		model = resp.Model
	}
	dec := decideHit(resp, selected, ModalityText)
	chunkCount := len(splitText(text, maxTextChunkBytes))
	loc := contentLocation{Field: fieldFor(direction, ModalityText)}
	if !dec.Hit {
		r.recordPass(ctx, direction, ModalityText, model, 1, latency, loc)
		return
	}
	if chunkCount > 1 {
		loc.ChunkIndex, loc.ChunkCount = dec.FirstResult, chunkCount
	}
	pctx, cancel := r.s.persistCtx(ctx)
	defer cancel()
	e := r.newEvent(direction, ModalityText, ResultHit)
	e.ModelName, e.LatencyMS = model, latency
	e.CategoriesJSON = categoriesToJSON(dec.Categories)
	e.CategoryScoresJSON = mustJSON(dec.Scores, "{}")
	e.ContentLocationJSON = mustJSON(loc, "{}")
	e.ContentSummary = summaryFor(direction, ModalityText, ResultHit)
	e.ContentExpiresAt = s.clock.Now().Add(contentRetention)
	if enc, err := s.secrets.EncryptBound(text, textBinding(e.PublicID)); err != nil {
		s.log.Error("content_moderation_text_encrypt_failed", errField(err))
	} else {
		e.EncryptedText = enc
	}
	eventID := e.PublicID
	if err := s.createEvent(pctx, e); err != nil {
		s.log.Error("content_moderation_event_write_failed", errField(err))
		eventID = ""
	}
	r.addStats(pctx, statDeltasFor(direction, ModalityText, ResultHit, 1, latency, dec.Categories))
	r.registerHit(blockInfo{EventID: eventID, Direction: direction, Categories: dec.Categories}, pctx)
}

func (r *Run) addStats(ctx context.Context, deltas []statDelta) {
	if err := r.s.addStats(ctx, deltas...); err != nil {
		r.s.log.Error("content_moderation_stats_failed", errField(err))
	}
}

func (r *Run) recordPass(ctx context.Context, direction, modality, model string, items int, latency int64, loc contentLocation) {
	pctx, cancel := r.s.persistCtx(ctx)
	defer cancel()
	e := r.newEvent(direction, modality, ResultPassed)
	e.ModelName, e.LatencyMS = model, latency
	e.ContentLocationJSON = mustJSON(loc, "{}")
	e.ContentSummary = summaryFor(direction, modality, ResultPassed)
	if err := r.s.createEvent(pctx, e); err != nil {
		r.s.log.Error("content_moderation_event_write_failed", errField(err))
	}
	r.addStats(pctx, statDeltasFor(direction, modality, ResultPassed, items, latency, nil))
}
