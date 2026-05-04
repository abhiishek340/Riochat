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
