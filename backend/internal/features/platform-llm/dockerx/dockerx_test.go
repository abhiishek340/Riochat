package dockerx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fake(out string, err error) ExecFunc {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) { return []byte(out), err }
}

func TestRunOutcomes(t *testing.T) {
	ctx := context.Background()
	if out, err := (&Client{Exec: fake(" ok\n", nil)}).Run(ctx, time.Second, "ps"); err != nil || out != "ok" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := (&Client{Exec: fake("", errors.New("exit 1"))}).Run(ctx, time.Second, "ps"); err == nil || err.Error() != "docker_command_failed" {
		t.Fatalf("%v", err)
	}
	if out, err := (&Client{Exec: fake("boom happened", errors.New("exit 1"))}).Run(ctx, time.Second, "ps"); err == nil || err.Error() != "boom happened" || out != "boom happened" {
		t.Fatalf("%q %v", out, err)
	}
	for _, msg := range []string{"Error: No such object: x", "Error response: no such container: x"} {
		if _, err := (&Client{Exec: fake(msg, errors.New("exit 1"))}).Run(ctx, time.Second, "container", "inspect", "x"); !errors.Is(err, ErrContainerNotFound) {
			t.Fatalf("%s: %v", msg, err)
		}
	}
	if _, err := (&Client{Exec: fake("no such container", errors.New("exit 1"))}).Run(ctx, time.Second, "rm", "x"); errors.Is(err, ErrContainerNotFound) {
		t.Fatal("only inspect maps to not found")
	}
}

func TestRunTimeoutAndCancel(t *testing.T) {
	slow := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if _, err := (&Client{Exec: slow}).Run(context.Background(), 20*time.Millisecond, "pull", "x"); err == nil || err.Error() != "docker_timeout" {
		t.Fatalf("%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Client{Exec: slow}).Run(ctx, time.Second, "pull", "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := (&Client{Command: "definitely-not-a-real-docker-binary"}).Run(context.Background(), time.Second, "ps"); err == nil || err.Error() != "docker_unavailable" {
		t.Fatalf("%v", err)
	}
}
