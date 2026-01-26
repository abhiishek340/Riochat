// Package pagination parses page/page_size query parameters and applies them
// to database queries.
package pagination

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// Defaults and limits.
const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 1000
)

// Page is a resolved pagination request.
type Page struct {
	Page     int
	PageSize int
}

// Parse resolves raw page and page_size values. Non-numeric or non-positive
// values fall back to the defaults; the page size is capped at MaxPageSize.
func Parse(page, pageSize string) Page {
	p := Page{Page: DefaultPage, PageSize: DefaultPageSize}
	if n, err := strconv.Atoi(strings.TrimSpace(page)); err == nil && n > 0 {
		p.Page = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(pageSize)); err == nil && n > 0 {
		p.PageSize = n
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
	return p
}

// FromRequest reads the "page" and "page_size" query parameters.
func FromRequest(r *http.Request) Page {
	q := r.URL.Query()
	return Parse(q.Get("page"), q.Get("page_size"))
}

// Offset is (page-1)*page_size, saturating at the maximum int instead of
// overflowing.
func (p Page) Offset() int {
	page, size := p.Page, p.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = DefaultPageSize
	}
	if page-1 > math.MaxInt/size {
		return math.MaxInt
	}
	return (page - 1) * size
}

// Limit is the page size.
func (p Page) Limit() int {
	if p.PageSize < 1 {
		return DefaultPageSize
	}
	return p.PageSize
}

// Scope applies the page as OFFSET/LIMIT:
//
//	db.Scopes(pagination.Scope(p)).Find(&rows)
func Scope(p Page) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB { return db.Offset(p.Offset()).Limit(p.Limit()) }
}
