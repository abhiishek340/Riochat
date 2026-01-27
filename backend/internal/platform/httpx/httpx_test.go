package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

func init() { gin.SetMode(gin.TestMode) }

type body struct {
	Name  string `json:"name" binding:"required"`
	Count int    `json:"count" binding:"min=3"`
}

func serve(t *testing.T, h gin.HandlerFunc, method, target, payload string) (*httptest.ResponseRecorder, Envelope) {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		st := reqctx.NewState("req-xyz", "trace")
		c.Request = c.Request.WithContext(reqctx.Attach(c.Request.Context(), st))
		c.Next()
	})
	r.Handle(method, "/t", h)
	req := httptest.NewRequest(method, target, strings.NewReader(payload))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env Envelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w, env
}

func details(env Envelope) map[string]any {
	m, _ := env.Details.(map[string]any)
	return m
}

func TestOKAndPaged(t *testing.T) {
	w, env := serve(t, func(c *gin.Context) { OK(c, map[string]int{"a": 1}) }, "GET", "/t", "")
	if w.Code != 200 || env.ErrorMsg != "" || env.ErrorCode != "" || env.RequestID != "" {
		t.Fatalf("ok: %d %+v", w.Code, env)
	}
	if !strings.Contains(w.Body.String(), `"errorMsg":""`) {
		t.Fatalf("errorMsg must always be present: %s", w.Body)
	}

	w, _ = serve(t, func(c *gin.Context) { Paged[string](c, 0, nil) }, "GET", "/t", "")
	if !strings.Contains(w.Body.String(), `"results":[]`) || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("paged nil must render []: %s", w.Body)
	}
}

func TestFailEnvelopeHidesRawErrors(t *testing.T) {
	w, env := serve(t, func(c *gin.Context) {
		Fail(c, 500, errors.New("dial tcp: password=hunter2"))
	}, "GET", "/t", "")
	if w.Code != 500 || env.ErrorCode != "internal.error" || env.ErrorMsg != "internal server error" || env.RequestID != "req-xyz" {
		t.Fatalf("fail: %d %+v", w.Code, env)
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Fatal("raw error text leaked")
	}
	if !strings.Contains(w.Body.String(), `"data":null`) {
		t.Fatalf("failure data must be null: %s", w.Body)
	}
}

func TestFailRecordsCauseOnState(t *testing.T) {
	var state *reqctx.State
	serve(t, func(c *gin.Context) {
		state = reqctx.FromRequest(c.Request)
		Fail(c, 502, apperr.ErrUpstream.WithCause(errors.New("upstream said no")).WithDiagnostic("raw body"))
	}, "GET", "/t", "")
	code, cause, diag := state.Failure()
	if code != "upstream.unavailable" || cause == nil || cause.Error() != "upstream said no" || diag != "raw body" {
		t.Fatalf("state: %q %v %q", code, cause, diag)
	}
}

func TestFailErrUsesStatusHint(t *testing.T) {
	w, env := serve(t, func(c *gin.Context) { FailErr(c, apperr.ErrNotFound.WithStatus(404)) }, "GET", "/t", "")
	if w.Code != 404 || env.ErrorCode != "resource.not_found" {
		t.Fatalf("%d %+v", w.Code, env)
	}
	w, _ = serve(t, func(c *gin.Context) { FailErr(c, errors.New("x")) }, "GET", "/t", "")
	if w.Code != 500 {
		t.Fatalf("untyped defaults to 500, got %d", w.Code)
	}
}

func TestInvalidQueryDoesNotEchoInput(t *testing.T) {
	w, env := serve(t, func(c *gin.Context) { InvalidQuery(c, "status filter") }, "GET", "/t?status=<script>", "")
	if w.Code != 400 || env.ErrorCode != "request.invalid_query" || env.ErrorMsg != "invalid status filter" {
		t.Fatalf("%d %+v", w.Code, env)
	}
}

func bindHandler(c *gin.Context) {
	var b body
	if BindJSON(c, &b) {
		OK(c, b)
	}
}

func TestBindJSONEmptyBody(t *testing.T) {
	w, env := serve(t, bindHandler, "POST", "/t", "  ")
	if w.Code != 400 || env.ErrorCode != "request.invalid_body" || env.ErrorMsg != "request body is required" || details(env)["reason"] != "empty_body" {
		t.Fatalf("%d %+v", w.Code, env)
	}
}

func TestBindJSONSyntaxAndType(t *testing.T) {
	_, env := serve(t, bindHandler, "POST", "/t", `{"name": }`)
	d := details(env)
	if d["reason"] != "json_syntax" || !strings.HasPrefix(env.ErrorMsg, "invalid JSON body at offset ") || d["offset"] == nil {
		t.Fatalf("syntax: %+v", env)
	}
	_, env = serve(t, bindHandler, "POST", "/t", `{"name": 5}`)
	d = details(env)
	if env.ErrorMsg != "name has invalid type" || d["reason"] != "json_type" || d["field"] != "name" || d["expected"] != "string" || d["value"] != "number" {
		t.Fatalf("type: %+v", env)
	}
	_, env = serve(t, bindHandler, "POST", "/t", `{"name": "x"`)
	if details(env)["reason"] != "json_syntax" {
		t.Fatalf("truncated: %+v", env)
	}
}

func TestBindJSONValidation(t *testing.T) {
	type two struct {
		A string `json:"a" binding:"required"`
		B string `json:"b" binding:"min=3"`
	}
	h := func(c *gin.Context) {
		var v two
		if BindJSON(c, &v) {
			OK(c, v)
		}
	}
	w, env := serve(t, h, "POST", "/t", `{"b":"x"}`)
	if w.Code != 400 || env.ErrorMsg != "invalid request body: a is required; b must be at least 3" {
		t.Fatalf("%d %q", w.Code, env.ErrorMsg)
	}
	d := details(env)
	fe, _ := d["fieldErrors"].([]any)
	if d["reason"] != "validation" || len(fe) != 2 {
		t.Fatalf("details: %+v", d)
	}
	first := fe[0].(map[string]any)
	if first["field"] != "a" || first["rule"] != "required" {
		t.Fatalf("first field error: %+v", first)
	}
	if w, _ := serve(t, h, "POST", "/t", `{"a":"ok","b":"long"}`); w.Code != 200 {
		t.Fatalf("valid body rejected: %d", w.Code)
	}
}

func TestRuleMessages(t *testing.T) {
	cases := map[[2]string]string{
		{"oneof", "a b"}:          "f must be one of: a b",
		{"email", ""}:             "f must be a valid email address",
		{"url", ""}:               "f must be a full URL, for example https://api.example.com",
		{"uri", ""}:               "f must be a valid URI",
		{"len", "4"}:              "f length must be 4",
		{"max", "9"}:              "f must be at most 9",
		{"gt", "1"}:               "f must be greater than 1",
		{"gte", "1"}:              "f must be greater than or equal to 1",
		{"lt", "1"}:               "f must be less than 1",
		{"lte", "1"}:              "f must be less than or equal to 1",
		{"alphanum", ""}:          "f failed validation rule alphanum",
		{"required_without", "x"}: "f is required",
	}
	for k, want := range cases {
		if got := ruleMessage("f", k[0], k[1]); got != want {
			t.Errorf("%v: %q want %q", k, got, want)
		}
	}
}

func TestFieldName(t *testing.T) {
	cases := map[string]string{
		"Req.APIKey":             "apiKey",
		"Req.UserID":             "userID",
		"Req.IDToken":            "idToken",
		"Req.Name":               "name",
		"Req.Items[2].BaseURL":   "items[2].baseURL",
		"Req.Provider.JWTSecret": "provider.jwtSecret",
		"Req.URLPath":            "urlPath",
		"Req.ID":                 "id",
		"Req":                    "field",
		"":                       "field",
	}
	for in, want := range cases {
		if got := FieldName(in); got != want {
			t.Errorf("FieldName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBodyTooLarge(t *testing.T) {
	h := func(c *gin.Context) {
		var b body
		if BindJSONLimit(c, &b, 8) {
			OK(c, b)
		}
	}
	w, env := serve(t, h, "POST", "/t", `{"name":"this is far too long"}`)
	if w.Code != http.StatusRequestEntityTooLarge || env.ErrorCode != "request.invalid" {
		t.Fatalf("%d %+v", w.Code, env)
	}
}

func TestBuildFailureForStreams(t *testing.T) {
	env := BuildFailure(429, nil, "rid")
	if env.ErrorCode != "rate_limit.exceeded" || env.RequestID != "rid" || env.Data != nil {
		t.Fatalf("%+v", env)
	}
}

func TestValidateStructAnonymousNamesFields(t *testing.T) {
	var req struct {
		Type  string `binding:"required"`
		Items []struct {
			APIKey string `binding:"required"`
		} `binding:"dive"`
	}
	req.Items = make([]struct {
		APIKey string `binding:"required"`
	}, 1)
	errs := ValidateStruct(req)
	if len(errs) != 2 || errs[0].Field != "type" || errs[1].Field != "items[0].apiKey" {
		t.Fatalf("field names: %+v", errs)
	}
}
