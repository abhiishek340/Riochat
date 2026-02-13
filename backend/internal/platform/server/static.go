package server

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// reservedPrefixes never fall through to the front end.
var reservedPrefixes = []string{"/api", "/swagger", "/healthz", "/readyz"}

func reserved(p string) bool {
	for _, r := range reservedPrefixes {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// newFallback handles requests that match no route: front-end static hosting
// when FRONTEND_DIST_DIR points at an existing directory (read per request, so
// a configuration change applies), otherwise the 404 envelope.
func newFallback(cfg *config.Holder, log *logx.Logger) gin.HandlerFunc {
	logged := ""
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		dir := cfg.Load().Server.FrontendDistDir
		if (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) || reserved(p) || dir == "" {
			httpx.NotFound(c)
			return
		}
		root, err := filepath.EvalSymlinks(dir)
		if err != nil || !isDir(root) {
			if logged != dir {
				logged = dir
				log.Warn("frontend_static_disabled", logx.F("dir", dir))
			}
			httpx.NotFound(c)
			return
		}
		if logged != dir {
			logged = dir
			log.Info("frontend_static_enabled", logx.F("dir", dir))
		}
		serveStatic(c, root, p)
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// resolveInside maps a URL path to a regular file under root, refusing
// anything that escapes the root (including through symlinks).
func resolveInside(root, urlPath string) (string, bool) {
	clean := path.Clean("/" + urlPath)
	full := filepath.Join(root, filepath.FromSlash(clean))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return real, true
}

// serveStatic looks up, in order: the exact file, <path>.html,
// <path>/index.html, the root index.html (SPA fallback), 404.html with status
// 404, and finally the 404 envelope.
func serveStatic(c *gin.Context, root, urlPath string) {
	clean := path.Clean("/" + urlPath)
	candidates := []string{clean}
	if clean != "/" {
		candidates = append(candidates, clean+".html", clean+"/index.html")
	}
	candidates = append(candidates, "/index.html")
	for _, cand := range candidates {
		if file, ok := resolveInside(root, cand); ok {
			sendFile(c, file, clean, http.StatusOK)
			return
		}
	}
	if file, ok := resolveInside(root, "/404.html"); ok {
		sendFile(c, file, "/404.html", http.StatusNotFound)
		return
	}
	httpx.NotFound(c)
}

func sendFile(c *gin.Context, file, urlPath string, status int) {
	f, err := os.Open(file)
	if err != nil {
		httpx.NotFound(c)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		httpx.NotFound(c)
		return
	}
	c.Header("Cache-Control", staticCacheControl(urlPath, file))
	if status != http.StatusOK {
		c.Status(status)
		c.Header("Content-Type", "text/html; charset=utf-8")
		if c.Request.Method != http.MethodHead {
			http.ServeContent(&statusWriter{ResponseWriter: c.Writer, status: status}, c.Request, file, st.ModTime(), f)
		}
		return
	}
	http.ServeContent(c.Writer, c.Request, file, st.ModTime(), f)
}

// statusWriter forces the response status for ServeContent, which always
// answers 200 for a plain GET.
type statusWriter struct {
	gin.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		if code == http.StatusOK {
			code = w.status
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

// staticCacheControl implements the cache policy for hosted files.
func staticCacheControl(urlPath, file string) string {
	const immutable = "public, max-age=31536000, immutable"
	switch {
	case strings.HasPrefix(urlPath, "/_next/static/"), strings.HasPrefix(urlPath, "/fonts/"):
		return immutable
	case strings.HasPrefix(urlPath, "/vendor/lobehub-icons/") && strings.HasSuffix(urlPath, ".svg"):
		parts := strings.Split(strings.TrimPrefix(urlPath, "/vendor/lobehub-icons/"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[0][0] >= '0' && parts[0][0] <= '9' {
			return immutable
		}
	}
	ext := strings.ToLower(filepath.Ext(file))
	if ext == ".html" || ext == ".txt" {
		return "no-cache"
	}
	return "public, max-age=3600"
}
