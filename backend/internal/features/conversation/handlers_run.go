package conversation

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// beginSend validates the body and prepares the pending pair. ok is false when
// the response has been written.
func (a *api) beginSend(c *gin.Context, stream bool) (*pendingSend, bool) {
	var req sendRequest
	if !httpx.BindJSON(c, &req) {
		return nil, false
	}
	p := middleware.MustPrincipal(c)
	prep, err := a.s.normalizeSend(c.Request.Context(), req, p.IsAdmin())
	if err != nil {
		a.fail(c, err)
		return nil, false
	}
	return a.beginPrepared(c, p.UserID, p.IsAdmin(), c.Param("id"), prep, stream)
}

func (a *api) beginPrepared(c *gin.Context, userID uint, isAdmin bool, convID string, prep preparedRequest, stream bool) (*pendingSend, bool) {
	if stream && a.s.lc.Drain().Draining() {
		a.fail(c, ErrServiceUnavailable)
		return nil, false
	}
	prep.InStream = stream && prep.Media == nil
	requestID := reqctx.FromRequest(c.Request).RequestID()
	ps, err := a.s.prepareSend(c.Request.Context(), userID, isAdmin, convID, requestID, prep)
	if err != nil {
		if prep.InStream && a.streamRejection(c, ps, requestID, prep, err) {
			return nil, false
		}
		a.fail(c, err)
		return nil, false
	}
	return ps, true
}

// streamRejection answers a refused streaming send the way a failed run is
// answered: HTTP 200 with a single terminal error event (after a file_proc
// notice for a bad attachment). It reports whether it wrote the response.
func (a *api) streamRejection(c *gin.Context, ps *pendingSend, requestID string, prep preparedRequest, err error) bool {
	te, ok := apperr.Find(err)
	if !ok || !apperr.Registered(te.Code) {
		return false
	}
	switch te.Code {
	case ErrModelRouteMissing.Code, ErrModelAccessDenied.Code, ErrInvalidBranch.Code, ErrKBUnavailable.Code,
		ErrFileInvalidRef.Code, ErrRunAlreadyExists.Code:
	default:
		return false
	}
	f := classifyFailure(err)
	var data *SendResult
	if ps != nil {
		if res, rerr := a.s.sendResult(c.Request.Context(), ps); rerr == nil {
			data = res
		}
	}
	setNDJSONHeaders(c)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	w := &lineWriter{c: c}
	seq := 0
	if te.Code == ErrFileInvalidRef.Code && len(prep.FileIDs) > 0 {
		seq++
		w.write([]byte(marshalNoEscape(map[string]any{"type": "file_proc", "message": "processing attachments", "seq": seq})))
	}
	ev := errorEvent(f, requestID, data)
	ev["type"] = evError
	seq++
	ev["seq"] = seq
	w.write([]byte(marshalNoEscape(ev)))
	return true
}

func (a *api) sendMessage(c *gin.Context) {
	ps, ok := a.beginSend(c, false)
	if !ok {
		return
	}
	_, out := a.s.startRun(c.Request.Context(), ps)
	a.s.auditEntry(c, ps.userID, "send_message", "conversation", ps.conv.PublicID, map[string]any{"run_id": ps.req.RunID})
	select {
	case o := <-out:
		a.writeOutcome(c, o)
	case <-c.Request.Context().Done():
		// The client left; generation continues and is persisted.
	}
}

func (a *api) writeOutcome(c *gin.Context, o execOutcome) {
	if o.Fail != nil {
		if o.Billable && o.Result != nil && !o.Fail.Canceled {
			httpx.OK(c, o.Result)
			return
		}
		httpx.Fail(c, o.Fail.Status, o.Fail.asError())
		return
	}
	httpx.OK(c, o.Result)
}

func (a *api) streamMessage(c *gin.Context) {
	ps, ok := a.beginSend(c, true)
	if !ok {
		return
	}
	lr, _ := a.s.startRun(c.Request.Context(), ps)
	a.s.auditAsync(c, ps.userID, "stream_message", "conversation", ps.conv.PublicID, map[string]any{"run_id": ps.req.RunID})
	a.s.pumpRun(c, lr.runID, 0, false)
}

// resume handles GET /conversation-runs/:run_id/stream.
func (a *api) resume(c *gin.Context) {
	runID, ok := validRunParam(c.Param("run_id"))
	if !ok {
		a.fail(c, ErrRunInvalidID)
		return
	}
	after := 0
	if raw := strings.TrimSpace(c.Query("after")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			after = n
		}
	}
	snapshot := c.Query("snapshot") == "true"
	userID := uid(c)
	view, _, _, found := a.s.hub.fetchEvents(c.Request.Context(), runID, 0)
	if !found {
		a.s.markUnknownRunInterrupted(c.Request.Context(), userID, runID)
		httpx.Fail(c, http.StatusNotFound, ErrStreamNotFound)
		return
	}
	if view.userID != userID {
		httpx.Fail(c, http.StatusNotFound, ErrStreamNotFound)
		return
	}
	a.s.pumpRun(c, runID, after, snapshot)
}

func (a *api) cancel(c *gin.Context) {
	runID, ok := validRunParam(c.Param("run_id"))
	if !ok {
		a.fail(c, ErrRunInvalidID)
		return
	}
	httpx.OK(c, gin.H{"canceled": a.s.hub.cancel(uid(c), runID)})
}

func (a *api) userRunsStream(c *gin.Context) { a.s.pumpUserRuns(c, uid(c)) }

// ----- media -----

func (a *api) media(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req mediaRequest
		if kind == taskVideoExtension {
			var ext mediaExtensionRequest
			if !httpx.BindJSON(c, &ext) {
				return
			}
			req = ext.common()
		} else if !httpx.BindJSON(c, &req) {
			return
		}
		p := middleware.MustPrincipal(c)
		ctx := c.Request.Context()
		prep, err := a.s.normalizeMedia(ctx, p.UserID, p.IsAdmin(), kind, req)
		if err != nil {
			a.fail(c, err)
			return
		}
		if err := a.s.ensureMediaStorage(ctx, p.UserID); err != nil {
			a.fail(c, err)
			return
		}
		ps, ok := a.beginPrepared(c, p.UserID, p.IsAdmin(), c.Param("id"), prep, true)
		if !ok {
			return
		}
		lr, _ := a.s.startRun(ctx, ps)
		a.s.auditAsync(c, p.UserID, "stream_message", "conversation", ps.conv.PublicID, map[string]any{"run_id": ps.req.RunID, "task": kind})
		a.s.pumpRun(c, lr.runID, 0, false)
	}
}

func (a *api) temporaryStream(c *gin.Context) {
	if a.s.lc.Drain().Draining() {
		a.fail(c, ErrServiceUnavailable)
		return
	}
	req, atts, idx, ok := a.s.parseTemporary(c)
	if !ok {
		return
	}
	vt, err := a.s.validateTemporary(c.Request.Context(), req, atts, idx)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.runTemporary(c, vt)
}
