// Package buildinfo carries release metadata injected at link time.
//
// Example:
//
//	go build -ldflags "-X github.com/riochat/riochat/backend/internal/platform/buildinfo.Version=1.2.3"
package buildinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// Link-time injected values.
var (
	Version   = "dev"
	Commit    = ""
	BuildTime = ""
)

// Info is the resolved build metadata.
type Info struct {
	Version   string
	Commit    string
	BuildTime string
	BuildID   string
}

// Resolve returns the build metadata. When the injected version is empty or
// "dev", the first non-empty content of a file named VERSION found in the
// working directory or up to two parent directories is used instead.
func Resolve() Info {
	wd, _ := os.Getwd()
	return resolve(Version, Commit, BuildTime, wd)
}

func resolve(version, commit, buildTime, startDir string) Info {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		version = versionFromFile(startDir)
	}
	if version == "" {
		version = "dev"
	}
	commit = strings.TrimSpace(commit)
	buildTime = strings.TrimSpace(buildTime)
	parts := []string{version}
	for _, p := range []string{commit, buildTime} {
		if p == "" || p == "unknown" || p == "dev" {
			continue
		}
		parts = append(parts, p)
	}
	return Info{Version: version, Commit: commit, BuildTime: buildTime, BuildID: strings.Join(parts, "-")}
}

func versionFromFile(dir string) string {
	if dir == "" {
		return ""
	}
	for i := 0; i < 3; i++ {
		raw, err := os.ReadFile(filepath.Join(dir, "VERSION"))
		if err == nil {
			if v := strings.TrimSpace(string(raw)); v != "" {
				return v
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
