package conversation

import (
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Files is the narrow door other areas use to reach the chat-file store (the
// file_objects table and the bytes behind it). It exists so the integrator can
// adapt the store to the avatar, knowledge-base and account-deletion needs
// without any other area reading this area's tables.
type Files struct{ s *service }

// StoredFile is an opened file owned by a user.
type StoredFile struct {
	ObjectID  uint
	FileID    string
	Name      string
	MimeType  string
	Category  string
	Size      int64
	UpdatedAt time.Time
	Body      io.ReadCloser
}

// Open returns the active file with its bytes, or ErrFileNotFound.
func (f *Files) Open(ctx context.Context, ownerUserID uint, fileID string) (*StoredFile, error) {
	obj, data, err := f.s.openFile(ctx, ownerUserID, fileID)
	if err != nil {
		return nil, err
	}
	mime := obj.DetectedMIME
	if mime == "" {
		mime = obj.MimeType
	}
	return &StoredFile{
		ObjectID: obj.ID, FileID: obj.FileID, Name: obj.FileName, MimeType: mime, Category: obj.FileCategory,
		Size: int64(len(data)), UpdatedAt: obj.UpdatedAt, Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

// IsActiveImage reports whether the user owns an active file that is an image.
// Reading through tx lets a caller lock the row inside its own transaction.
func (f *Files) IsActiveImage(ctx context.Context, tx *gorm.DB, ownerUserID uint, fileID string) (bool, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" || len(fileID) > 64 {
		return false, nil
	}
	db := f.s.db
	if tx != nil {
		db = tx
	}
	var n int64
	err := db.WithContext(ctx).Model(&FileObject{}).
		Where("file_id = ? AND user_id = ? AND status = ? AND file_category = ?", fileID, ownerUserID, fileStatusActive, "image").
		Count(&n).Error
	return n > 0, err
}

// ObjectID resolves a public file id to the internal row id (0 when unknown).
func (f *Files) ObjectID(ctx context.Context, ownerUserID uint, fileID string) (uint, error) {
	obj, err := f.s.getFile(ctx, ownerUserID, fileID)
	if err != nil {
		if err == ErrFileNotFound {
			return 0, nil
		}
		return 0, err
	}
	return obj.ID, nil
}

// Upload stores a file for a user and returns its public id.
func (f *Files) Upload(ctx context.Context, ownerUserID uint, name, mimeType, purpose string, body io.Reader) (string, error) {
	res, err := f.s.uploadFile(ctx, ownerUserID, name, mimeType, purpose, body)
	if err != nil {
		return "", err
	}
	return res.File.FileID, nil
}

// DeleteUnreferenced removes the file when no conversation attachment and no
// other area uses it. It reports whether the file was removed.
func (f *Files) DeleteUnreferenced(ctx context.Context, ownerUserID uint, fileID string) (bool, error) {
	obj, err := f.s.getFile(ctx, ownerUserID, fileID)
	if err != nil {
		if err == ErrFileNotFound {
			return false, nil
		}
		return false, err
	}
	var refs int64
	if err := f.s.db.WithContext(ctx).Table("chat_attachments AS a").
		Joins("JOIN chat_conversations c ON c.id = a.conversation_id AND c.deleted_at IS NULL").
		Where("a.file_id = ? AND a.user_id = ? AND a.status <> ? AND a.deleted_at IS NULL", obj.FileID, ownerUserID, fileStatusDeleted).
		Count(&refs).Error; err != nil {
		return false, err
	}
	if refs > 0 {
		return false, nil
	}
	inUse, err := f.s.ports.Capabilities.FileInUse(ctx, ownerUserID, obj.FileID)
	if err != nil || inUse {
		return false, err
	}
	if _, err := f.s.removeFile(ctx, obj); err != nil {
		return false, err
	}
	return true, nil
}

// SubmitEmbeddings queues files for retrieval indexing and reports which were
// accepted and which skipped (with the reason).
func (f *Files) SubmitEmbeddings(ctx context.Context, ownerUserID uint, fileIDs []string) (submitted []string, skipped map[string]string, err error) {
	res, err := f.s.submitEmbeddings(ctx, ownerUserID, fileIDs)
	if err != nil {
		return nil, nil, err
	}
	skipped = make(map[string]string, len(res.Skipped))
	for _, s := range res.Skipped {
		skipped[s.FileID] = s.Reason
	}
	return res.SubmittedFileIDs, skipped, nil
}

// CanVectorize applies the area's indexing rules to a file's facts. With an
// index attached the facts are judged by it; the arguments describe a file
// whose text is (not) extracted.
func (f *Files) CanVectorize(ctx context.Context, extractReady, ragOptOut bool) (bool, string) {
	rs := f.s.settings(ctx)
	status := ""
	if extractReady {
		status = "ready"
	}
	return f.s.vectorization(ctx, rs, FileObject{RAGOptOut: ragOptOut, ExtractStatus: status})
}

// FileFacts loads the stored facts of an active file owned by the user; ok is
// false when there is no such file.
func (f *Files) FileFacts(ctx context.Context, ownerUserID uint, fileID string) (FileFacts, bool, error) {
	obj, err := f.s.getFile(ctx, ownerUserID, fileID)
	if err != nil {
		if err == ErrFileNotFound || err == ErrFileInvalidID {
			return FileFacts{}, false, nil
		}
		return FileFacts{}, false, err
	}
	return factsOf(*obj), true, nil
}

// RemoveStored deletes stored bytes by the storage paths a file row carried
// and reports how many deletions were attempted and which paths failed.
func (f *Files) RemoveStored(paths []string) (total int, failed []string) {
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		total++
		if err := f.s.deleteBytes(context.Background(), path.Clean(p)); err != nil {
			failed = append(failed, p)
		}
	}
	return total, failed
}

func removeIfExists(abs string) error {
	err := os.Remove(abs)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SetInlineFileProcessing makes uploads finish their text processing before
// the upload request returns. It exists for tests of other areas that attach a
// file right after uploading it.
func SetInlineFileProcessing(on bool) { inlineProcessing.Store(on) }
