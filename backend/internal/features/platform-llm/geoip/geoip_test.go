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

func TestEligibleAddresses(t *testing.T) {
	for _, ok := range []string{"8.8.8.8", "8.8.8.8:1234", "[2001:4860:4860::8888]:53", "2001:4860:4860::8888", "::ffff:8.8.8.8"} {
		if _, good := Eligible(ok); !good {
			t.Errorf("%s should be eligible", ok)
		}
	}
	for _, bad := range []string{"", "10.0.0.1", "192.168.1.1", "127.0.0.1", "169.254.1.1", "::1", "fe80::1", "0.0.0.0", "224.0.0.1", "nope"} {
		if _, good := Eligible(bad); good {
			t.Errorf("%s must be skipped", bad)
		}
	}
	if _, err := (*Resolver)(nil).Lookup(context.Background(), "8.8.8.8"); err != ErrSkipped {
		t.Fatal("a nil resolver skips silently")
	}
}

type direct struct{ c *http.Client }

func (d direct) Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error) {
	return d.c.Do(req.WithContext(ctx))
}

func TestHTTPProviders(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			io.WriteString(w, `{"country":"fr","region":"IDF","city":"Paris","timezone":"Europe/Paris","loc":"48.85,2.35"}`)
		case strings.Contains(r.URL.Path, "1.1.1.1"):
			io.WriteString(w, `{"success":false}`)
		case strings.Contains(r.URL.Path, "2.2.2.2"):
			w.WriteHeader(500)
		default:
			io.WriteString(w, `{"success":true,"country_code":"DE","region":"Berlin","city":"Berlin","latitude":52.5,"longitude":13.4,"timezone":{"id":"Europe/Berlin"}}`)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	w, err := New(Settings{Provider: "IPWhois", BaseURL: srv.URL + "/"}, direct{srv.Client()})
	if err != nil || w.Source() != SourceAPI {
		t.Fatalf("%v", err)
	}
	loc, err := w.Lookup(ctx, "8.8.4.4")
	if err != nil || loc.CountryCode != "DE" || loc.TimeZone != "Europe/Berlin" || *loc.Latitude != 52.5 {
		t.Fatalf("%v %+v", err, loc)
	}
	if _, err := w.Lookup(ctx, "1.1.1.1"); err == nil {
		t.Fatal("success flag is required")
	}
	if _, err := w.Lookup(ctx, "2.2.2.2"); err == nil {
		t.Fatal("non-2xx is an error")
	}
	if _, err := w.Lookup(ctx, "10.1.1.1"); err != ErrSkipped || len(paths) != 3 {
		t.Fatalf("private addresses never reach the service: %v %v", err, paths)
	}

	i, _ := New(Settings{Provider: "ipinfo", BaseURL: srv.URL, Token: "tk en"}, direct{srv.Client()})
	loc, err = i.Lookup(ctx, "8.8.4.4")
	if err != nil || loc.CountryCode != "FR" || *loc.Longitude != 2.35 {
		t.Fatalf("%v %+v", err, loc)
	}
	if last := paths[len(paths)-1]; last != "/8.8.4.4/json?token=tk+en" {
		t.Fatalf("token goes in the query: %s", last)
	}
}

func TestDisabledAndUnknownProviders(t *testing.T) {
	for _, p := range []string{"", "none", "DISABLED", "  "} {
		if r, err := New(Settings{Provider: p}, nil); r != nil || err != nil {
			t.Fatalf("%q: %v %v", p, r, err)
		}
	}
	if _, err := New(Settings{Provider: "bogus"}, nil); err == nil {
		t.Fatal("unknown provider")
	}
	dir := t.TempDir()
	if r, err := New(Settings{Provider: "mmdb", DatabasePath: filepath.Join(dir, "none.mmdb")}, nil); err != nil || r.Source() != SourceMMDB {
		t.Fatalf("a missing file is tolerated: %v", err)
	}
}

func TestRefreshDownloadsVerifiesAndSwaps(t *testing.T) {
	img := buildDB(4, bitsOf([]byte{8, 8, 8}, 24), sampleRecord())
	var mode atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 0:
			w.Write(img)
		case 1:
			io.WriteString(w, "not a database")
		default:
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "sub", "geoip.mmdb")
	res, _ := New(Settings{Provider: "mmdb", DatabasePath: path}, nil)
	f := &Refresher{S: RefreshSettings{URL: srv.URL, Path: path, MaxBytes: int64(len(img))}, HTTP: direct{srv.Client()}, Resolver: res}

	mode.Store(1)
	if err := f.Download(context.Background()); err == nil {
		t.Fatal("an unreadable download must be rejected")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("nothing is written for a rejected download")
	}
	mode.Store(0)
	if err := f.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loc, err := res.Lookup(context.Background(), "8.8.8.8"); err != nil || loc.City != "Mountain View" {
		t.Fatalf("the reader is swapped: %v %+v", err, loc)
	}
	f.S.MaxBytes = int64(len(img)) - 1
	if err := f.Download(context.Background()); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("size cap: %v", err)
	}
	mode.Store(2)
	f.S.MaxBytes = int64(len(img))
	if err := f.Download(context.Background()); err == nil {
		t.Fatal("HTTP errors fail")
	}
}

func TestNextDelayBackoff(t *testing.T) {
	if NextDelay(0, 0) != 168*time.Hour || NextDelay(5*time.Hour, 0) != 5*time.Hour {
		t.Fatal("interval")
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := NextDelay(time.Hour, i+1); got != w {
			t.Fatalf("failure %d: %v != %v", i+1, got, w)
		}
	}
}

func TestRunRefreshesUntilCanceled(t *testing.T) {
	img := buildDB(4, bitsOf([]byte{8, 8, 8}, 24), sampleRecord())
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.Write(img)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "g.mmdb")
	ctx, cancel := context.WithCancel(context.Background())
	var waits []time.Duration
	f := &Refresher{S: RefreshSettings{URL: srv.URL, Path: path, MaxBytes: 1 << 20, Interval: time.Hour}, HTTP: direct{srv.Client()},
		Sleep: func(ctx context.Context, d time.Duration) error {
			waits = append(waits, d)
			if len(waits) == 2 {
				cancel()
			}
			return ctx.Err()
		}}
	f.Run(ctx)
	if len(waits) != 2 || waits[0] != time.Minute || waits[1] != time.Hour {
		t.Fatalf("backoff after failure, interval after success: %v", waits)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the second attempt wrote the file")
	}
}
