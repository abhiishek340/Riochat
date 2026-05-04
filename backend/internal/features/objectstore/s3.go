package objectstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3Options configures the S3-compatible backend.
type S3Options struct {
	// Endpoint is the service URL; blank means the provider default for Region.
	Endpoint string
	// Region defaults to "auto" when blank.
	Region string
	Bucket string
	// Prefix is prepended to every key as a folder; callers and databases keep
	// the unprefixed key.
	Prefix string
	// AccessKeyID and SecretAccessKey are optional; when both are blank the
	// ambient credentials (AWS_* environment variables) are used.
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle selects https://host/bucket/key addressing over
	// https://bucket.host/key.
	PathStyle bool
	// HTTP is the transport; http.DefaultClient when nil.
	HTTP *http.Client
	// Getenv reads the ambient credentials; os.Getenv when nil.
	Getenv func(string) string
	// Now is the clock used for signing; time.Now when nil.
	Now func() time.Time
}

// S3 talks to an S3-compatible service with Signature Version 4. No
// pre-signed or public URLs are ever produced: downloads go through the API.
type S3 struct {
	o        S3Options
	endpoint *url.URL
	prefix   string
}

// NewS3 validates the options and returns the backend.
func NewS3(o S3Options) (*S3, error) {
	if strings.TrimSpace(o.Bucket) == "" {
		return nil, fmt.Errorf("%w: s3 bucket is required", ErrInvalidConfig)
	}
	o.Bucket = strings.TrimSpace(o.Bucket)
	o.Region = strings.TrimSpace(o.Region)
	if o.Region == "" {
		o.Region = "auto"
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	raw := strings.TrimSpace(o.Endpoint)
	if raw == "" {
		raw = "https://s3." + o.Region + ".amazonaws.com"
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: s3 endpoint", ErrInvalidConfig)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return &S3{o: o, endpoint: u, prefix: strings.Trim(strings.TrimSpace(o.Prefix), "/")}, nil
}

// ObjectKey returns the key as stored in the bucket (prefix applied).
func (s *S3) ObjectKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

func (s *S3) objectURL(key string) *url.URL {
	full := s.ObjectKey(key)
	u := *s.endpoint
	if s.o.PathStyle {
		u.Path = s.endpoint.Path + "/" + s.o.Bucket + "/" + full
	} else {
		u.Host = s.o.Bucket + "." + s.endpoint.Host
		u.Path = s.endpoint.Path + "/" + full
	}
	return &u
}

// S3Error carries the service error code of a failed request. The response
// body is never kept.
type S3Error struct {
	Status int
	Code   string
}

func (e *S3Error) Error() string {
	if e.Code != "" {
		return "objectstore: s3 request failed: " + e.Code + " (" + strconv.Itoa(e.Status) + ")"
	}
	return "objectstore: s3 request failed: status " + strconv.Itoa(e.Status)
}

func (s *S3) credentials() (id, secret, token string) {
	if s.o.AccessKeyID != "" || s.o.SecretAccessKey != "" {
		return s.o.AccessKeyID, s.o.SecretAccessKey, ""
	}
	return s.o.Getenv("AWS_ACCESS_KEY_ID"), s.o.Getenv("AWS_SECRET_ACCESS_KEY"), s.o.Getenv("AWS_SESSION_TOKEN")
}

// Write spools the stream to a temporary file to learn its hash and exact
// length (which Signature V4 needs), then uploads it.
func (s *S3) Write(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Info, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return Info{}, err
	}
	tmp, err := os.CreateTemp("", "objectstore-up-*")
	if err != nil {
		return Info{}, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), &ctxReader{ctx: ctx, r: r})
	if err != nil {
		return Info{}, err
	}
	if size >= 0 && n != size {
		return Info{}, io.ErrUnexpectedEOF
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return Info{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(k).String(), tmp)
	if err != nil {
		return Info{}, err
	}
	req.ContentLength = n
	if n == 0 {
		req.Body = http.NoBody
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	s.sign(req, hex.EncodeToString(h.Sum(nil)))
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Info{}, readS3Error(resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return Info{Key: k, Size: n, ContentType: contentType, ModTime: s.o.Now()}, nil
}

// Open streams an object; NoSuchKey, NotFound and 404 become ErrNotFound.
func (s *S3) Open(ctx context.Context, key string) (*Object, error) {
	k, err := NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(k).String(), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, emptySHA256)
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		e := readS3Error(resp)
		if se, ok := e.(*S3Error); ok && isMissing(se) {
			return nil, ErrNotFound
		}
		return nil, e
	}
	mod, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return &Object{Body: resp.Body, Size: resp.ContentLength, ContentType: resp.Header.Get("Content-Type"), ModTime: mod}, nil
}

// Delete removes an object; a missing key and an empty key succeed.
func (s *S3) Delete(ctx context.Context, key string) error {
	if strings.Trim(strings.TrimSpace(strings.ReplaceAll(key, "\\", "/")), "/") == "" {
		return nil
	}
	k, err := NormalizeKey(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(k).String(), nil)
	if err != nil {
		return err
	}
	s.sign(req, emptySHA256)
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	e := readS3Error(resp)
	if se, ok := e.(*S3Error); ok && isMissing(se) {
		return nil
	}
	return e
}

// Materialize downloads the object to a temporary file; cleanup removes it.
func (s *S3) Materialize(ctx context.Context, key string) (string, func(), error) {
	obj, err := s.Open(ctx, key)
	if err != nil {
		return "", nil, err
	}
	defer obj.Body.Close()
	tmp, err := os.CreateTemp("", "objectstore-dl-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	if _, err := io.Copy(tmp, &ctxReader{ctx: ctx, r: obj.Body}); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp.Name(), cleanup, nil
}

func isMissing(e *S3Error) bool {
	switch e.Code {
	case "NoSuchKey", "NotFound", "404":
		return true
	}
	return e.Status == http.StatusNotFound && (e.Code == "" || e.Code == "NoSuchKey")
}

func readS3Error(resp *http.Response) error {
	e := &S3Error{Status: resp.StatusCode}
	var body struct {
		Code string `xml:"Code"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if xml.Unmarshal(raw, &body) == nil {
		e.Code = strings.TrimSpace(body.Code)
	}
	return e
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sign adds Signature Version 4 headers for the S3 service.
func (s *S3) sign(req *http.Request, payloadHash string) {
	id, secret, token := s.credentials()
	now := s.o.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("x-amz-date", stamp)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if token != "" {
		req.Header.Set("x-amz-security-token", token)
	}
	if id == "" || secret == "" {
		return // anonymous access
	}
	host := req.URL.Host
	signed := map[string]string{"host": host, "x-amz-content-sha256": payloadHash, "x-amz-date": stamp}
	if token != "" {
		signed["x-amz-security-token"] = token
	}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		signed["content-type"] = ct
	}
	names := make([]string, 0, len(signed))
	for n := range signed {
		names = append(names, n)
	}
	sort.Strings(names)
	var canon strings.Builder
	for _, n := range names {
		canon.WriteString(n + ":" + strings.TrimSpace(signed[n]) + "\n")
	}
	signedList := strings.Join(names, ";")
	canonical := strings.Join([]string{
		req.Method, canonicalPath(req.URL.Path), req.URL.RawQuery, canon.String(), signedList, payloadHash,
	}, "\n")
	scope := date + "/" + s.o.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hexSHA256(canonical)
	sig := hex.EncodeToString(hmacSHA256(signingKey(secret, date, s.o.Region, "s3"), toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+id+"/"+scope+", SignedHeaders="+signedList+", Signature="+sig)
}

// canonicalPath encodes each path segment once, as S3 requires.
func canonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	segs := strings.Split(p, "/")
	for i, sg := range segs {
		segs[i] = awsEscape(sg)
	}
	return strings.Join(segs, "/")
}

func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func signingKey(secret, date, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, "aws4_request")
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

func hexSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
