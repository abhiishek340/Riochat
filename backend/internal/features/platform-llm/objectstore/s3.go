package objectstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
)

const unsignedPayload = "UNSIGNED-PAYLOAD"

// S3Options configures the S3-compatible backend.
type S3Options struct {
	Endpoint        string // optional custom base endpoint
	Region          string // default "auto"
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	PathStyle       bool
	HTTPClient      *http.Client
	Tracer          *trace.Tracer
	Now             func() time.Time
	Getenv          func(string) string
}

// S3 talks to an S3-compatible service using AWS Signature Version 4.
type S3 struct {
	o      S3Options
	prefix string
	creds  *credentials
}

type credentials struct{ id, secret, token string }

// NewS3 validates options and returns a store.
func NewS3(o S3Options) (*S3, error) {
	if strings.TrimSpace(o.Bucket) == "" {
		return nil, errors.New("s3 bucket is required")
	}
	o.Bucket = strings.TrimSpace(o.Bucket)
	if strings.TrimSpace(o.Region) == "" {
		o.Region = "auto"
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	s := &S3{o: o}
	if strings.TrimSpace(o.Prefix) != "" {
		p := strings.Trim(strings.ReplaceAll(strings.TrimSpace(o.Prefix), "\\", "/"), "/")
		s.prefix = p
	}
	switch {
	case o.AccessKeyID != "" || o.SecretAccessKey != "":
		s.creds = &credentials{o.AccessKeyID, o.SecretAccessKey, o.SessionToken}
	default:
		// Default credential chain (environment subset).
		if id := o.Getenv("AWS_ACCESS_KEY_ID"); id != "" {
			s.creds = &credentials{id, o.Getenv("AWS_SECRET_ACCESS_KEY"), o.Getenv("AWS_SESSION_TOKEN")}
		}
	}
	return s, nil
}

// ObjectKey returns the full object name for a store key (prefix applied).
func (s *S3) ObjectKey(key string) (string, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return "", err
	}
	if s.prefix != "" {
		return s.prefix + "/" + k, nil
	}
	return k, nil
}

func (s *S3) region() string {
	if s.o.Region == "auto" {
		return "us-east-1"
	}
	return s.o.Region
}

// encodePath percent-encodes everything except unreserved characters and "/".
func encodePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' || c == '/' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (s *S3) objectURL(objectKey string) (*url.URL, error) {
	var base *url.URL
	var err error
	if ep := strings.TrimSpace(s.o.Endpoint); ep != "" {
		if !strings.Contains(ep, "://") {
			ep = "https://" + ep
		}
		base, err = url.Parse(ep)
		if err != nil || base.Host == "" {
			return nil, fmt.Errorf("invalid s3 endpoint %q", s.o.Endpoint)
		}
	} else {
		base = &url.URL{Scheme: "https", Host: "s3." + s.region() + ".amazonaws.com"}
		if s.region() == "us-east-1" {
			base.Host = "s3.amazonaws.com"
		}
	}
	u := &url.URL{Scheme: base.Scheme, Host: base.Host}
	basePath := strings.TrimRight(base.Path, "/")
	var path string
	if s.o.PathStyle {
		path = basePath + "/" + s.o.Bucket + "/" + objectKey
	} else {
		u.Host = s.o.Bucket + "." + base.Host
		path = basePath + "/" + objectKey
	}
	enc := encodePath(path)
	u.Path, _ = url.PathUnescape(enc)
	u.RawPath = enc
	return u, nil
}

func (s *S3) newRequest(ctx context.Context, method, key string, body io.Reader, size int64, contentType string) (*http.Request, error) {
	ok, err := s.ObjectKey(key)
	if err != nil {
		return nil, err
	}
	u, err := s.objectURL(ok)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil && size >= 0 {
		req.ContentLength = size
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	s.sign(req, unsignedPayload, s.o.Now())
	return req, nil
}

// sign adds SigV4 headers. A store without credentials sends anonymous requests.
func (s *S3) sign(req *http.Request, payloadHash string, now time.Time) {
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if s.creds == nil {
		return
	}
	signV4(req, s.creds, s.region(), "s3", payloadHash, now)
}

func (s *S3) do(req *http.Request) (*http.Response, error) {
	return s.o.HTTPClient.Do(req)
}

type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func (s *S3) failure(op string, resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e s3Error
	_ = xml.Unmarshal(b, &e)
	switch {
	case e.Code == "NoSuchKey" || e.Code == "NotFound" || e.Code == "404" || resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	}
	msg := e.Message
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("s3 %s failed: status %d code %q %s", op, resp.StatusCode, e.Code, msg)
}

func (s *S3) span(ctx context.Context, op string, size int64, ct string) (context.Context, *trace.Span) {
	return s.o.Tracer.Start(ctx, "objectstore.s3."+op, trace.KindClient,
		trace.String("system", "s3"), trace.String("bucket", s.o.Bucket),
		trace.Int("size", size), trace.String("content_type", ct))
}

// Put uploads an object. With an unknown size the content is spooled to a
// temporary file first, since S3 needs the length up front.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (info Info, err error) {
	clean, err := NormalizeKey(key)
	if err != nil {
		return Info{}, err
	}
	ctx, span := s.span(ctx, "put", size, contentType)
	defer func() { span.RecordError(err); span.End() }()

	body := r
	if size < 0 {
		tmp, terr := os.CreateTemp("", "riochat-put-*")
		if terr != nil {
			return Info{}, terr
		}
		defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
		n, terr := io.Copy(tmp, &ctxReader{ctx: ctx, r: r})
		if terr != nil {
			return Info{}, terr
		}
		if _, terr = tmp.Seek(0, io.SeekStart); terr != nil {
			return Info{}, terr
		}
		body, size = tmp, n
	}
	cr := &countingReader{r: body}
	req, err := s.newRequest(ctx, http.MethodPut, key, cr, size, contentType)
	if err != nil {
		return Info{}, err
	}
	if size == 0 {
		req.Body = http.NoBody
	}
	resp, err := s.do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Info{}, s.failure("put", resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return Info{Key: clean, Size: size, ContentType: contentType, ModTime: s.o.Now()}, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Open starts a download. The tracing span ends when the stream is closed.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	clean, err := NormalizeKey(key)
	if err != nil {
		return nil, Info{}, err
	}
	ctx, span := s.span(ctx, "open", -1, "")
	fail := func(err error) (io.ReadCloser, Info, error) {
		span.RecordError(err)
		span.End()
		return nil, Info{}, err
	}
	req, err := s.newRequest(ctx, http.MethodGet, key, nil, -1, "")
	if err != nil {
		return fail(err)
	}
	resp, err := s.do(req)
	if err != nil {
		return fail(err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return fail(s.failure("open", resp))
	}
	info := Info{Key: clean, Size: resp.ContentLength, ContentType: resp.Header.Get("Content-Type")}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		info.ModTime = t
	}
	span.SetAttr(trace.Int("size", info.Size), trace.String("content_type", info.ContentType))
	return &spanCloser{ReadCloser: resp.Body, span: span}, info, nil
}

type spanCloser struct {
	io.ReadCloser
	span *trace.Span
}

func (c *spanCloser) Close() error {
	err := c.ReadCloser.Close()
	c.span.End()
	return err
}

// Delete removes an object; a missing object or invalid key is not an error.
func (s *S3) Delete(ctx context.Context, key string) (err error) {
	if _, kerr := NormalizeKey(key); kerr != nil {
		return nil
	}
	ctx, span := s.span(ctx, "delete", -1, "")
	defer func() { span.RecordError(err); span.End() }()
	req, err := s.newRequest(ctx, http.MethodDelete, key, nil, -1, "")
	if err != nil {
		return err
	}
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	if ferr := s.failure("delete", resp); !errors.Is(ferr, ErrNotFound) {
		return ferr
	}
	return nil
}

// Materialize downloads the object into a temporary file named with the
// prefix "riochat-object-".
func (s *S3) Materialize(ctx context.Context, key string) (string, func(), error) {
	rc, _, err := s.Open(ctx, key)
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()
	f, err := os.CreateTemp("", "riochat-object-*")
	if err != nil {
		return "", nil, err
	}
	name := f.Name()
	if _, err := io.Copy(f, &ctxReader{ctx: ctx, r: rc}); err != nil {
		f.Close()
		os.Remove(name)
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", nil, err
	}
	return name, func() { _ = os.Remove(name) }, nil
}

// ---- SigV4 ----------------------------------------------------------------

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// signV4 signs req in place. Signed headers: host, range, content-type,
// content-md5 and every x-amz-* header present.
func signV4(req *http.Request, c *credentials, region, service, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	if c.token != "" {
		req.Header.Set("x-amz-security-token", c.token)
	}
	host := req.URL.Host
	if req.Host != "" {
		host = req.Host
	}
	names := []string{"host"}
	vals := map[string]string{"host": host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if lk == "range" || lk == "content-type" || lk == "content-md5" || strings.HasPrefix(lk, "x-amz-") {
			vals[lk] = strings.Join(strings.Fields(strings.Join(v, ",")), " ")
			names = append(names, lk)
		}
	}
	sort.Strings(names)
	var canon strings.Builder
	for _, n := range names {
		canon.WriteString(n + ":" + vals[n] + "\n")
	}
	signed := strings.Join(names, ";")

	uri := req.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	query := canonicalQuery(req.URL.Query())
	creq := strings.Join([]string{req.Method, uri, query, canon.String(), signed, payloadHash}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(creq))
	k := hmacSHA256([]byte("AWS4"+c.secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, sts))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.id+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, encodeQuery(k)+"="+encodeQuery(v))
		}
	}
	return strings.Join(parts, "&")
}

func encodeQuery(s string) string {
	return strings.ReplaceAll(encodePath(s), "/", "%2F")
}
