package buildinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveInjected(t *testing.T) {
	info := resolve("1.2.3", "abc123", "2026-01-01", t.TempDir())
	if info.Version != "1.2.3" || info.BuildID != "1.2.3-abc123-2026-01-01" {
		t.Fatalf("unexpected info: %+v", info)
	}
	info = resolve("1.2.3", "unknown", "dev", t.TempDir())
	if info.BuildID != "1.2.3" {
		t.Fatalf("unknown/dev parts must be omitted: %+v", info)
	}
}

func TestResolveVersionFileFromParent(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "VERSION"), []byte("\n 7.8.9 \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := resolve("dev", "", "", child)
	if info.Version != "7.8.9" {
		t.Fatalf("expected version from parent file, got %q", info.Version)
	}
	if got := resolve("", "", "", t.TempDir()).Version; got != "dev" {
		t.Fatalf("fallback should be dev, got %q", got)
	}
}
