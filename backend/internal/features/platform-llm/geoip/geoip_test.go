package geoip

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// ---- a tiny MaxMind DB writer for tests -----------------------------------

func ctrl(typ, size int) []byte {
	var out []byte
	t := typ
	if typ > 7 {
		t = 0
	}
	switch {
	case size < 29:
		out = []byte{byte(t<<5 | size)}
	case size < 285:
		out = []byte{byte(t<<5 | 29), byte(size - 29)}
	default:
		s := size - 285
		out = []byte{byte(t<<5 | 30), byte(s >> 8), byte(s)}
	}
	if typ > 7 {
		out = append(out[:1], append([]byte{byte(typ - 7)}, out[1:]...)...)
	}
	return out
}

func encStr(s string) []byte { return append(ctrl(2, len(s)), s...) }
func encDouble(f float64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, math.Float64bits(f))
	return append(ctrl(3, 8), b...)
}
func encUint(typ int, v uint64) []byte {
	var b []byte
	for x := v; x > 0; x >>= 8 {
		b = append([]byte{byte(x)}, b...)
	}
	return append(ctrl(typ, len(b)), b...)
}
func encMap(kv ...any) []byte {
	out := ctrl(7, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, encStr(kv[i].(string))...)
		out = append(out, kv[i+1].([]byte)...)
	}
	return out
}
func encArray(items ...[]byte) []byte {
	out := ctrl(11, len(items))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

// buildDB writes a database whose only record covers the given bit path.
func buildDB(ipVersion int, path []int, record []byte) []byte {
	nodes := len(path)
	var tree []byte
	for i, bit := range path {
		var l, r uint32 = uint32(nodes), uint32(nodes)
		next := uint32(i + 1)
		if i == nodes-1 {
			next = uint32(nodes) + 16
		}
		if bit == 0 {
			l = next
		} else {
			r = next
		}
		tree = append(tree, byte(l>>16), byte(l>>8), byte(l), byte(r>>16), byte(r>>8), byte(r))
	}
	var buf bytes.Buffer
	buf.Write(tree)
	buf.Write(make([]byte, 16))
	buf.Write(record)
	buf.Write([]byte("\xAB\xCD\xEFMaxMind.com"))
	buf.Write(encMap("node_count", encUint(6, uint64(nodes)), "record_size", encUint(5, 24), "ip_version", encUint(5, uint64(ipVersion))))
	return buf.Bytes()
}

func bitsOf(b []byte, n int) []int {
	var out []int
	for i := 0; i < n; i++ {
		out = append(out, int(b[i/8]>>(7-uint(i%8)))&1)
	}
	return out
}

func sampleRecord() []byte {
	return encMap(
		"country", encMap("iso_code", encStr("US")),
		"city", encMap("names", encMap("en", encStr("Mountain View"), "de", encStr("Bergansicht"))),
		"subdivisions", encArray(encMap("names", encMap("fr", encStr("Californie"), "en", encStr("California")))),
		"location", encMap("latitude", encDouble(37.386), "longitude", encDouble(-122.0838), "time_zone", encStr("America/Los_Angeles")),
	)
}

func TestMMDBLookupIPv4AndIPv6Trees(t *testing.T) {
	v4 := buildDB(4, bitsOf([]byte{8, 8, 8}, 24), sampleRecord())
	var six []int
	six = append(six, make([]int, 96)...)
	six = append(six, bitsOf([]byte{8, 8, 8}, 24)...)
	v6 := buildDB(6, six, sampleRecord())
	for name, img := range map[string][]byte{"v4": v4, "v6": v6} {
		db, err := OpenDatabase(img)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r := &Resolver{kind: ProviderMMDB, langs: []string{"en"}}
		r.SetDatabase(db)
		loc, err := r.Lookup(context.Background(), "8.8.8.8:443")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if loc.CountryCode != "US" || loc.City != "Mountain View" || loc.Region != "California" || loc.TimeZone != "America/Los_Angeles" ||
			loc.Latitude == nil || *loc.Latitude != 37.386 || *loc.Longitude != -122.0838 {
			t.Fatalf("%s: %+v", name, loc)
		}
		if _, err := r.Lookup(context.Background(), "9.9.9.9"); err == nil {
			t.Fatalf("%s: addresses outside the record are not found", name)
		}
	}
	// Localized name selection: preferred language, then alphabetical fallback.
	db, _ := OpenDatabase(v4)
	r := &Resolver{kind: ProviderMMDB, langs: []string{"ja", "de"}}
	r.SetDatabase(db)
	loc, _ := r.Lookup(context.Background(), "8.8.8.8")
	if loc.City != "Bergansicht" || loc.Region != "California" {
		t.Fatalf("%+v", loc)
	}
	if _, err := OpenDatabase([]byte("junk")); err == nil {
		t.Fatal("garbage is not a database")
	}
	if _, err := (&Resolver{kind: ProviderMMDB}).Lookup(context.Background(), "8.8.8.8"); err != ErrNoDatabase {
		t.Fatalf("%v", err)
	}
}
