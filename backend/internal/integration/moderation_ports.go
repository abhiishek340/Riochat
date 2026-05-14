package integration

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/contentmoderation"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/objectstore"
)

// This file connects three owners that never import each other: the
// moderation engine (which declares RunStore, FileStore, ObjectStore and
// Notifier), the conversation area (which owns runs, messages, files and the
// live streams and offers them through ModerationBridge) and the shared object
// store (local disk or S3-compatible).

var errModerationNotMounted = errors.New("integration: conversation moderation bridge is not mounted")

// objectManager returns the shared object store, built on first use from the
// application configuration. The manager follows configuration changes.
func (h *Hub) objectManager(d deps.Deps) *objectstore.Manager {
	h.storeMu.Lock()
	defer h.storeMu.Unlock()
	if h.store == nil {
		h.store = objectstore.NewManager(objectstore.StorageOf(d.Config), nil, nil)
	}
	return h.store
}

// ---- object store adapters ------------------------------------------------------

// moderationObjects keeps the encrypted evidence images.
type moderationObjects struct{ m *objectstore.Manager }

func (o moderationObjects) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := o.m.Write(ctx, key, bytes.NewReader(data), int64(len(data)), contentType)
	return err
}

func (o moderationObjects) Get(ctx context.Context, key string) ([]byte, error) {
	b, _, err := objectstore.ReadAll(ctx, o.m, key, 0)
	return b, err
}

func (o moderationObjects) Delete(ctx context.Context, key string) error {
	return o.m.Delete(ctx, key)
}

// chatObjects keeps the bytes of chat files.
type chatObjects struct{ m *objectstore.Manager }

func (o chatObjects) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := o.m.Write(ctx, key, bytes.NewReader(data), int64(len(data)), contentType)
	return err
}

func (o chatObjects) Get(ctx context.Context, key string) ([]byte, error) {
	b, _, err := objectstore.ReadAll(ctx, o.m, key, 0)
	if errors.Is(err, objectstore.ErrNotFound) {
		return nil, conversation.ErrObjectNotFound
	}
	return b, err
}

func (o chatObjects) Delete(ctx context.Context, key string) error {
	return o.m.Delete(ctx, key)
}

// ---- conversation bridge adapters ---------------------------------------------------

func (h *Hub) bridge() (*conversation.ModerationBridge, error) {
	if h.modBridge == nil {
		return nil, errModerationNotMounted
	}
	return h.modBridge, nil
}

// moderationRuns lets the engine reach run and message rows.
type moderationRuns struct{ h *Hub }

func (r moderationRuns) SetRunState(ctx context.Context, runID, state, eventID, categoriesJSON string) error {
	b, err := r.h.bridge()
	if err != nil {
		return err
	}
	return b.SetRunState(ctx, runID, state, eventID, categoriesJSON)
}

func (r moderationRuns) ApplyBlock(ctx context.Context, spec contentmoderation.BlockSpec) ([]uint, error) {
	b, err := r.h.bridge()
	if err != nil {
		return nil, err
	}
	return b.ApplyBlock(ctx, spec.RunID, spec.EventID, spec.Direction, spec.Categories)
}

func (r moderationRuns) StaleRuns(ctx context.Context, cutoff time.Time) ([]contentmoderation.RunRef, error) {
	b, err := r.h.bridge()
	if err != nil {
		return nil, err
	}
	runs, err := b.StaleRuns(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	out := make([]contentmoderation.RunRef, 0, len(runs))
	for _, s := range runs {
		out = append(out, contentmoderation.RunRef{
			RunID: s.RunID, UserID: s.UserID, ConversationID: s.ConversationID,
			MessageID: s.MessageID, MessagePublicID: s.MessagePublicID,
		})
	}
	return out, nil
}

// moderationFiles lets the engine load, revoke and physically remove files.
type moderationFiles struct{ h *Hub }

func (f moderationFiles) Load(ctx context.Context, fileID uint) ([]byte, string, error) {
	b, err := f.h.bridge()
	if err != nil {
		return nil, "", err
	}
	data, mime, err := b.LoadFile(ctx, fileID)
	switch {
	case errors.Is(err, conversation.ErrModerationNotImage):
		return nil, mime, contentmoderation.ErrNotImage
	case errors.Is(err, conversation.ErrFileNotFound):
		return nil, mime, contentmoderation.ErrFileNotFound
	}
	return data, mime, err
}

func (f moderationFiles) Revoke(ctx context.Context, fileID uint) error {
	b, err := f.h.bridge()
	if err != nil {
		return err
	}
	return b.RevokeFile(ctx, fileID)
}

func (f moderationFiles) DeleteObjects(ctx context.Context, ids []uint) error {
	b, err := f.h.bridge()
	if err != nil {
		return err
	}
	return b.DeleteFileObjects(ctx, ids)
}

func (f moderationFiles) RetryBlockedDeletes(ctx context.Context, limit int) error {
	b, err := f.h.bridge()
	if err != nil {
		return err
	}
	return b.RetryBlockedDeletes(ctx, limit)
}

// moderationNotifier delivers engine events to the run streams.
type moderationNotifier struct{ h *Hub }

func (n moderationNotifier) Emit(ctx context.Context, runID string, ev contentmoderation.StreamEvent) {
	b, err := n.h.bridge()
	if err != nil {
		return
	}
	var fields map[string]any
	if ev.Type == contentmoderation.EventTypeBlocked {
		cats := ev.Categories
		if cats == nil {
			cats = []string{}
		}
		fields = map[string]any{"eventID": ev.EventID, "direction": ev.Direction, "categories": cats}
		if ev.BilledReason != "" {
			fields["billedReason"] = ev.BilledReason
		}
	}
	b.Emit(ctx, runID, ev.Type, fields)
}

func (n moderationNotifier) DiscardRetained(ctx context.Context, runID string) {
	if b, err := n.h.bridge(); err == nil {
		b.DiscardRetained(ctx, runID)
	}
}

// ---- conversation: tracked moderation -----------------------------------------------

// Begin starts engine tracking of a persisted run. It returns nil when
// moderation is off for the run, in which case the conversation area has
// nothing to check or settle.
func (m convModeration) Begin(ctx context.Context, b conversation.ModerationBegin) conversation.ModerationSession {
	if m.h.mod == nil {
		return nil
	}
	run := m.h.mod.BeginRun(ctx, contentmoderation.RunInfo{
		UserID: b.UserID, ConversationID: b.ConversationID, RunID: b.RunID, MessageID: b.MessageID,
		MessagePublicID: b.MessagePublicID, PaidReservation: b.PaidReservation,
	}, contentmoderation.RunOptions{CancelGeneration: b.CancelGeneration})
	if !run.Active() {
		run.Close()
		return nil
	}
	return &moderationSession{run: run}
}

type moderationSession struct{ run *contentmoderation.Run }

func (s *moderationSession) CheckInputText(text string) { s.run.CheckInputText(text) }

func (s *moderationSession) CheckInputImages(files []conversation.ModerationFile) {
	refs := make([]contentmoderation.ImageRef, 0, len(files))
	for _, f := range files {
		refs = append(refs, contentmoderation.ImageRef{FileID: f.ObjectID, SHA256: f.SHA256})
	}
	s.run.CheckInputImages(refs)
}

func (s *moderationSession) ReportOutputImageFailure(ctx context.Context) {
	s.run.ReportOutputImageFailure(ctx)
}

func (s *moderationSession) Finish(ctx context.Context, out conversation.ModerationOutput) conversation.ModerationVerdict {
	o := contentmoderation.Output{AssistantText: out.Text, ReasoningText: out.Reasoning}
	for _, img := range out.Images {
		o.Images = append(o.Images, contentmoderation.ImageRef{Bytes: img.Bytes, Name: img.Name})
	}
	res := s.run.Finish(ctx, o)
	return conversation.ModerationVerdict{
		Blocked: res.Blocked, EventID: res.EventID, Direction: res.Direction,
		Categories: res.Categories, BilledReason: res.BilledReason,
	}
}

func (s *moderationSession) Close() { s.run.Close() }
