package channel

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// postWithin runs an admin request and fails the test when it does not return
// in time. A request that waits for the single SQLite connection while a
// transaction of its own holds it never returns.
func (s *suite) postWithin(d time.Duration, path string, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- s.env.Do("POST", path, s.adm(), body) }()
	select {
	case w := <-done:
		return w
	case <-time.After(d):
		s.t.Fatalf("POST %s did not answer within %s (self-deadlock on the database connection)", path, d)
		return nil
	}
}

// Creating a platform model through a binding on an upstream that already has a
// binding must answer, not hang: the transaction holds SQLite's only
// connection, so no read through the pool may happen inside it.
func TestBindingNewPlatformModelOnUpstreamWithBindingDoesNotHang(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("oa", "openai", nil)
	s.bind(up.ID, "first-model", "up-1", []string{ProtoOpenAIChat}, nil)
	path := fmt.Sprintf("%s/%d/models", upPath, up.ID)

	body := map[string]any{"platformModelName": "second-model", "upstreamModelName": "ba-mock-9",
		"protocols": []string{ProtoOpenAIChat}, "kindsJSON": `["chat"]`}
	// The breaker settings are cached for 5 seconds; once they expire the row
	// view reads them again.
	s.env.Clock.Advance(time.Minute)
	w := s.postWithin(10*time.Second, path, body)
	s.expect(w, 200, "")

	// A platform model name that does not exist yet, with the breaker enabled
	// (the row view reads the breaker settings and the circuit state).
	s.setBreaker(`{"enabled":true,"failureThreshold":3,"durationMin":1,"windowMin":1}`)
	body["platformModelName"], body["upstreamModelName"] = "third-model", "up-3"
	s.env.Clock.Advance(time.Minute)
	s.expect(s.postWithin(10*time.Second, path, body), 200, "")

	// Inferred kinds, no explicit kinds and no explicit protocols.
	s.env.Clock.Advance(time.Minute)
	body = map[string]any{"platformModelName": "gpt-image-9", "upstreamModelName": "img-9", "protocols": []string{}}
	s.expect(s.postWithin(10*time.Second, path, body), 200, "")

	var n int64
	s.env.DB.Model(&PlatformModel{}).Count(&n)
	if n != 4 {
		t.Fatalf("platform models: %d", n)
	}
}
