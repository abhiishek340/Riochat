package user

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// avatarHandler serves GET /users/:public_id/avatar. Only uploaded images are
// served; external URLs, paths and generated markers are not.
func avatarHandler(accounts *Accounts, files FileReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		publicID := strings.TrimSpace(c.Param("public_id"))
		if publicID == "" {
			httpx.Fail(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		ctx := c.Request.Context()
		acct, err := accounts.GetByPublicID(ctx, publicID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				httpx.Fail(c, http.StatusNotFound, ErrNotFound)
				return
			}
			httpx.Fail(c, http.StatusInternalServerError, err)
			return
		}
		uploadID, ok := UploadIDFromAvatarRef(acct.AvatarURL)
		if !ok || files == nil {
			httpx.Fail(c, http.StatusNotFound, ErrAvatarNotFound)
			return
		}
		up, err := files.OpenUpload(ctx, acct.ID, uploadID)
		if err != nil {
			if errors.Is(err, ErrUploadNotFound) {
				httpx.Fail(c, http.StatusNotFound, ErrAvatarNotFound)
				return
			}
			httpx.Fail(c, http.StatusInternalServerError, err)
			return
		}
		if up == nil || up.Body == nil {
			httpx.Fail(c, http.StatusNotFound, ErrAvatarNotFound)
			return
		}
		defer up.Body.Close()

		ctype := strings.TrimSpace(up.ContentType)
		if ctype == "" {
			ctype = mime.TypeByExtension(strings.ToLower(path.Ext(up.FileName)))
		}
		if !strings.HasPrefix(strings.ToLower(ctype), "image/") {
			httpx.Fail(c, http.StatusNotFound, ErrAvatarNotFound)
			return
		}

		h := c.Writer.Header()
		h.Set("Content-Type", ctype)
		h.Set("Content-Disposition", "inline")
		h.Set("Cache-Control", "public, max-age=300")
		h.Set("X-Content-Type-Options", "nosniff")
		if up.Size > 0 {
			h.Set("Content-Length", strconv.FormatInt(up.Size, 10))
		}
		if !up.ModTime.IsZero() {
			h.Set("Last-Modified", up.ModTime.UTC().Format(http.TimeFormat))
		}
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, up.Body); err != nil {
			// The header is already out; end the response without an envelope.
			reqctx.FromRequest(c.Request).RecordFailure("avatar.stream_failed", err, "avatar stream interrupted")
			c.Abort()
		}
	}
}
