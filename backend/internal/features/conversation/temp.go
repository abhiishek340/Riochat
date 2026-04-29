package conversation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

const (
	tempJSONLimit      = 8 << 20
	tempMaxAttachments = 20
	tempMaxTotalRunes  = 1_000_000
	tempAuditAction    = "temporary_chat.stream_message"
)

type tempMessage struct {
	Role    string `json:"role" binding:"required,oneof=user assistant"`
	Content string `json:"content" binding:"max=200000"`
}

type tempUIState struct {
	MessageIndex int            `json:"messageIndex" binding:"min=0,max=99"`
	BlockID      string         `json:"blockID" binding:"max=128"`
	Title        string         `json:"title" binding:"max=200"`
	State        map[string]any `json:"state"`
}

// tempRequest is the body of POST /temporary-chat/messages/stream.
type tempRequest struct {
	SessionID        string         `json:"sessionID" binding:"required,max=64"`
	ClientRunID      string         `json:"clientRunID" binding:"required,max=64"`
	Model            string         `json:"model" binding:"required,max=128"`
	Options          map[string]any `json:"options"`
	Controls         map[string]any `json:"controls"`
	SelectedToolIDs  []uint         `json:"selectedToolIDs" binding:"max=128"`
	SkillIDs         []uint         `json:"skillIDs" binding:"max=128"`
	KnowledgeBaseIDs []string       `json:"knowledgeBaseIDs" binding:"max=8,dive,max=32"`
	HTMLVisualPrompt bool           `json:"htmlVisualPrompt"`
	Messages         []tempMessage  `json:"messages" binding:"required,min=1,max=100,dive"`
	UIComponentIDs   []uint         `json:"uiComponentIDs" binding:"max=32"`
	UIBlockVersion   int            `json:"uiBlockVersion" binding:"omitempty,oneof=1 2"`
	UIStates         []tempUIState  `json:"uiStates" binding:"max=32,dive"`
}

// tempAttachment is an uploaded file kept only for the duration of a request.
type tempAttachment struct {
	name string
	mime string
	data []byte
}

// parseTemporary reads the JSON or multipart body. It writes the error response
// itself and returns false on failure.
func (s *service) parseTemporary(c *gin.Context) (*tempRequest, []tempAttachment, []int, bool) {
	rs := s.settings(c.Request.Context())
	ct := strings.ToLower(c.GetHeader("Content-Type"))
	if !strings.HasPrefix(ct, "multipart/form-data") {
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, tempJSONLimit))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrTempContextTooLarge)
			} else {
				httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.WithCause(err))
			}
			return nil, nil, nil, false
		}
		var req tempRequest
		if !httpxBindBytes(c, raw, &req) {
			return nil, nil, nil, false
		}
		return &req, nil, nil, true
	}
	limit := int64(tempJSONLimit) + rs.MaxUploadBytes*tempMaxAttachments
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	if err := c.Request.ParseMultipartForm(16 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrTempContextTooLarge)
			return nil, nil, nil, false
		}
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidFile.WithCause(err))
		return nil, nil, nil, false
	}
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	payload := c.Request.FormValue("payload")
	if strings.TrimSpace(payload) == "" {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidTempMessages)
		return nil, nil, nil, false
	}
	var req tempRequest
	if !httpxBindBytes(c, []byte(payload), &req) {
		return nil, nil, nil, false
	}
	var idx []int
	if raw := strings.TrimSpace(c.Request.FormValue("attachmentMessageIndexes")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &idx); err != nil {
			httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidRef)
			return nil, nil, nil, false
		}
	}
	var headers []*multipart.FileHeader
	if c.Request.MultipartForm != nil {
		headers = c.Request.MultipartForm.File["attachments"]
	}
	if len(headers) == 0 || len(headers) > tempMaxAttachments || len(headers) != len(idx) {
		httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidRef)
		return nil, nil, nil, false
	}
	var atts []tempAttachment
	for _, h := range headers {
		if h.Size > rs.MaxUploadBytes {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrFileTooLarge)
			return nil, nil, nil, false
		}
		f, err := h.Open()
		if err != nil {
			httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidStream.WithCause(err))
			return nil, nil, nil, false
		}
		data, err := io.ReadAll(io.LimitReader(f, rs.MaxUploadBytes+1))
		_ = f.Close()
		if err != nil {
			httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidStream.WithCause(err))
			return nil, nil, nil, false
		}
		if int64(len(data)) > rs.MaxUploadBytes {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrFileTooLarge)
			return nil, nil, nil, false
		}
		name := filepath.Base(h.Filename)
		head := data
		if len(head) > 512 {
			head = head[:512]
		}
		mt := detectMIME(name, head, h.Header.Get("Content-Type"))
		if blockedMIME[mt] || blockedExt[strings.ToLower(filepath.Ext(name))] {
			httpx.Fail(c, http.StatusBadRequest, ErrFileTypeBlocked)
			return nil, nil, nil, false
		}
		atts = append(atts, tempAttachment{name: name, mime: mt, data: data})
	}
	return &req, atts, idx, true
}

func httpxBindBytes(c *gin.Context, raw []byte, dst any) bool {
	saved := c.Request.Body
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	ok := httpx.BindJSONLimit(c, dst, tempJSONLimit)
	c.Request.Body = saved
	return ok
}

// validatedTemp is a temporary-chat request after semantic validation.
type validatedTemp struct {
	req      *tempRequest
	messages []PromptMessage
}

// validateTemporary applies the semantic rules of the temporary chat request.
func (s *service) validateTemporary(ctx context.Context, req *tempRequest, atts []tempAttachment, idx []int) (*validatedTemp, error) {
	rs := s.settings(ctx)
	msgs := make([]PromptMessage, len(req.Messages))
	prev := ""
	total := 0
	perMsg := map[int]int{}
	for _, i := range idx {
		if i < 0 || i >= len(req.Messages) || strings.ToLower(strings.TrimSpace(req.Messages[i].Role)) != roleUser {
			return nil, ErrInvalidTempMessages
		}
		perMsg[i]++
	}
	if len(atts) > tempMaxAttachments {
		return nil, ErrFileInvalidRef
	}
	for i, m := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != roleUser && role != roleAssistant {
			return nil, ErrInvalidTempMessages
		}
		if role == prev {
			return nil, ErrInvalidTempMessages
		}
		prev = role
		content := strings.TrimSpace(m.Content)
		if content == "" && !(role == roleUser && perMsg[i] > 0) {
			return nil, ErrInvalidTempMessages
		}
		total += utf8.RuneCountInString(content)
		msgs[i] = PromptMessage{Role: role, Content: content}
	}
	if prev != roleUser || total > tempMaxTotalRunes {
		return nil, ErrInvalidTempMessages
	}
	for _, n := range perMsg {
		if n > rs.MaxMessageFiles {
			return nil, ErrTooManyFiles
		}
	}
	seen := map[string]bool{}
	for _, kb := range req.KnowledgeBaseIDs {
		t := strings.TrimSpace(kb)
		if t == "" || seen[t] {
			return nil, ErrInvalidTempMessages
		}
		seen[t] = true
	}
	if len(seen) > 0 && !rs.KnowledgeBaseEnabled {
		return nil, ErrInvalidKBReference
	}
	if len(req.SelectedToolIDs) > rs.MaxSelectedTools {
		return nil, ErrTooManyTools
	}
	if len(req.SkillIDs) > rs.MaxSelectedTools {
		return nil, ErrTooManySkills
	}
	for k, i := range idx {
		a := atts[k]
		pf := PromptFile{FileName: a.name, MimeType: a.mime, Kind: fileCategory(a.mime, a.name), Size: int64(len(a.data)), Data: a.data}
		if isTextual(a.mime, a.name) && utf8.Valid(a.data) {
			pf.Text = string(a.data)
		}
		msgs[i].Files = append(msgs[i].Files, pf)
	}
	return &validatedTemp{req: req, messages: msgs}, nil
}
