package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

func setNDJSONHeaders(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
}

// lineWriter writes NDJSON lines and reports whether the client is still there.
type lineWriter struct {
	c    *gin.Context
	gone bool
}

func (w *lineWriter) write(line []byte) bool {
	if w.gone {
		return false
	}
	if _, err := w.c.Writer.Write(append(append([]byte(nil), line...), '\n')); err != nil {
		w.gone = true
		return false
	}
	w.c.Writer.Flush()
	return true
}

// pumpRun streams a run's events to the client: replay after `after`, then live
// events, ending at the first terminal event. Generation is never affected by
// the client leaving.
func (s *service) pumpRun(c *gin.Context, runID string, after int, snapshot bool) {
	ctx := c.Request.Context()
	setNDJSONHeaders(c)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	c.Writer.Flush()
	w := &lineWriter{c: c}

	if snapshot {
		view, all, _, found := s.hub.fetchEvents(ctx, runID, 0)
		if found {
			var content, think strings.Builder
			if view.local != nil {
				// The in-process builders hold the complete text even after
				// old events fell out of the bounded buffer.
				view.local.mu.Lock()
				content.WriteString(view.local.content.String())
				think.WriteString(view.local.think.String())
				view.local.mu.Unlock()
			} else {
				for _, ev := range all {
					var probe struct {
						Type  string `json:"type"`
						Delta string `json:"delta"`
					}
					if json.Unmarshal(ev.Line, &probe) != nil {
						continue
					}
					switch probe.Type {
					case "delta":
						content.WriteString(probe.Delta)
					case "upstream_think_delta":
						think.WriteString(probe.Delta)
					}
				}
			}
			line := marshalNoEscape(map[string]any{"type": "snapshot", "seq": after, "content": content.String(), "think": think.String()})
			if !w.write([]byte(line)) {
				return
			}
		}
	}

	cur := after
	// Loop pacing uses wall time on purpose: it is not business time.
	var lastCheck time.Time
	drain := s.lc.Drain().Done()
	for {
		view, evs, notify, found := s.hub.fetchEvents(ctx, runID, cur)
		if !found {
			return
		}
		for _, ev := range evs {
			if !w.write(ev.Line) {
				return
			}
			cur = ev.Seq
			if ev.Terminal {
				return
			}
		}
		if view.done && len(evs) == 0 {
			return
		}
		if time.Since(lastCheck) >= s.timing.leaseCheck {
			lastCheck = time.Now()
			if !s.hub.leaseAlive(ctx, runID) {
				s.interruptRun(context.WithoutCancel(ctx), runID, view)
				continue
			}
		}
		wait := s.timing.remotePoll
		if notify != nil {
			wait = s.timing.leaseCheck
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-drain:
			timer.Stop()
			return
		case <-notify:
			timer.Stop()
		case <-timer.C:
		}
	}
}

var errInterrupted = failure{Status: http.StatusServiceUnavailable, Code: ErrStreamInterrupted.Code, Message: ErrStreamInterrupted.Message}

// interruptRun closes a run whose lease vanished: pending messages become
// errors and a terminal error event references the original request.
func (s *service) interruptRun(ctx context.Context, runID string, view runView) {
	now := s.now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{"status": msgError, "error_code": errInterrupted.Code, "error_message": truncateRunes(errInterrupted.Message, 255)}
		if err := tx.Model(&Message{}).Where("run_id = ? AND status = ?", runID, msgPending).Updates(upd).Error; err != nil {
			return err
		}
		return tx.Model(&Run{}).Where("run_id = ? AND status = ?", runID, runRunning).
			Updates(map[string]any{"status": runInterrupted, "ended_at": now, "error_code": errInterrupted.Code, "error_message": truncateRunes(errInterrupted.Message, 255)}).Error
	})
	if err != nil {
		s.log.Warn("conversation_interrupt_persist_failed", logErr(err))
	}
	ev := errorEvent(errInterrupted, view.requestID, nil)
	if view.local != nil {
		s.hub.publish(view.local, evError, ev)
		view.local.mu.Lock()
		cancel := view.local.cancel
		view.local.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return
	}
	// Remote run: append the terminal event to the shared buffer.
	seq := view.lastSeq + 1
	obj := map[string]any{"type": evError, "seq": seq}
	for k, v := range ev {
		obj[k] = v
	}
	line := []byte(marshalNoEscape(obj))
	ok, _ := s.cache.SetNX(ctx, runKey(runID, "ev:"+strconv.Itoa(seq)), line, s.timing.keepAfterFinish)
	if !ok {
		return
	}
	meta := runMeta{UserID: view.userID, ConvPublicID: view.convPublicID, RequestID: view.requestID, LastSeq: seq, Done: true}
	raw, _ := json.Marshal(meta)
	_ = s.cache.Set(ctx, runKey(runID, "meta"), raw, s.timing.keepAfterFinish)
}

// markUnknownRunInterrupted closes pending messages of a run whose buffer is gone.
func (s *service) markUnknownRunInterrupted(ctx context.Context, userID uint, runID string) {
	now := s.now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{"status": msgInterrupted, "error_code": errInterrupted.Code, "error_message": truncateRunes(errInterrupted.Message, 255)}
		if err := tx.Model(&Message{}).Where("run_id = ? AND user_id = ? AND status = ?", runID, userID, msgPending).Updates(upd).Error; err != nil {
			return err
		}
		return tx.Model(&Run{}).Where("run_id = ? AND user_id = ? AND status = ?", runID, userID, runRunning).
			Updates(map[string]any{"status": runInterrupted, "ended_at": now, "error_code": errInterrupted.Code}).Error
	})
	if err != nil {
		s.log.Warn("conversation_interrupt_persist_failed", logErr(err))
	}
}

// pumpUserRuns serves GET /conversation-runs/stream as server-sent events.
func (s *service) pumpUserRuns(c *gin.Context, userID uint) {
	ctx := c.Request.Context()
	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	c.Writer.Flush()

	ch, unsubscribe := s.hub.subscribeUser(userID)
	defer unsubscribe()
	send := func(obj map[string]any) bool {
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", marshalNoEscape(obj)); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}
	sendSnapshot := func() bool {
		runs, err := s.activeRuns(ctx, userID)
		if err != nil {
			if _, werr := fmt.Fprint(c.Writer, ": keepalive\n\n"); werr != nil {
				return false
			}
			c.Writer.Flush()
			return true
		}
		return send(map[string]any{"type": "snapshot", "runs": runs})
	}
	if !sendSnapshot() {
		return
	}
	ticker := time.NewTicker(s.timing.userSnapshot)
	defer ticker.Stop()
	drain := s.lc.Drain().Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-drain:
			return
		case ev := <-ch:
			if !send(map[string]any{"type": ev.Type, "runID": ev.RunID, "conversationPublicID": ev.ConvPublicID}) {
				return
			}
		case <-ticker.C:
			if !sendSnapshot() {
				return
			}
		}
	}
}

var _ = reqctx.RoleAdmin
