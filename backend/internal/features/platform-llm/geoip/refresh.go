package geoip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// Refresh defaults.
const (
	DefaultRefreshInterval = 168 * time.Hour
	InitialBackoff         = time.Minute
	MaxBackoff             = time.Hour
)

// RefreshSettings configures database download.
type RefreshSettings struct {
	URL      string
	Path     string
	MaxBytes int64
	Interval time.Duration
}

// Refresher keeps a local MaxMind file fresh and swaps the resolver's reader
// atomically after each verified download.
type Refresher struct {
	S        RefreshSettings
	HTTP     Doer
	Resolver *Resolver
	// Sleep waits and must honor ctx; defaults to a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// Log receives failures (optional).
	Log func(msg string, err error)
}

// Download fetches the database once. A declared or actual size above MaxBytes
// is rejected; the file is verified by opening it before it replaces the old
// one.
func (f *Refresher) Download(ctx context.Context) error {
	if f.S.URL == "" || f.S.Path == "" {
		return errors.New("geoip database download is not configured")
	}
	if f.S.MaxBytes <= 0 {
		return errors.New("geoip database size cap must be positive")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.S.URL, nil)
	if err != nil {
		return errors.New("invalid geoip database url")
	}
	resp, err := f.HTTP.Do(ctx, outbound.Endpoint{Kind: outbound.KindStrict, Integration: "geoip database"}, req)
	if err != nil {
		return fmt.Errorf("geoip database download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("geoip database download returned HTTP status %d", resp.StatusCode)
	}
	if resp.ContentLength > f.S.MaxBytes {
		return fmt.Errorf("geoip database exceeds the %d byte limit", f.S.MaxBytes)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, f.S.MaxBytes+1))
	if err != nil {
		return fmt.Errorf("geoip database download failed: %w", err)
	}
	if int64(len(buf)) > f.S.MaxBytes {
		return fmt.Errorf("geoip database exceeds the %d byte limit", f.S.MaxBytes)
	}
	db, err := OpenDatabase(buf)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.S.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.S.Path), ".geoip-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	_ = os.Chmod(name, 0o644)
	if err := os.Rename(name, f.S.Path); err != nil {
		os.Remove(name)
		return err
	}
	if f.Resolver != nil {
		f.Resolver.SetDatabase(db)
	}
	return nil
}

// NextDelay returns the wait after a refresh attempt: the interval on success,
// otherwise a backoff that starts at one minute and doubles up to one hour.
func NextDelay(interval time.Duration, failures int) time.Duration {
	if failures <= 0 {
		if interval <= 0 {
			return DefaultRefreshInterval
		}
		return interval
	}
	d := InitialBackoff
	for i := 1; i < failures && d < MaxBackoff; i++ {
		d *= 2
	}
	if d > MaxBackoff {
		d = MaxBackoff
	}
	return d
}

// Run downloads immediately when the file is missing, then every interval,
// until ctx ends.
func (f *Refresher) Run(ctx context.Context) {
	sleep := f.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	failures := 0
	if _, err := os.Stat(f.S.Path); err == nil {
		if err := sleep(ctx, NextDelay(f.S.Interval, 0)); err != nil {
			return
		}
	}
	for ctx.Err() == nil {
		if err := f.Download(ctx); err != nil {
			failures++
			if f.Log != nil && ctx.Err() == nil {
				f.Log("geoip database refresh failed", err)
			}
		} else {
			failures = 0
		}
		if err := sleep(ctx, NextDelay(f.S.Interval, failures)); err != nil {
			return
		}
	}
}
