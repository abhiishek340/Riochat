package channel

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// isErr reports whether err matches target by error code.
func isErr(err error, target error) bool { return errors.Is(err, target) }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, p := range subs {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }

func itoa(n int) string { return strconv.Itoa(n) }

// gormDB abbreviates the GORM handle type in function signatures.
type gormDB = gorm.DB

func jsonObject(m map[string]string) string {
	raw, _ := json.Marshal(m)
	return string(raw)
}

// pageWindow turns a 1-based page and a size into slice bounds over n items.
// The arithmetic saturates so an absurd page number selects an empty window
// instead of overflowing.
func pageWindow(page, size, n int) (from, to int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 1
	}
	if n < 0 {
		n = 0
	}
	if page-1 > n/size+1 {
		return n, n
	}
	from = (page - 1) * size
	if from > n {
		from = n
	}
	to = from + size
	if to > n || to < from {
		to = n
	}
	return from, to
}

// pageOffset is the SQL offset of a page; it saturates instead of overflowing.
func pageOffset(page, size int) int {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 1
	}
	if page-1 > math.MaxInt32/size {
		return math.MaxInt32
	}
	return (page - 1) * size
}

func msDuration(ms, def int) time.Duration {
	return time.Duration(timeoutOrDefault(ms, def)) * time.Millisecond
}
