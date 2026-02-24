package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// readyLine is the single JSON line the desktop sidecar prints on stdout.
type readyLine struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	Origin  string `json:"origin"`
	Grant   string `json:"grant"`
	PID     int    `json:"pid"`
}

// Run starts workers, serves HTTP on the configured address and blocks until
// ctx is canceled, then shuts down in phases. The cancellation cause names the
// trigger in the "server_shutting_down" log ("SIGTERM", "parent_exit", ...).
func (a *App) Run(ctx context.Context) error {
	ln, err := a.Listen()
	if err != nil {
		a.shutdownResources(context.Background())
		return fmt.Errorf("listen: %w", err)
	}
	return a.Serve(ctx, ln)
}

// Serve is Run on an existing listener.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	log := a.Logger
	cfg := a.Config.Load()

	if !a.Server.Sessions.IsSet() {
		log.Warn("session_validator_not_registered", logx.F("detail", "no feature registered a session validator; bearer tokens are accepted on signature and expiry alone"))
	}

	// Local mode publishes its real origin before anything can use it.
	if cfg.IsLocal() {
		origin := "http://" + ln.Addr().String()
		a.Config.Update(func(c *config.Config) {
			c.Server.PublicAPIBaseURL = origin
			c.Server.PublicWebBaseURL = origin
		})
	}

	if err := a.Lifecycle.RunStartHooks(ctx); err != nil {
		_ = ln.Close()
		a.shutdownResources(context.Background())
		return err
	}

	httpSrv := a.NewHTTPServer()
	serveErr := make(chan error, 1)
	log.Info("server_starting", logx.F("address", ln.Addr().String()))
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	if cfg.IsLocal() {
		if err := a.printReady(ctx, ln); err != nil {
			log.Error("local_ready_failed", logx.Err(err))
		}
	}

	var runErr error
	select {
	case <-ctx.Done():
		cause := context.Cause(ctx)
		reason := "context_canceled"
		if cause != nil && !errors.Is(cause, context.Canceled) {
			reason = cause.Error()
		}
		log.Info("server_shutting_down", logx.F("signal", reason))
	case err := <-serveErr:
		if err != nil {
			runErr = fmt.Errorf("serve: %w", err)
			log.Error("server_failed", logx.Err(err))
		}
	}

	a.shutdown(httpSrv)
	log.Info("server_stopped")
	_ = log.Sync()
	return runErr
}

func (a *App) printReady(ctx context.Context, ln net.Listener) error {
	grant, registered, err := a.grant.Issue(ctx)
	if err != nil {
		return fmt.Errorf("issue local login grant: %w", err)
	}
	if !registered {
		a.Logger.Warn("local_grant_not_registered", logx.F("detail", "no feature registered a local login grant issuer; the desktop shell cannot sign in"))
	}
	line, err := json.Marshal(readyLine{
		Type:    "ready",
		Version: a.Build.Version,
		Origin:  "http://" + ln.Addr().String(),
		Grant:   grant,
		PID:     os.Getpid(),
	})
	if err != nil {
		return err
	}
	_, err = a.stdout.Write(append(line, '\n'))
	return err
}

// shutdown performs the phased graceful shutdown:
//
//  1. begin draining (readiness answers 503 "draining");
//  2. stop accepting connections and wait for in-flight requests up to the
//     shutdown timeout;
//  3. on timeout force-close the rest (a warning, not a failure);
//  4. cancel and wait for workers, then wait up to 5 s for detached tasks;
//  5. run stop hooks in reverse order (the cache and database close last).
func (a *App) shutdown(httpSrv *http.Server) {
	log := a.Logger
	a.Lifecycle.Drain().Begin()

	_, _, _, _, timeout := httpTimeouts(a.Config.Load())
	sctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := httpSrv.Shutdown(sctx); err != nil {
		log.Warn("server_drain_timeout_force_close", logx.Err(err))
		_ = httpSrv.Close()
	}
	a.shutdownResources(context.Background())
}

func (a *App) shutdownResources(ctx context.Context) {
	log := a.Logger
	if !a.Lifecycle.StopWorkers(timeoutWorkers) {
		log.Warn("workers_drain_timeout")
	}
	if !a.Lifecycle.WaitDetached(timeoutDetached) {
		log.Warn("background_tasks_drain_timeout")
	}
	a.Lifecycle.RunStopHooks(ctx)
}
