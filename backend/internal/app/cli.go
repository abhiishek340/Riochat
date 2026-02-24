package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
)

// Flags are the command-line options.
type Flags struct {
	Local   bool
	DataDir string
}

// ParseFlags parses and validates the command line.
//
//	--local            run as the desktop sidecar (SQLite, memory cache, loopback)
//	--data-dir <path>  data directory for local mode (required with --local)
func ParseFlags(args []string, usage io.Writer) (Flags, error) {
	var f Flags
	fs := flag.NewFlagSet("riochat", flag.ContinueOnError)
	fs.SetOutput(usage)
	fs.BoolVar(&f.Local, "local", false, "run as a desktop sidecar: SQLite + local storage + in-process cache, loopback-only listener")
	fs.StringVar(&f.DataDir, "data-dir", "", "data directory for local mode (required with --local)")
	if err := fs.Parse(args); err != nil {
		return Flags{}, err
	}
	f.DataDir = strings.TrimSpace(f.DataDir)
	if f.DataDir != "" && !f.Local {
		return Flags{}, errors.New("--data-dir requires --local")
	}
	if f.Local {
		if f.DataDir == "" {
			return Flags{}, errors.New("--local requires --data-dir <absolute path>")
		}
		abs, err := filepath.Abs(f.DataDir)
		if err != nil {
			return Flags{}, fmt.Errorf("resolve --data-dir: %w", err)
		}
		f.DataDir = abs
	}
	return f, nil
}

// Streams are the process standard streams (injectable for tests).
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// RunCLI is the process entry point: parse flags, load and (in local mode)
// override configuration, build the app and serve until a termination signal or
// the parent process exits (standard input reaching EOF in local mode).
func RunCLI(ctx context.Context, args []string, std Streams, list []features.Feature) error {
	flags, err := ParseFlags(args, std.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	cfg, err := config.Load(config.LoadOptions{Warn: std.Stderr})
	if err != nil {
		return err
	}

	logWriter := std.Stdout
	if flags.Local {
		secrets, err := config.LoadOrCreateLocalSecrets(flags.DataDir)
		if err != nil {
			return err
		}
		if err := config.ApplyLocal(cfg, flags.DataDir, secrets); err != nil {
			return err
		}
		// Standard output carries the ready line and nothing else.
		logWriter = std.Stderr
	}

	a, err := Build(ctx, Options{Config: cfg, Features: list, LogWriter: logWriter, Stdout: std.Stdout})
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case sig := <-sigs:
			cancel(errors.New(sig.String()))
		case <-runCtx.Done():
		}
	}()

	if flags.Local && std.Stdin != nil {
		go func() {
			// EOF means the parent exited (including a hard kill).
			_, _ = io.Copy(io.Discard, std.Stdin)
			cancel(errors.New("parent_exit"))
		}()
	}
	return a.Run(runCtx)
}
