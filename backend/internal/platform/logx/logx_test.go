package logx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFoldSortsAndSkipsEmpty(t *testing.T) {
	got := Fold("hello", []Field{F("zeta", 1), F("a.b.alpha", "x"), F("empty", ""), F("nil", nil)})
	if got != "hello alpha x zeta 1" {
		t.Fatalf("unexpected fold: %q", got)
	}
	if Fold("plain", nil) != "plain" {
		t.Fatal("no fields must leave msg untouched")
	}
}

func TestLineHasOnlyKnownKeys(t *testing.T) {
	var buf bytes.Buffer
	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	l := New(&buf, LevelInfo, Options{Now: func() time.Time { return fixed }})
	l.With(F("area", "x")).Info("started", F("port", 8080))
	l.Debug("dropped")
	l.Error("boom", Err(nil))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	for k := range first {
		switch k {
		case "time", "level", "caller", "msg", "stacktrace":
		default:
			t.Fatalf("unexpected key %q", k)
		}
	}
	if first["msg"] != "started area x port 8080" || first["level"] != "info" {
		t.Fatalf("unexpected record: %v", first)
	}
	if _, ok := first["stacktrace"]; ok {
		t.Fatal("info must not carry a stacktrace")
	}
	var second map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if second["stacktrace"] == nil || second["stacktrace"] == "" {
		t.Fatal("error level must carry a stacktrace")
	}
}

func TestNopAndParseLevel(t *testing.T) {
	Nop().Error("ignored")
	var nilLogger *Logger
	nilLogger.Info("safe on nil")
	if ParseLevel("DEBUG") != LevelDebug || ParseLevel("???") != LevelInfo || ParseLevel("warning") != LevelWarn {
		t.Fatal("ParseLevel mismatch")
	}
}
