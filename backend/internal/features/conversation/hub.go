package conversation

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// timings holds the intervals of the generation lifecycle. Tests shorten them.
type timings struct {
	leaseTTL        time.Duration
	leaseRefresh    time.Duration
	leaseCheck      time.Duration
	cancelPoll      time.Duration
	userSnapshot    time.Duration
	remotePoll      time.Duration
	keepAfterFinish time.Duration
	activeTTL       time.Duration
	maxEvents       int
	maxEventBytes   int
}

func defaultTimings() timings {
	return timings{
		leaseTTL: 30 * time.Second, leaseRefresh: 10 * time.Second, leaseCheck: 5 * time.Second,
		cancelPoll: time.Second, userSnapshot: 20 * time.Second, remotePoll: 200 * time.Millisecond,
		keepAfterFinish: 15 * time.Minute, activeTTL: 2 * time.Hour, maxEvents: 1024, maxEventBytes: 128 * 1024,
	}
}

// Event types that end a stream.
const (
	evCompleted         = "completed"
	evError             = "error"
	evModerationBlocked = "moderation_blocked"
)

func isTerminalType(t string) bool {
	return t == evCompleted || t == evError || t == evModerationBlocked
}

// runEvent is one buffered stream event; Line is its JSON encoding.
type runEvent struct {
	Seq      int
	Type     string
	Line     []byte
	Terminal bool
}

// liveRun is the in-process state of one generation.
type liveRun struct {
	mu              sync.Mutex
	runID           string
	userID          uint
	convPublicID    string
	requestID       string
	userMsgPublicID string
	asstMsgPublicID string
	events          []runEvent
	lastSeq         int
	done            bool
	doneAt          time.Time
	notify          chan struct{}
	cancel          context.CancelFunc
	canceledByUser  bool
	terminal        bool
	// holdBlocked makes the moderation bridge leave the terminal blocked event
	// to the run's own job, which publishes it with the final result.
	holdBlocked bool
	content     strings.Builder
	think       strings.Builder
}

type runMeta struct {
	UserID       uint   `json:"userID"`
	ConvPublicID string `json:"convPublicID"`
	RequestID    string `json:"requestID"`
	LastSeq      int    `json:"lastSeq"`
	Done         bool   `json:"done"`
}

type userRunEvent struct {
	Type         string
	RunID        string
	ConvPublicID string
}

// runHub tracks live runs, their event buffers and per-user subscribers.
type runHub struct {
	s        *service
	mu       sync.Mutex
	runs     map[string]*liveRun
	userSubs map[uint]map[chan userRunEvent]struct{}
}

func newRunHub(s *service) *runHub {
	return &runHub{s: s, runs: map[string]*liveRun{}, userSubs: map[uint]map[chan userRunEvent]struct{}{}}
}

func runKey(runID, suffix string) string { return "conversation:run:" + runID + ":" + suffix }

func (h *runHub) get(runID string) *liveRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	lr := h.runs[runID]
	if lr == nil {
		return nil
	}
	lr.mu.Lock()
	expired := lr.done && h.s.now().Sub(lr.doneAt) > h.s.timing.keepAfterFinish
	lr.mu.Unlock()
	if expired {
		delete(h.runs, runID)
		return nil
	}
	return lr
}

func (h *runHub) purgeLocked() {
	now := h.s.now()
	for id, lr := range h.runs {
		lr.mu.Lock()
		expired := lr.done && now.Sub(lr.doneAt) > h.s.timing.keepAfterFinish
		lr.mu.Unlock()
		if expired {
			delete(h.runs, id)
		}
	}
}

// register adds a run, claims its lease and announces it.
func (h *runHub) register(lr *liveRun) {
	lr.notify = make(chan struct{})
	h.mu.Lock()
	h.purgeLocked()
	h.runs[lr.runID] = lr
	h.mu.Unlock()
	ctx := context.Background()
	_ = h.s.cache.Set(ctx, runKey(lr.runID, "lease"), []byte("1"), h.s.timing.leaseTTL)
	h.mirrorMeta(lr, false)
	h.notifyUsers(lr.userID, userRunEvent{Type: "started", RunID: lr.runID, ConvPublicID: lr.convPublicID})
}

func (h *runHub) mirrorMeta(lr *liveRun, done bool) {
	lr.mu.Lock()
	m := runMeta{UserID: lr.userID, ConvPublicID: lr.convPublicID, RequestID: lr.requestID, LastSeq: lr.lastSeq, Done: done || lr.done}
	lr.mu.Unlock()
	raw, _ := json.Marshal(m)
	ttl := h.s.timing.activeTTL
	if m.Done {
		ttl = h.s.timing.keepAfterFinish
	}
	if err := h.s.cache.Set(context.Background(), runKey(lr.runID, "meta"), raw, ttl); err != nil {
		h.s.log.Debug("conversation_run_mirror_failed", logx.Err(err))
	}
}

// publish appends an event to the run buffer and wakes subscribers. The first
// terminal event wins; later ones are dropped.
func (h *runHub) publish(lr *liveRun, typ string, fields map[string]any) bool {
	terminal := isTerminalType(typ)
	lr.mu.Lock()
	if lr.terminal {
		lr.mu.Unlock()
		return false
	}
	lr.lastSeq++
	seq := lr.lastSeq
	obj := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		obj[k] = v
	}
	obj["type"] = typ
	obj["seq"] = seq
	line, err := encodeLine(obj)
	if err != nil || (len(line) > h.s.timing.maxEventBytes && !terminal) {
		line, _ = encodeLine(map[string]any{"type": typ, "seq": seq, "truncated": true})
	}
	if len(line) > h.s.timing.maxEventBytes && terminal {
		line, _ = encodeLine(map[string]any{"type": typ, "seq": seq, "truncated": true, "status": fields["status"], "errorCode": fields["errorCode"], "message": fields["message"]})
	}
	ev := runEvent{Seq: seq, Type: typ, Line: line, Terminal: terminal}
	if typ == "delta" {
		if d, ok := fields["delta"].(string); ok {
			lr.content.WriteString(d)
		}
	} else if typ == "upstream_think_delta" {
		if d, ok := fields["delta"].(string); ok {
			lr.think.WriteString(d)
		}
	}
	lr.events = append(lr.events, ev)
	if len(lr.events) > h.s.timing.maxEvents {
		// Keep the newest events; terminal events are always newest.
		lr.events = append([]runEvent(nil), lr.events[len(lr.events)-h.s.timing.maxEvents:]...)
	}
	if terminal {
		lr.terminal = true
	}
	old := lr.notify
	lr.notify = make(chan struct{})
	lr.mu.Unlock()
	close(old)

	ctx := context.Background()
	_ = h.s.cache.Set(ctx, runKey(lr.runID, "ev:"+strconv.Itoa(seq)), line, h.s.timing.activeTTL)
	h.mirrorMeta(lr, false)
	return true
}

func encodeLine(obj map[string]any) ([]byte, error) {
	return []byte(marshalNoEscape(obj)), nil
}

// finish marks the run done, drops its lease and re-ages the mirrored events.
func (h *runHub) finish(lr *liveRun) {
	lr.mu.Lock()
	lr.done = true
	lr.terminal = true
	lr.doneAt = h.s.now()
	last := lr.lastSeq
	events := append([]runEvent(nil), lr.events...)
	old := lr.notify
	lr.notify = make(chan struct{})
	lr.mu.Unlock()
	close(old)
	ctx := context.Background()
	_ = h.s.cache.Delete(ctx, runKey(lr.runID, "lease"), runKey(lr.runID, "cancel"))
	for _, ev := range events {
		_ = h.s.cache.Set(ctx, runKey(lr.runID, "ev:"+strconv.Itoa(ev.Seq)), ev.Line, h.s.timing.keepAfterFinish)
	}
	_ = last
	h.mirrorMeta(lr, true)
	h.notifyUsers(lr.userID, userRunEvent{Type: "finished", RunID: lr.runID, ConvPublicID: lr.convPublicID})
}

// cancel signals an owned live run. It reports whether a run was signalled.
func (h *runHub) cancel(userID uint, runID string) bool {
	if lr := h.get(runID); lr != nil {
		if lr.userID != userID {
			return false
		}
		lr.mu.Lock()
		running := !lr.done
		if running {
			lr.canceledByUser = true
		}
		cancel := lr.cancel
		lr.mu.Unlock()
		if running && cancel != nil {
			cancel()
			return true
		}
		return false
	}
	// A run on another instance: leave a cancel request for its lease loop.
	ctx := context.Background()
	raw, found, err := h.s.cache.Get(ctx, runKey(runID, "meta"))
	if err != nil || !found {
		return false
	}
	var m runMeta
	if json.Unmarshal(raw, &m) != nil || m.UserID != userID || m.Done {
		return false
	}
	if _, ok, _ := h.s.cache.Get(ctx, runKey(runID, "lease")); !ok {
		return false
	}
	return h.s.cache.Set(ctx, runKey(runID, "cancel"), []byte("1"), h.s.timing.leaseTTL) == nil
}

// leaseLoop refreshes the lease and honours cancel requests until ctx ends.
func (h *runHub) leaseLoop(ctx context.Context, lr *liveRun) {
	refresh := time.NewTicker(h.s.timing.leaseRefresh)
	poll := time.NewTicker(h.s.timing.cancelPoll)
	defer refresh.Stop()
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			_ = h.s.cache.Set(context.Background(), runKey(lr.runID, "lease"), []byte("1"), h.s.timing.leaseTTL)
		case <-poll.C:
			if _, found, err := h.s.cache.Get(context.Background(), runKey(lr.runID, "cancel")); err == nil && found {
				lr.mu.Lock()
				lr.canceledByUser = true
				cancel := lr.cancel
				lr.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				_ = h.s.cache.Delete(context.Background(), runKey(lr.runID, "cancel"))
			}
		}
	}
}

// ----- user subscriptions -----

func (h *runHub) subscribeUser(userID uint) (chan userRunEvent, func()) {
	ch := make(chan userRunEvent, 64)
	h.mu.Lock()
	if h.userSubs[userID] == nil {
		h.userSubs[userID] = map[chan userRunEvent]struct{}{}
	}
	h.userSubs[userID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.userSubs[userID], ch)
		h.mu.Unlock()
	}
}

func (h *runHub) notifyUsers(userID uint, ev userRunEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.userSubs[userID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// ----- reading -----

// runView describes a run as seen by a reader.
type runView struct {
	userID       uint
	convPublicID string
	requestID    string
	lastSeq      int
	done         bool
	local        *liveRun
}

// fetchEvents returns the events after a sequence number. found is false when
// no buffer exists for the run anywhere.
func (h *runHub) fetchEvents(ctx context.Context, runID string, after int) (view runView, evs []runEvent, notify <-chan struct{}, found bool) {
	if lr := h.get(runID); lr != nil {
		lr.mu.Lock()
		defer lr.mu.Unlock()
		view = runView{userID: lr.userID, convPublicID: lr.convPublicID, requestID: lr.requestID, lastSeq: lr.lastSeq, done: lr.done, local: lr}
		for _, ev := range lr.events {
			if ev.Seq > after {
				evs = append(evs, ev)
			}
		}
		return view, evs, lr.notify, true
	}
	raw, ok, err := h.s.cache.Get(ctx, runKey(runID, "meta"))
	if err != nil || !ok {
		return runView{}, nil, nil, false
	}
	var m runMeta
	if json.Unmarshal(raw, &m) != nil {
		return runView{}, nil, nil, false
	}
	view = runView{userID: m.UserID, convPublicID: m.ConvPublicID, requestID: m.RequestID, lastSeq: m.LastSeq, done: m.Done}
	for seq := after + 1; seq <= m.LastSeq; seq++ {
		line, ok, err := h.s.cache.Get(ctx, runKey(runID, "ev:"+strconv.Itoa(seq)))
		if err != nil || !ok {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(line, &probe)
		evs = append(evs, runEvent{Seq: seq, Type: probe.Type, Line: line, Terminal: isTerminalType(probe.Type)})
	}
	return view, evs, nil, true
}

// leaseAlive reports whether some instance still holds the run's lease.
func (h *runHub) leaseAlive(ctx context.Context, runID string) bool {
	_, found, err := h.s.cache.Get(ctx, runKey(runID, "lease"))
	if err != nil {
		return true
	}
	return found
}

// activeRuns lists the user's running runs from the database.
func (s *service) activeRuns(ctx context.Context, userID uint) ([]map[string]string, error) {
	var runs []Run
	if err := s.db.WithContext(ctx).Where("user_id = ? AND status = ?", userID, runRunning).Order("id ASC").Find(&runs).Error; err != nil {
		return nil, err
	}
	out := []map[string]string{}
	if len(runs) == 0 {
		return out, nil
	}
	convIDs := make([]uint, 0, len(runs))
	for _, r := range runs {
		convIDs = append(convIDs, r.ConversationID)
	}
	var convs []Conversation
	if err := s.db.WithContext(ctx).Select("id", "public_id").Where("id IN ?", convIDs).Find(&convs).Error; err != nil {
		return nil, err
	}
	pub := map[uint]string{}
	for _, c := range convs {
		pub[c.ID] = c.PublicID
	}
	for _, r := range runs {
		if !s.hub.leaseAlive(ctx, r.RunID) {
			continue
		}
		out = append(out, map[string]string{"runID": r.RunID, "conversationPublicID": pub[r.ConversationID]})
	}
	return out, nil
}
