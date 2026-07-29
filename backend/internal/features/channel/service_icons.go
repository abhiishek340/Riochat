package channel

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const (
	maxIconBytes     = 1 << 20
	maxIconBodyBytes = maxIconBytes + 256<<10
	maxIconDimension = 2048
	iconLease        = 24 * time.Hour
	cleanupBatch     = 128
)

var errStorageMissing = errors.New("channel: object storage is not configured")

// IconMaxBodyBytes is the request body cap of an icon upload.
const IconMaxBodyBytes = maxIconBodyBytes

type iconInfo struct {
	contentType string
	ext         string
	width       int
	height      int
}

// webpDimensions reads the canvas size of a WebP file from its container. No
// WebP pixel codec is linked in, so the container is validated structurally
// instead: the RIFF size must match the file, every chunk must fit and the
// chunk layout must be one a real encoder produces (a bitstream chunk, or an
// extended header followed by bitstream or animation frame chunks).
func webpDimensions(b []byte) (w, h int, ok bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	end := int(binary.LittleEndian.Uint32(b[4:8])) + 8
	if end > len(b) || end < 20 || end%2 != 0 {
		return 0, 0, false
	}
	b = b[:end]
	type chunk struct {
		tag     string
		payload []byte
	}
	var chunks []chunk
	for off := 12; off < len(b); {
		if off+8 > len(b) {
			return 0, 0, false
		}
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		if size < 0 || off+8+size > len(b) {
			return 0, 0, false
		}
		chunks = append(chunks, chunk{string(b[off : off+4]), b[off+8 : off+8+size]})
		off += 8 + size + size%2
	}
	if len(chunks) == 0 {
		return 0, 0, false
	}
	dims := func(c chunk) (int, int, bool) {
		p := c.payload
		switch c.tag {
		case "VP8 ":
			// Frame tag (3 bytes, key frame bit clear), start code, 14-bit sizes.
			if len(p) < 10 || p[0]&1 != 0 || p[3] != 0x9d || p[4] != 0x01 || p[5] != 0x2a {
				return 0, 0, false
			}
			return int(binary.LittleEndian.Uint16(p[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(p[8:10]) & 0x3fff), true
		case "VP8L":
			if len(p) < 5 || p[0] != 0x2f {
				return 0, 0, false
			}
			v := binary.LittleEndian.Uint32(p[1:5])
			return int(v&0x3fff) + 1, int((v>>14)&0x3fff) + 1, true
		}
		return 0, 0, false
	}
	first := chunks[0]
	switch first.tag {
	case "VP8 ", "VP8L":
		if len(chunks) != 1 {
			return 0, 0, false
		}
		return dims2(dims(first))
	case "VP8X":
		if len(first.payload) != 10 {
			return 0, 0, false
		}
		p := first.payload
		w = 1 + (int(p[4]) | int(p[5])<<8 | int(p[6])<<16)
		h = 1 + (int(p[7]) | int(p[8])<<8 | int(p[9])<<16)
		hasImage := false
		for _, c := range chunks[1:] {
			switch c.tag {
			case "VP8 ", "VP8L":
				cw, ch, good := dims(c)
				if !good || (p[0]&0x02 == 0 && (cw != w || ch != h)) {
					return 0, 0, false
				}
				hasImage = true
			case "ANMF":
				if len(c.payload) < 16 {
					return 0, 0, false
				}
				hasImage = true
			}
		}
		if !hasImage {
			return 0, 0, false
		}
		return w, h, true
	}
	return 0, 0, false
}

func dims2(w, h int, ok bool) (int, int, bool) { return w, h, ok }

// inspectIcon validates an uploaded file: PNG, JPEG or WebP, each side 1..2048,
// and (for PNG/JPEG) a full decode agreeing with the header.
func inspectIcon(data []byte) (iconInfo, error) {
	if len(data) == 0 {
		return iconInfo{}, ErrIconFileInvalid
	}
	if len(data) > maxIconBytes {
		return iconInfo{}, ErrIconFileTooLarge
	}
	var info iconInfo
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		w, h, ok := webpDimensions(data)
		if !ok {
			return iconInfo{}, ErrIconFileInvalid
		}
		info = iconInfo{"image/webp", ".webp", w, h}
	} else {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return iconInfo{}, ErrIconFileInvalid
		}
		switch format {
		case "png":
			info = iconInfo{"image/png", ".png", cfg.Width, cfg.Height}
		case "jpeg":
			info = iconInfo{"image/jpeg", ".jpg", cfg.Width, cfg.Height}
		default:
			return iconInfo{}, ErrIconFileInvalid
		}
		if info.width < 1 || info.height < 1 || info.width > maxIconDimension || info.height > maxIconDimension {
			return iconInfo{}, ErrIconFileInvalid
		}
		img, fmtName, err := image.Decode(bytes.NewReader(data))
		if err != nil || fmtName != format || img.Bounds().Dx() != cfg.Width || img.Bounds().Dy() != cfg.Height {
			return iconInfo{}, ErrIconFileInvalid
		}
	}
	if info.width < 1 || info.height < 1 || info.width > maxIconDimension || info.height > maxIconDimension {
		return iconInfo{}, ErrIconFileInvalid
	}
	return info, nil
}

func iconStoragePath(sha, ext string) string { return "model-icons/" + sha[:2] + "/" + sha + ext }

func newIconPublicID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ico_" + hex.EncodeToString(b[:]), nil
}

func iconView(a *IconAsset) IconAssetView {
	return IconAssetView{Ref: assetPrefix + a.PublicID, PublicID: a.PublicID, ContentType: a.ContentType,
		SizeBytes: a.SizeBytes, Width: a.Width, Height: a.Height, CreatedAt: a.CreatedAt}
}

// UploadIcon validates and stores an icon. Identical content is deduplicated.
func (s *Service) UploadIcon(ctx context.Context, userID uint, data []byte) (*IconUploadResult, error) {
	info, err := inspectIcon(data)
	if err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, errStorageMissing
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	path := iconStoragePath(sha, info.ext)
	now := s.now()

	var asset IconAsset
	reused := false
	err = s.tx(ctx, func(tx *gorm.DB) error {
		err := tx.Where("sha256 = ?", sha).First(&asset).Error
		switch {
		case err == nil:
			if asset.DeletingAt != nil {
				return ErrIconAssetUnavail
			}
			reused = true
			asset.LeaseExpiresAt = now.Add(iconLease)
			asset.UnreferencedAt = ptr(now)
			asset.DeleteRequestedAt = nil
			return tx.Save(&asset).Error
		case !isNotFound(err):
			return err
		}
		id, err := newIconPublicID()
		if err != nil {
			return err
		}
		asset = IconAsset{PublicID: id, SHA256: sha, StoragePath: path, ContentType: info.contentType,
			SizeBytes: int64(len(data)), Width: info.width, Height: info.height, CreatedByUserID: userID,
			LeaseExpiresAt: now.Add(iconLease), UnreferencedAt: ptr(now)}
		return tx.Create(&asset).Error
	})
	if err != nil && database.IsDuplicate(err) {
		// Someone stored the same content concurrently; use their row.
		if lerr := s.db.WithContext(ctx).Where("sha256 = ?", sha).First(&asset).Error; lerr != nil {
			return nil, lerr
		}
		reused, err = true, nil
	}
	if err != nil {
		return nil, err
	}
	// Store the object, keeping an identical existing one.
	if existing, gerr := s.store.Get(ctx, asset.StoragePath); gerr != nil || !bytes.Equal(existing, data) {
		if perr := s.store.Put(ctx, asset.StoragePath, asset.ContentType, data); perr != nil {
			return nil, perr
		}
	}
	if asset.ReadyAt == nil {
		ready := s.now()
		if err := s.db.WithContext(ctx).Model(&IconAsset{}).Where("id = ?", asset.ID).Update("ready_at", ready).Error; err != nil {
			return nil, err
		}
	}
	return &IconUploadResult{Ref: assetPrefix + asset.PublicID, PublicID: asset.PublicID, ContentType: asset.ContentType,
		SizeBytes: asset.SizeBytes, Width: asset.Width, Height: asset.Height, Reused: reused}, nil
}

// ListIcons lists the ready, non-removed library icons.
func (s *Service) ListIcons(ctx context.Context, page, size int) ([]IconAssetView, int64, error) {
	q := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&IconAsset{}).
			Where("ready_at IS NOT NULL AND delete_requested_at IS NULL AND deleting_at IS NULL")
	}
	var total int64
	if err := q().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []IconAsset
	if err := q().Order("created_at DESC, id DESC").Offset(pageOffset(page, size)).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]IconAssetView, 0, len(rows))
	for i := range rows {
		out = append(out, iconView(&rows[i]))
	}
	return out, total, nil
}

// IconReferences are the places an icon is used.
type IconReferences struct {
	ReferenceCount   int64 `json:"referenceCount"`
	Models           int64 `json:"models"`
	Vendors          int64 `json:"vendors"`
	DisplayGroups    int64 `json:"displayGroups"`
	ConversationRuns int64 `json:"conversationRuns"`
}

// externalIconReferences counts the references other areas hold. It reads
// through their own connection, so it must run outside any transaction here
// (SQLite has a single connection and would wait on itself).
func (s *Service) externalIconReferences(ctx context.Context, ref string) (int64, error) {
	if s.opts.IconUsage == nil {
		return 0, nil
	}
	return s.opts.IconUsage.CountIconReferences(ctx, ref)
}

// iconReferences counts the references to ref. external is the result of
// externalIconReferences, fetched by the caller before opening a transaction.
func (s *Service) iconReferences(ctx context.Context, db *gorm.DB, ref string, external int64) (IconReferences, error) {
	var r IconReferences
	count := func(model any, dst *int64) error {
		return db.WithContext(ctx).Model(model).Where("icon = ?", ref).Count(dst).Error
	}
	if err := count(&PlatformModel{}, &r.Models); err != nil {
		return r, err
	}
	if err := count(&ModelVendor{}, &r.Vendors); err != nil {
		return r, err
	}
	if err := count(&ModelDisplayGroup{}, &r.DisplayGroups); err != nil {
		return r, err
	}
	r.ConversationRuns = external
	r.ReferenceCount = r.Models + r.Vendors + r.DisplayGroups + r.ConversationRuns
	return r, nil
}

// RemoveIcon removes an unreferenced icon from the library; the object is
// deleted after a 24 hour grace period.
func (s *Service) RemoveIcon(ctx context.Context, publicID string) error {
	id, ok := parseAssetID(publicID)
	if !ok {
		return ErrIconAssetNotFound
	}
	external, err := s.externalIconReferences(ctx, assetPrefix+id)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx *gorm.DB) error {
		var a IconAsset
		if err := tx.Where("public_id = ?", id).First(&a).Error; err != nil {
			if isNotFound(err) {
				return ErrIconAssetNotFound
			}
			return err
		}
		if a.ReadyAt == nil || a.DeletingAt != nil || a.DeleteRequestedAt != nil {
			return ErrIconAssetNotFound
		}
		refs, err := s.iconReferences(ctx, tx, assetPrefix+id, external)
		if err != nil {
			return err
		}
		if refs.ReferenceCount > 0 {
			return ErrIconAssetInUse.WithDetails(refs)
		}
		now := s.now()
		return tx.Model(&IconAsset{}).Where("id = ?", a.ID).Updates(map[string]any{
			"delete_requested_at": now, "unreferenced_at": now, "lease_expires_at": now.Add(iconLease),
		}).Error
	})
}

// reserveIcon checks that a managed asset may be referenced and renews its
// lease so the cleanup cannot remove it before the reference is saved.
func (s *Service) reserveIcon(ctx context.Context, tx *gorm.DB, assetID string) error {
	var a IconAsset
	if err := tx.WithContext(ctx).Where("public_id = ?", assetID).First(&a).Error; err != nil {
		if isNotFound(err) {
			return ErrIconAssetNotFound
		}
		return err
	}
	if a.ReadyAt == nil || a.DeletingAt != nil {
		return ErrIconAssetNotFound
	}
	return tx.WithContext(ctx).Model(&IconAsset{}).Where("id = ?", a.ID).Updates(map[string]any{
		"lease_expires_at": s.now().Add(iconLease), "unreferenced_at": nil, "delete_requested_at": nil,
	}).Error
}

// IconObject is a stored icon ready to be served.
type IconObject struct {
	ContentType string
	SHA256      string
	Data        []byte
}

// OpenIcon opens an icon for the public reader. The object is opened and
// verified before the caller may answer 304.
func (s *Service) OpenIcon(ctx context.Context, publicID string) (*IconObject, error) {
	id, ok := parseAssetID(publicID)
	if !ok {
		return nil, ErrIconAssetNotFound
	}
	var a IconAsset
	if err := s.db.WithContext(ctx).Where("public_id = ?", id).First(&a).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrIconAssetNotFound
		}
		return nil, err
	}
	if a.ReadyAt == nil || a.DeletingAt != nil {
		return nil, ErrIconAssetNotFound
	}
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}[a.ContentType]
	if ext == "" || len(a.SHA256) != 64 || a.StoragePath != iconStoragePath(a.SHA256, ext) || a.SizeBytes < 1 || a.SizeBytes > maxIconBytes {
		return nil, ErrIconAssetNotFound
	}
	if s.store == nil {
		return nil, errStorageMissing
	}
	data, err := s.store.Get(ctx, a.StoragePath)
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			return nil, ErrIconAssetNotFound
		}
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != a.SizeBytes || hex.EncodeToString(sum[:]) != a.SHA256 {
		return nil, ErrIconAssetNotFound
	}
	return &IconObject{ContentType: a.ContentType, SHA256: a.SHA256, Data: data}, nil
}

// RunIconCleanup is one pass of the periodic icon cleanup.
func (s *Service) RunIconCleanup(ctx context.Context) {
	if s.store == nil {
		return
	}
	now := s.now()
	var batch []IconAsset
	err := s.db.WithContext(ctx).
		Where("lease_expires_at <= ? OR deleting_at IS NOT NULL", now).
		Order("CASE WHEN deleting_at IS NULL THEN 1 ELSE 0 END, lease_expires_at ASC, id ASC").
		Limit(cleanupBatch).Find(&batch).Error
	if err != nil {
		s.log.Warn("llm_icon_cleanup_failed", logx.Err(err))
		return
	}
	for i := range batch {
		if ctx.Err() != nil {
			return
		}
		if err := s.cleanupIcon(ctx, &batch[i], now); err != nil {
			s.log.Warn("llm_icon_cleanup_item_failed", logx.F("asset", batch[i].PublicID), logx.Err(err))
		}
	}
}

func (s *Service) cleanupIcon(ctx context.Context, a *IconAsset, now time.Time) error {
	db := s.db.WithContext(ctx)
	if a.DeletingAt == nil {
		external, err := s.externalIconReferences(ctx, assetPrefix+a.PublicID)
		if err != nil {
			return err
		}
		refs, err := s.iconReferences(ctx, db, assetPrefix+a.PublicID, external)
		if err != nil {
			return err
		}
		switch {
		case refs.ReferenceCount > 0:
			return db.Model(&IconAsset{}).Where("id = ?", a.ID).Updates(map[string]any{
				"lease_expires_at": now.Add(iconLease), "unreferenced_at": nil, "delete_requested_at": nil}).Error
		case a.UnreferencedAt == nil:
			return db.Model(&IconAsset{}).Where("id = ?", a.ID).Updates(map[string]any{
				"unreferenced_at": now, "lease_expires_at": now.Add(iconLease)}).Error
		}
		res := db.Model(&IconAsset{}).Where("id = ? AND deleting_at IS NULL AND lease_expires_at <= ?", a.ID, now).
			Update("deleting_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // someone else claimed or renewed it
		}
	}
	if err := s.store.Delete(ctx, a.StoragePath); err != nil && !errors.Is(err, ErrObjectNotFound) {
		return err
	}
	return db.Delete(&IconAsset{}, a.ID).Error
}

// startIconCleanup runs the cleanup immediately and then hourly.
func (s *Service) startIconCleanup() {
	if s.store == nil {
		return
	}
	s.life.Go("llm-icon-cleanup", func(ctx context.Context) {
		s.RunIconCleanup(ctx)
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.RunIconCleanup(ctx)
			}
		}
	})
}

var _ = strings.TrimSpace
