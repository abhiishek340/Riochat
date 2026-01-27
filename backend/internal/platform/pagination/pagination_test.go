package pagination

import (
	"math"
	"net/http/httptest"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		page, size string
		want       Page
	}{
		{"", "", Page{1, 20}},
		{"3", "50", Page{3, 50}},
		{"abc", "-5", Page{1, 20}},
		{"0", "0", Page{1, 20}},
		{"2", "5000", Page{2, 1000}},
		{" 4 ", " 10 ", Page{4, 10}},
	}
	for _, c := range cases {
		if got := Parse(c.page, c.size); got != c.want {
			t.Errorf("Parse(%q,%q) = %+v, want %+v", c.page, c.size, got, c.want)
		}
	}
}

func TestOffsetSaturates(t *testing.T) {
	if got := (Page{Page: 3, PageSize: 20}).Offset(); got != 40 {
		t.Fatalf("offset: %d", got)
	}
	if got := (Page{Page: math.MaxInt, PageSize: 1000}).Offset(); got != math.MaxInt {
		t.Fatalf("overflow must saturate, got %d", got)
	}
	if (Page{}).Offset() != 0 || (Page{}).Limit() != 20 {
		t.Fatal("zero value must behave like defaults")
	}
}

func TestFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?page=2&page_size=7", nil)
	if p := FromRequest(r); p.Page != 2 || p.PageSize != 7 {
		t.Fatalf("%+v", p)
	}
}
