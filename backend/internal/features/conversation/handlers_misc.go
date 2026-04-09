package conversation

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// ----- sharing -----

type shareCreateReq struct {
	DefaultMessagePublicIDs []string `json:"defaultMessagePublicIDs" binding:"max=1000"`
}

func (a *api) getShare(c *gin.Context) {
	d, err := a.s.getShare(c.Request.Context(), uid(c), c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, d)
}

func (a *api) createShare(regenerate bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req shareCreateReq
		if !bindOptional(c, &req) {
			return
		}
		d, err := a.s.createShare(c.Request.Context(), uid(c), c.Param("id"), req.DefaultMessagePublicIDs, regenerate)
		if err != nil {
			a.fail(c, err)
			return
		}
		action := "create_conversation_share"
		if regenerate {
			action = "regenerate_conversation_share"
		}
		a.s.auditEntry(c, uid(c), action, "conversation", strings.TrimSpace(c.Param("id")), map[string]any{"share_id": d.ShareID, "message_count": d.MessageCount})
		httpx.OK(c, d)
	}
}

func (a *api) revokeShare(c *gin.Context) {
	d, err := a.s.revokeShare(c.Request.Context(), uid(c), c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "revoke_conversation_share", "conversation", strings.TrimSpace(c.Param("id")), nil)
	httpx.OK(c, d)
}

type revokeSharesReq struct {
	ConversationPublicIDs []string `json:"conversationPublicIDs" binding:"max=1000"`
}

func (a *api) revokeShares(c *gin.Context) {
	var req revokeSharesReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if err := a.s.revokeShares(c.Request.Context(), uid(c), req.ConversationPublicIDs); err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "revoke_conversation_shares", "conversation", "", map[string]any{"count": len(req.ConversationPublicIDs)})
	httpx.OK(c, gin.H{"revoked": true})
}

func (a *api) readShared(c *gin.Context) {
	d, err := a.s.readSharedConversation(c.Request.Context(), c.Param("share_id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, d)
}

var activeContentTypes = map[string]bool{
	"text/html": true, "image/svg+xml": true, "application/xhtml+xml": true, "text/xml": true, "application/xml": true,
}

// ownerCSP is the policy sent with a file the owner downloads: nothing in the
// file may run, load or frame anything.
const ownerCSP = "default-src 'none'; base-uri 'none'; form-action 'none'; script-src 'none'; object-src 'none'; " +
	"frame-ancestors 'none'; img-src 'self' data: blob:; media-src 'self' data: blob:"

func serveBytes(c *gin.Context, f *FileObject, data []byte, forceInline bool) {
	ctype := f.DetectedMIME
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h := c.Writer.Header()
	if !forceInline {
		// Owner download: only pictures, PDFs, audio and video open in the
		// page; every text format (SVG markup included) downloads.
		disposition := "attachment"
		switch {
		case strings.HasPrefix(ctype, "image/") && ctype != "image/svg+xml", ctype == "application/pdf",
			strings.HasPrefix(ctype, "audio/"), strings.HasPrefix(ctype, "video/"):
			disposition = "inline"
		}
		if strings.EqualFold(filepath.Ext(f.FileName), ".svg") || ctype == "image/svg+xml" {
			ctype = "text/plain; charset=utf-8"
		}
		h.Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, asciiName(f.FileName), url.PathEscape(f.FileName)))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", ownerCSP)
		h.Set("Cache-Control", "private, max-age=60")
		c.Data(http.StatusOK, ctype, data)
		return
	}
	disposition := "inline"
	if activeContentTypes[ctype] {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": asciiName(f.FileName)})+
		fmt.Sprintf("; filename*=UTF-8''%s", url.PathEscape(f.FileName)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, ctype, data)
}

func asciiName(n string) string {
	var b strings.Builder
	for _, r := range n {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}

func (a *api) sharedFile(c *gin.Context) {
	f, data, err := a.s.sharedFile(c.Request.Context(), c.Param("share_id"), c.Param("file_id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	serveBytes(c, f, data, true)
}
