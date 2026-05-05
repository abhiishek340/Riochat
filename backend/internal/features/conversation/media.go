package conversation

import (
	"context"
	"strings"
)

const maxImageEditInputs = 16

// normalizeMedia validates a media request and turns it into a send request.
func (s *service) normalizeMedia(ctx context.Context, userID uint, isAdmin bool, kind string, req mediaRequest) (preparedRequest, error) {
	prompt := strings.TrimSpace(req.Prompt)
	mr := &MediaRequest{Kind: kind, Prompt: prompt}
	ids := make([]string, 0, len(req.FileIDs))
	for _, f := range req.FileIDs {
		ids = append(ids, strings.TrimSpace(f))
	}
	mr.InputFileIDs = ids
	mr.MaskFileID = strings.TrimSpace(req.MaskFileID)
	mr.SourceVideoFileID = strings.TrimSpace(req.SourceVideoFileID)

	switch kind {
	case taskImageGeneration:
		if prompt == "" {
			return preparedRequest{}, ErrMediaImagePrompt
		}
		if len(ids) > 0 {
			return preparedRequest{}, ErrMediaImageRejects
		}
	case taskImageEdit:
		if prompt == "" {
			return preparedRequest{}, ErrMediaImagePrompt
		}
		if len(ids) == 0 {
			return preparedRequest{}, ErrMediaEditInput
		}
		if len(ids) > maxImageEditInputs {
			return preparedRequest{}, ErrMediaEditTooMany
		}
		if err := s.requireCategory(ctx, userID, ids, "image", ErrMediaEditInvalid); err != nil {
			return preparedRequest{}, err
		}
		if mr.MaskFileID != "" {
			if err := s.requireCategory(ctx, userID, []string{mr.MaskFileID}, "image", ErrMediaEditInvalid); err != nil {
				return preparedRequest{}, err
			}
		}
	case taskVideoGeneration:
		if prompt == "" {
			return preparedRequest{}, ErrMediaVideoPrompt
		}
		if len(ids) > 1 {
			return preparedRequest{}, ErrMediaVideoTooMany
		}
		if err := s.requireCategory(ctx, userID, ids, "image", ErrMediaVideoInput); err != nil {
			return preparedRequest{}, err
		}
	case taskVideoExtension:
		if prompt == "" {
			return preparedRequest{}, ErrMediaVideoPrompt
		}
		if mr.SourceVideoFileID == "" {
			return preparedRequest{}, ErrMediaVideoInput
		}
		if len(ids) > 1 {
			return preparedRequest{}, ErrMediaVideoTooMany
		}
		if err := s.requireCategory(ctx, userID, []string{mr.SourceVideoFileID}, "video", ErrMediaVideoInput); err != nil {
			return preparedRequest{}, err
		}
		if err := s.requireCategory(ctx, userID, ids, "image", ErrMediaVideoInput); err != nil {
			return preparedRequest{}, err
		}
	default:
		return preparedRequest{}, ErrMediaInvalidTask
	}

	attach := append([]string(nil), ids...)
	if kind == taskVideoExtension {
		attach = append(attach, mr.SourceVideoFileID)
	}
	p := preparedRequest{
		ContentType: "text", Content: prompt, Model: strings.TrimSpace(req.Model),
		Options: stripReservedOptions(req.Options), Controls: map[string]any{},
		RunID: normalizeRunID(req.ClientRunID), FileIDs: dedupeStrings(attach),
		ParentPub: strings.TrimSpace(req.ParentMessagePublicID), SourcePub: strings.TrimSpace(req.SourceMessagePublicID),
		Reason: strings.TrimSpace(req.BranchReason), UIBlockVersion: 1, Media: mr,
	}
	p.ReasoningEffort = reasoningEffortOf(nil, p.Options)
	return p, nil
}

// requireCategory checks that every file exists for the user and has a category.
func (s *service) requireCategory(ctx context.Context, userID uint, ids []string, category string, bad error) error {
	for _, id := range ids {
		if id == "" {
			return ErrFileInvalidRef
		}
		f, err := s.getFile(ctx, userID, id)
		if err != nil {
			return err
		}
		if f.FileCategory != category {
			return bad
		}
	}
	return nil
}

// ensureMediaStorage verifies that generated media can be stored for the user.
func (s *service) ensureMediaStorage(ctx context.Context, userID uint) error {
	if err := s.storageReady(); err != nil {
		return err
	}
	q, err := s.userQuota(ctx, userID)
	if err != nil {
		return err
	}
	if q.QuotaBytes > 0 && q.UsedBytes+q.ReservedBytes >= q.QuotaBytes {
		return ErrQuotaExceeded
	}
	return nil
}
