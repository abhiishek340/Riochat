package integration

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/knowledgebase"
	"github.com/riochat/riochat/backend/internal/features/user"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// The chat-file store belongs to the conversation area. These adapters give
// the user, auth, knowledge-base and conversation areas the file operations
// they declared, without any of them reading the file tables.

var errFilesUnavailable = errors.New("integration: file store is not mounted")

// ---- user area ----

type userFiles struct{ h *Hub }

func (u userFiles) OpenUpload(ctx context.Context, ownerUserID uint, uploadID string) (*user.Upload, error) {
	if u.h.files == nil {
		return nil, user.ErrUploadNotFound
	}
	f, err := u.h.files.Open(ctx, ownerUserID, uploadID)
	if err != nil {
		if errors.Is(err, conversation.ErrFileNotFound) || errors.Is(err, conversation.ErrFileInvalidID) {
			return nil, user.ErrUploadNotFound
		}
		return nil, err
	}
	return &user.Upload{Body: f.Body, ContentType: f.MimeType, Size: f.Size, ModTime: f.UpdatedAt, FileName: f.Name}, nil
}

type userImages struct{ h *Hub }

func (u userImages) IsActiveImage(ctx context.Context, tx *gorm.DB, ownerUserID uint, uploadID string) (bool, error) {
	if u.h.files == nil {
		return false, nil
	}
	return u.h.files.IsActiveImage(ctx, tx, ownerUserID, uploadID)
}

// ---- auth area ----

type avatarChecker struct{ h *Hub }

func (a avatarChecker) IsOwnedImage(ctx context.Context, userID uint, fileID string) bool {
	if a.h.files == nil {
		return false
	}
	ok, err := a.h.files.IsActiveImage(ctx, nil, userID, fileID)
	return err == nil && ok
}

// ---- knowledge-base area ----

type kbReader struct{ h *Hub }

func (k kbReader) OpenFile(ctx context.Context, ownerUserID uint, fileID string) (*knowledgebase.FileContent, error) {
	if k.h.files == nil {
		return nil, errFilesUnavailable
	}
	f, err := k.h.files.Open(ctx, ownerUserID, fileID)
	if err != nil {
		return nil, err
	}
	return &knowledgebase.FileContent{FileName: f.Name, MimeType: f.MimeType, Size: f.Size, Body: f.Body}, nil
}

type kbCleaner struct{ h *Hub }

func (k kbCleaner) DeleteUnreferenced(ctx context.Context, ownerUserID uint, fileID string) (bool, error) {
	if k.h.files == nil {
		return false, nil
	}
	return k.h.files.DeleteUnreferenced(ctx, ownerUserID, fileID)
}

type kbUploader struct{ h *Hub }

func (k kbUploader) Upload(ctx context.Context, req knowledgebase.UploadRequest) (string, error) {
	if k.h.files == nil {
		return "", errFilesUnavailable
	}
	return k.h.files.Upload(ctx, req.OwnerUserID, req.FileName, req.ContentType, req.Purpose, req.Body)
}

type kbEmbedder struct{ h *Hub }

func (k kbEmbedder) SubmitFiles(ctx context.Context, ownerUserID uint, fileIDs []string) (knowledgebase.EmbedResult, error) {
	if k.h.files == nil {
		return knowledgebase.EmbedResult{}, errFilesUnavailable
	}
	submitted, skipped, err := k.h.files.SubmitEmbeddings(ctx, ownerUserID, fileIDs)
	if err != nil {
		return knowledgebase.EmbedResult{}, err
	}
	out := knowledgebase.EmbedResult{Submitted: submitted}
	for id, reason := range skipped {
		out.Skipped = append(out.Skipped, knowledgebase.SkippedFile{FileID: id, Reason: reason})
	}
	return out, nil
}

type kbVectorizer struct{ h *Hub }

func (k kbVectorizer) CanVectorize(ctx context.Context, f knowledgebase.FileFacts) (bool, string) {
	if k.h.rag != nil {
		can, reason, ok, err := k.h.rag.VectorizationOf(ctx, f.OwnerUserID, f.FileID)
		if err == nil && ok {
			return can, reason
		}
		if err == nil {
			return false, reason
		}
	}
	if k.h.files == nil {
		return false, "embedding_not_configured"
	}
	return k.h.files.CanVectorize(ctx, true, f.RAGOptOut)
}

// ---- conversation area: references into other areas ----

type convCapabilities struct {
	h  *Hub
	db *gorm.DB
}

func (c convCapabilities) ValidateMCPTools(ctx context.Context, userID uint, toolIDs []uint) error {
	if c.h.mcp == nil || len(toolIDs) == 0 {
		return nil
	}
	return c.h.mcp.ValidateMCPTools(ctx, userID, toolIDs)
}

func (c convCapabilities) ValidateSkills(ctx context.Context, userID uint, ids []uint) error {
	if c.h.skills == nil || len(ids) == 0 {
		return nil
	}
	return c.h.skills.ValidateUsable(ctx, userID, ids)
}

func (c convCapabilities) ValidateKnowledgeBases(ctx context.Context, userID uint, ids []string) error {
	if c.h.kb == nil || len(ids) == 0 {
		return nil
	}
	return mapKBReference(c.h.kb.ValidateKnowledgeBases(ctx, userID, ids))
}

// mapKBReference gives the knowledge-base area's rejections the status the
// conversation contract names: a reference problem is 400, a base without
// ready files 409 and an unavailable retrieval capability 503.
func mapKBReference(err error) error {
	if err == nil {
		return nil
	}
	te, ok := apperr.Find(err)
	if !ok {
		return err
	}
	switch te.Code {
	case conversation.ErrKBNotReady.Code:
		return conversation.ErrKBNotReady.WithCause(err)
	case conversation.ErrInvalidKBReference.Code:
		return conversation.ErrInvalidKBReference.WithCause(err)
	case conversation.ErrKBUnavailable.Code:
		return conversation.ErrKBUnavailable.WithCause(err)
	}
	return err
}

func (c convCapabilities) FileInUse(ctx context.Context, userID uint, fileID string) (bool, error) {
	var avatar int64
	if err := c.db.WithContext(ctx).Model(&user.Account{}).
		Where("id = ? AND avatar_url = ?", userID, "file:"+fileID).Count(&avatar).Error; err != nil {
		return false, err
	}
	if avatar > 0 {
		return true, nil
	}
	if c.h.kb != nil && c.h.files != nil {
		objID, err := c.h.files.ObjectID(ctx, userID, fileID)
		if err != nil {
			return false, err
		}
		if objID != 0 {
			return c.h.kb.IsFileLinked(ctx, objID)
		}
	}
	return false, nil
}

// ---- account deletion ----

// accountPurger removes what the other areas keep for a deleted user. The row
// purge itself lives in the user area; the knowledge-base area vetoes the
// deletion while a built-in base still points at the user's files.
type accountPurger struct {
	h *Hub

	mu    sync.Mutex
	paths map[uint][]string
}

func newAccountPurger(h *Hub) *accountPurger { return &accountPurger{h: h, paths: map[uint][]string{}} }

func (a *accountPurger) BeforeDelete(ctx context.Context, userID uint) error {
	if a.h.kb != nil {
		if err := a.h.kb.CheckAccountDeletion(ctx, userID); err != nil {
			return err
		}
	}
	if a.h.accounts != nil {
		paths, err := a.h.accounts.FileStoragePaths(ctx, userID)
		if err != nil {
			return err
		}
		a.mu.Lock()
		a.paths[userID] = paths
		a.mu.Unlock()
	}
	return nil
}

func (a *accountPurger) Purge(_ context.Context, tx *gorm.DB, userID uint) error {
	return user.PurgeRelated(tx, userID)
}

func (a *accountPurger) AfterCommit(_ context.Context, userID uint) (int, []string) {
	a.mu.Lock()
	paths := a.paths[userID]
	delete(a.paths, userID)
	a.mu.Unlock()
	if a.h.files == nil || len(paths) == 0 {
		return len(paths), nil
	}
	total, failed := a.h.files.RemoveStored(paths)
	return total, failed
}
