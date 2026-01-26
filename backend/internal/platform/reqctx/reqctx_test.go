package reqctx

import (
	"context"
	"errors"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	s := NewState("req-1", "trace-1")
	s.SetClient("10.0.0.1", true)
	s.SetPrincipal(Principal{UserID: 3, Role: RoleAdmin})
	s.RecordFailure("internal.error", errors.New("db down"), "detail")

	ctx := Attach(context.Background(), s)
	got := From(ctx)
	if got.RequestID() != "req-1" || got.TraceID() != "trace-1" || got.ClientIP() != "10.0.0.1" || !got.FromTrustedProxy() {
		t.Fatalf("identifiers lost: %+v", got)
	}
	p, ok := got.Principal()
	if !ok || p.UserID != 3 || !p.IsAdmin() {
		t.Fatalf("principal lost: %+v %v", p, ok)
	}
	code, cause, diag := got.Failure()
	if code != "internal.error" || cause == nil || diag != "detail" {
		t.Fatalf("failure lost: %q %v %q", code, cause, diag)
	}
}

func TestFromMissingIsSafeAndDetachKeepsValues(t *testing.T) {
	if From(context.Background()).RequestID() != "" {
		t.Fatal("empty state expected")
	}
	if _, ok := From(nil).Principal(); ok { //nolint:staticcheck // nil context is tolerated on purpose
		t.Fatal("no principal expected")
	}
	ctx, cancel := context.WithCancel(Attach(context.Background(), NewState("r", "t")))
	d := Detach(ctx)
	cancel()
	if d.Err() != nil {
		t.Fatal("detached context must not be canceled")
	}
	if From(d).RequestID() != "r" {
		t.Fatal("detached context must keep the state")
	}
	if (Principal{Role: "user"}).IsAdmin() {
		t.Fatal("user is not admin")
	}
}
