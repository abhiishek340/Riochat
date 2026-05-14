// Command catalog-snapshot regenerates the model catalog snapshot that is
// compiled into the server (internal/features/channel/builtin_catalog.json.gz).
//
// It downloads the models.dev api.json document (or reads a local copy),
// converts it and replaces the snapshot file atomically. On any failure the
// existing file is left untouched and the command exits non-zero, so a build
// script can treat a refresh as best effort and keep the committed data.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/riochat/riochat/backend/internal/features/channel"
)

const (
	defaultURL = "https://models.dev/api.json"
	defaultOut = "internal/features/channel/builtin_catalog.json.gz"
	maxBody    = 64 << 20
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "catalog-snapshot:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("catalog-snapshot", flag.ContinueOnError)
	url := fs.String("url", defaultURL, "address of the models.dev api.json document")
	from := fs.String("from-file", "", "read the document from this file instead of downloading it")
	out := fs.String("out", defaultOut, "snapshot file to replace")
	timeout := fs.Duration("timeout", 60*time.Second, "download timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var doc []byte
	var err error
	if *from != "" {
		doc, err = os.ReadFile(*from)
	} else {
		doc, err = download(*url, *timeout)
	}
	if err != nil {
		return err
	}
	snap, n, err := channel.EncodeCatalogSnapshot(doc, time.Now())
	if err != nil {
		return fmt.Errorf("convert catalog: %w", err)
	}
	if err := writeAtomic(*out, snap); err != nil {
		return err
	}
	fmt.Printf("catalog-snapshot: wrote %d entries to %s\n", n, *out)
	return nil
}

func download(url string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".catalog-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
