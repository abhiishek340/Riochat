package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestResolveTypedAndGeneric(t *testing.T) {
	code, msg, _ := Resolve(http.StatusBadRequest, ErrInvalidBody)
	if code != "request.invalid_body" || msg != "invalid request body" {
		t.Fatalf("typed: %q %q", code, msg)
	}
	// plain errors never leak
	code, msg, _ = Resolve(http.StatusInternalServerError, errors.New("password=hunter2"))
	if code != "internal.error" || msg != "internal server error" {
		t.Fatalf("plain: %q %q", code, msg)
	}
	// unregistered typed codes degrade to the generic contract
	code, msg, _ = Resolve(http.StatusNotFound, New("made.up", "nope"))
	if code != "resource.not_found" || msg != "resource not found" {
		t.Fatalf("unregistered: %q %q", code, msg)
	}
	// wrapped chains are searched
	wrapped := fmt.Errorf("ctx: %w", ErrConflict.WithCause(errors.New("dup key")))
	code, msg, _ = Resolve(http.StatusConflict, wrapped)
	if code != "resource.conflict" || msg != "resource conflict" {
		t.Fatalf("wrapped: %q %q", code, msg)
	}
	if !errors.Is(wrapped, ErrConflict) {
		t.Fatal("errors.Is must match by code")
	}
}

func TestGenericTable(t *testing.T) {
	cases := map[int]string{
		401: "auth.unauthorized", 403: "auth.forbidden", 404: "resource.not_found",
		409: "resource.conflict", 402: "billing.payment_required", 429: "rate_limit.exceeded",
		502: "upstream.unavailable", 503: "service.unavailable", 500: "internal.error",
		504: "internal.error", 400: "request.invalid", 418: "request.invalid",
	}
	for status, want := range cases {
		if got, _ := Generic(status); got != want {
			t.Errorf("status %d: got %s want %s", status, got, want)
		}
	}
}

func TestMaskedErrorAndDiagnostic(t *testing.T) {
	e := ErrUpstream.WithCause(errors.New("dial tcp 10.0.0.1: refused")).WithDiagnostic("body: secret")
	if e.Error() != "dial tcp 10.0.0.1: refused" || e.Message != "upstream service unavailable" {
		t.Fatalf("masked flavor wrong: %q / %q", e.Error(), e.Message)
	}
	if Diagnostic(fmt.Errorf("x: %w", e)) != "body: secret" {
		t.Fatal("diagnostic must be found through the chain")
	}
	if Diagnostic(errors.New("plain")) != "" {
		t.Fatal("plain errors have no diagnostic")
	}
}

func TestDefineRegistersAndCodesSorted(t *testing.T) {
	d := Define("test_area.sample", "sample")
	if !Registered(d.Code) {
		t.Fatal("Define must register")
	}
	if m, ok := CanonicalMessage("file.in_use"); !ok || m != "file is in use" {
		t.Fatalf("spec code missing: %q %v", m, ok)
	}
	codes := Codes()
	for i := 1; i < len(codes); i++ {
		if codes[i-1] > codes[i] {
			t.Fatal("Codes must be sorted")
		}
	}
	if len(codes) < 70 {
		t.Fatalf("expected the full boundary table, got %d codes", len(codes))
	}
}
