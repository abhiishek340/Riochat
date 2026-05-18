// Package dockerx runs the local docker command-line client, used to start and
// stop the managed extraction sidecars.
package dockerx

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Stable error texts.
const (
	TextTimeout       = "docker_timeout"
	TextCommandFailed = "docker_command_failed"
	TextUnavailable   = "docker_unavailable"
)

// ErrContainerNotFound is returned by inspect calls for a missing container.
var ErrContainerNotFound = errors.New("container not found")

// DefaultTimeout applies when a call passes a non-positive timeout.
const DefaultTimeout = 30 * time.Second

// ExecFunc runs a command and returns its combined output.
type ExecFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// Client runs docker commands.
type Client struct {
	// Command is the executable name (default "docker").
	Command string
	// Exec replaces process execution (tests).
	Exec ExecFunc
}

func defaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Run executes `docker args...` with a timeout and returns the combined
// output. A timeout yields an error whose text is "docker_timeout"; a failure
// with no output yields "docker_command_failed", and with output the output
// text; for `container inspect`, output naming a missing object or container
// maps to ErrContainerNotFound.
func (c *Client) Run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	name := c.Command
	if name == "" {
		name = "docker"
	}
	run := c.Exec
	if run == nil {
		if _, err := exec.LookPath(name); err != nil {
			return "", errors.New(TextUnavailable)
		}
		run = defaultExec
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := run(cctx, name, args...)
	text := strings.TrimSpace(string(out))
	if err == nil {
		return text, nil
	}
	if errors.Is(cctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return text, errors.New(TextTimeout)
	}
	if ctx.Err() != nil {
		return text, ctx.Err()
	}
	if isInspect(args) {
		low := strings.ToLower(text)
		if strings.Contains(low, "no such object") || strings.Contains(low, "no such container") {
			return text, ErrContainerNotFound
		}
	}
	if text == "" {
		return "", errors.New(TextCommandFailed)
	}
	return text, errors.New(text)
}

func isInspect(args []string) bool {
	return len(args) >= 2 && args[0] == "container" && args[1] == "inspect"
}
