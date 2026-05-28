package ragindex

import (
	"path/filepath"
	"strings"
)

// File kinds recognised by the indexing rules.
const (
	kindText    = "text"
	kindPDF     = "pdf"
	kindWord    = "word"
	kindPresent = "presentation"
	kindSheet   = "excel"
	kindImage   = "image"
	kindVideo   = "video"
	kindAudio   = "audio"
	kindUnknown = "unknown"
)

var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".tsv": true, ".json": true, ".jsonl": true,
	".xml": true, ".html": true, ".htm": true, ".css": true, ".scss": true, ".less": true, ".js": true,
	".jsx": true, ".mjs": true, ".ts": true, ".tsx": true, ".vue": true, ".svelte": true, ".go": true,
	".py": true, ".java": true, ".kt": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".cs": true, ".rs": true, ".rb": true, ".php": true, ".swift": true, ".scala": true, ".lua": true,
	".pl": true, ".r": true, ".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".bat": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".sql": true,
	".log": true, ".rst": true, ".tex": true, ".proto": true, ".gradle": true, ".dart": true,
}

// pixelMIMEs are the picture types the pixel path can send to a provider.
var pixelMIMEs = map[string]bool{
	"image/jpeg": true, "image/jpg": true, "image/png": true, "image/gif": true, "image/webp": true,
}

func baseMIME(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	return m
}

// kindOf classifies a file by its MIME type and name.
func kindOf(mime, name string) string {
	m := baseMIME(mime)
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case strings.HasPrefix(m, "video/"):
		return kindVideo
	case strings.HasPrefix(m, "audio/"):
		return kindAudio
	case strings.HasPrefix(m, "image/"):
		return kindImage
	case m == "application/pdf" || ext == ".pdf":
		return kindPDF
	case m == "application/msword" || strings.Contains(m, "wordprocessingml") || ext == ".doc" || ext == ".docx":
		return kindWord
	case strings.Contains(m, "presentation") || strings.Contains(m, "ms-powerpoint") || ext == ".ppt" || ext == ".pptx":
		return kindPresent
	case strings.Contains(m, "spreadsheet") || strings.Contains(m, "ms-excel") || ext == ".xls" || ext == ".xlsx":
		return kindSheet
	case strings.HasPrefix(m, "text/"), m == "application/json", m == "application/xml", m == "application/x-yaml",
		m == "application/yaml", m == "application/toml", m == "application/javascript", m == "application/typescript",
		m == "application/x-sh", strings.HasSuffix(m, "+json"), strings.HasSuffix(m, "+xml"):
		return kindText
	case textExtensions[ext]:
		return kindText
	}
	return kindUnknown
}

// imageIndexable applies the image rule: OCR text path, or a provider that can
// take the picture itself in a supported format.
func imageIndexable(mime string, ocr, imageProtocol bool) bool {
	return ocr || (imageProtocol && pixelMIMEs[baseMIME(mime)])
}

// indexable reports whether the file can be indexed now: a storage path, an
// active status and a content type the pipeline understands.
func (s *Service) indexable(f File, cfg Config) bool {
	if strings.TrimSpace(f.StoragePath) == "" || f.Status != fileActive {
		return false
	}
	switch kindOf(f.MIME(), f.FileName) {
	case kindText, kindPDF, kindWord, kindPresent, kindSheet:
		return true
	case kindImage:
		return imageIndexable(f.MIME(), cfg.ImageOCREnabled, s.imageProtocol(cfg))
	}
	return false
}

func (s *Service) imageProtocol(cfg Config) bool {
	return s.emb != nil && s.emb.SupportsImages(cfg.Protocol)
}

// embeddableCategories are the file categories listed as embeddable (the
// chat-file store also reports spreadsheet, document and code).
var embeddableCategories = []string{"pdf", "word", "presentation", "excel", "spreadsheet", "text", "document", "code"}
