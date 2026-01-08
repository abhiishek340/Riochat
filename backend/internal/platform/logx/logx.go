// Package logx is the process logger. It writes JSON lines with exactly six
// possible keys (time, level, caller, msg, stacktrace) and folds structured
// fields into the message text so downstream log tooling sees a fixed schema.
package logx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level is a log severity.
type Level int

// Severity levels, ascending.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// Field is one structured key/value pair attached to a log call.
type Field struct {
	Key   string
	Value any
}

// F builds a Field.
func F(key string, value any) Field { return Field{Key: key, Value: value} }

// Err builds a Field named "error" from err (skipped when err is nil).
func Err(err error) Field {
	if err == nil {
		return Field{Key: "error"}
	}
	return Field{Key: "error", Value: err.Error()}
}

type sink struct {
	mu    sync.Mutex
	w     io.Writer
	level Level
	now   func() time.Time
}

// Logger is a leveled structured logger. The zero value is not usable; build
// one with New or Nop. Loggers derived with With share the same output.
type Logger struct {
	sink   *sink
	fields []Field
}

// Options tune a Logger.
type Options struct {
	// Now overrides the timestamp source (tests).
	Now func() time.Time
}

// New returns a logger writing JSON lines to w at or above level.
func New(w io.Writer, level Level, opts ...Options) *Logger {
	s := &sink{w: w, level: level, now: time.Now}
	for _, o := range opts {
		if o.Now != nil {
			s.now = o.Now
		}
	}
	return &Logger{sink: s}
}

// Nop returns a logger that discards everything.
func Nop() *Logger { return New(io.Discard, LevelError+1) }

// ParseLevel maps a name to a Level; unknown names yield LevelInfo.
func ParseLevel(name string) Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// With returns a child logger that adds fields to every message.
func (l *Logger) With(fields ...Field) *Logger {
	if l == nil {
		return Nop()
	}
	merged := make([]Field, 0, len(l.fields)+len(fields))
	merged = append(merged, l.fields...)
	merged = append(merged, fields...)
	return &Logger{sink: l.sink, fields: merged}
}

// Enabled reports whether messages at level would be written.
func (l *Logger) Enabled(level Level) bool {
	return l != nil && l.sink != nil && level >= l.sink.level
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, fields ...Field) { l.log(LevelDebug, msg, fields) }

// Info logs at info level.
func (l *Logger) Info(msg string, fields ...Field) { l.log(LevelInfo, msg, fields) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, fields ...Field) { l.log(LevelWarn, msg, fields) }

// Error logs at error level and attaches a stack trace.
func (l *Logger) Error(msg string, fields ...Field) { l.log(LevelError, msg, fields) }

// Log writes at an explicit level.
func (l *Logger) Log(level Level, msg string, fields ...Field) { l.log(level, msg, fields) }

// Sync flushes buffered output when the writer supports it.
func (l *Logger) Sync() error {
	if l == nil || l.sink == nil {
		return nil
	}
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	if s, ok := l.sink.w.(interface{ Sync() error }); ok {
		return s.Sync()
	}
	return nil
}

type line struct {
	Time       string `json:"time"`
	Level      string `json:"level"`
	Caller     string `json:"caller,omitempty"`
	Msg        string `json:"msg"`
	Stacktrace string `json:"stacktrace,omitempty"`
}

func (l *Logger) log(level Level, msg string, fields []Field) {
	if !l.Enabled(level) {
		return
	}
	all := fields
	if len(l.fields) > 0 {
		all = append(append([]Field{}, l.fields...), fields...)
	}
	rec := line{
		Time:  l.sink.now().Format("2006-01-02T15:04:05.000Z07:00"),
		Level: level.String(),
		Msg:   Fold(msg, all),
	}
	if _, file, ln, ok := runtime.Caller(2); ok {
		rec.Caller = fmt.Sprintf("%s/%s:%d", filepath.Base(filepath.Dir(file)), filepath.Base(file), ln)
	}
	if level >= LevelError {
		rec.Stacktrace = stack()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep '&', '<', '>' readable in log messages
	if err := enc.Encode(rec); err != nil {
		return
	}
	l.sink.mu.Lock()
	_, _ = l.sink.w.Write(buf.Bytes())
	l.sink.mu.Unlock()
}

// Fold appends fields to msg as space separated "key value" pairs sorted by
// key. Dotted keys keep only their last segment and empty values are skipped.
func Fold(msg string, fields []Field) string {
	if len(fields) == 0 {
		return msg
	}
	type kv struct{ k, v string }
	pairs := make([]kv, 0, len(fields))
	for _, f := range fields {
		key := f.Key
		if i := strings.LastIndexByte(key, '.'); i >= 0 {
			key = key[i+1:]
		}
		val := render(f.Value)
		if key == "" || val == "" {
			continue
		}
		pairs = append(pairs, kv{key, val})
	}
	if len(pairs) == 0 {
		return msg
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
	var b strings.Builder
	b.WriteString(msg)
	for _, p := range pairs {
		b.WriteByte(' ')
		b.WriteString(p.k)
		b.WriteByte(' ')
		b.WriteString(p.v)
	}
	return b.String()
}

func render(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case error:
		return x.Error()
	case fmt.Stringer:
		return x.String()
	case time.Duration:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func stack() string {
	buf := make([]byte, 8192)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}
