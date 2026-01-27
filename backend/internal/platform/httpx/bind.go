package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// DefaultBodyLimit bounds JSON request bodies read by BindJSON.
const DefaultBodyLimit = 4 << 20

var (
	validatorOnce sync.Once
	validate      *validator.Validate
)

func validatorInstance() *validator.Validate {
	validatorOnce.Do(func() {
		validate = validator.New(validator.WithRequiredStructEnabled())
		validate.SetTagName("binding")
		// Name fields by their JSON key so errors point at what the client sent.
		validate.RegisterTagNameFunc(func(f reflect.StructField) string {
			name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
			if name == "-" {
				return ""
			}
			return name
		})
	})
	return validate
}

// FieldError describes one failed validation rule.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}

// BindJSON decodes the request body into dst and validates it using the
// `binding:"..."` struct tags. On failure it writes the 400 request.invalid_body
// envelope and returns false.
func BindJSON(c *gin.Context, dst any) bool { return BindJSONLimit(c, dst, DefaultBodyLimit) }

// BindJSONPlain is BindJSON for endpoints that have always answered a body that
// is not well-formed JSON with the plain "invalid request body" message and
// reason "invalid" (no syntax offset). Type and validation failures are unchanged.
func BindJSONPlain(c *gin.Context, dst any) bool {
	c.Set(plainSyntaxKey, true)
	return BindJSONLimit(c, dst, DefaultBodyLimit)
}

const plainSyntaxKey = "httpx.plain_syntax"

// BindJSONLimit is BindJSON with an explicit body size limit in bytes.
func BindJSONLimit(c *gin.Context, dst any, limit int64) bool {
	if c.Request.Body == nil {
		failBody(c, "request body is required", map[string]any{"reason": "empty_body"}, nil)
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			Fail(c, http.StatusRequestEntityTooLarge, apperr.ErrInvalidRequest.WithCause(err))
			return false
		}
		failBody(c, "invalid request body", map[string]any{"reason": "invalid"}, err)
		return false
	}
	return bindBytes(c, raw, dst)
}

func bindBytes(c *gin.Context, raw []byte, dst any) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		failBody(c, "request body is required", map[string]any{"reason": "empty_body"}, nil)
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		var syn *json.SyntaxError
		var typ *json.UnmarshalTypeError
		switch {
		case (errors.As(err, &syn) || errors.Is(err, io.ErrUnexpectedEOF)) && c.GetBool(plainSyntaxKey):
			failBody(c, "invalid request body", map[string]any{"reason": "invalid"}, err)
		case errors.As(err, &syn):
			failBody(c, fmt.Sprintf("invalid JSON body at offset %d", syn.Offset),
				map[string]any{"reason": "json_syntax", "offset": syn.Offset}, err)
		case errors.Is(err, io.ErrUnexpectedEOF):
			failBody(c, fmt.Sprintf("invalid JSON body at offset %d", len(raw)),
				map[string]any{"reason": "json_syntax", "offset": len(raw)}, err)
		case errors.As(err, &typ):
			field := typ.Field
			if field == "" {
				field = "field"
			}
			failBody(c, field+" has invalid type",
				map[string]any{"reason": "json_type", "field": field, "expected": typ.Type.String(), "value": typ.Value}, err)
		default:
			failBody(c, "invalid request body", map[string]any{"reason": "invalid"}, err)
		}
		return false
	}
	if errs := ValidateStruct(dst); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message
		}
		failBody(c, "invalid request body: "+strings.Join(msgs, "; "),
			map[string]any{"reason": "validation", "fieldErrors": errs}, nil)
		return false
	}
	return true
}

func failBody(c *gin.Context, message string, details map[string]any, cause error) {
	e := apperr.ErrInvalidBody.WithMessage(message).WithDetails(details)
	if cause != nil {
		e = e.WithCause(cause)
	}
	Fail(c, http.StatusBadRequest, e)
}

// ValidateStruct validates v with the `binding` tags and returns the field
// errors (nil when valid or when v is not a struct).
func ValidateStruct(v any) []FieldError {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	err := validatorInstance().Struct(rv.Interface())
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return []FieldError{{Field: "field", Rule: "invalid", Message: "invalid request body"}}
	}
	out := make([]FieldError, 0, len(verrs))
	anon := rv.Type().Name() == ""
	for _, fe := range verrs {
		ns := fe.Namespace()
		if anon {
			// An anonymous struct has no name segment to drop.
			ns = "_." + ns
		}
		name := FieldName(ns)
		out = append(out, FieldError{Field: name, Rule: fe.Tag(), Param: fe.Param(), Message: ruleMessage(name, fe.Tag(), fe.Param())})
	}
	return out
}

func ruleMessage(f, rule, param string) string {
	switch rule {
	case "required", "required_without", "required_with", "required_if":
		return f + " is required"
	case "min":
		return fmt.Sprintf("%s must be at least %s", f, param)
	case "max":
		return fmt.Sprintf("%s must be at most %s", f, param)
	case "len":
		return fmt.Sprintf("%s length must be %s", f, param)
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", f, strings.Join(strings.Fields(param), " "))
	case "email":
		return f + " must be a valid email address"
	case "url":
		return f + " must be a full URL, for example https://api.example.com"
	case "uri":
		return f + " must be a valid URI"
	case "gt":
		return fmt.Sprintf("%s must be greater than %s", f, param)
	case "gte":
		return fmt.Sprintf("%s must be greater than or equal to %s", f, param)
	case "lt":
		return fmt.Sprintf("%s must be less than %s", f, param)
	case "lte":
		return fmt.Sprintf("%s must be less than or equal to %s", f, param)
	default:
		return fmt.Sprintf("%s failed validation rule %s", f, rule)
	}
}

var initialisms = []string{"JSON", "SSO", "MCP", "API", "OTP", "URL", "URI", "USD", "CNY", "JWT", "ID"}

// FieldName renders a validator namespace ("Req.Items[0].APIKey") as a client
// facing path ("items[0].apiKey"): the top-level struct name is dropped, the
// first letter of each segment is lower-cased, and a leading initialism is
// lower-cased as a whole word. An empty result renders as "field".
func FieldName(namespace string) string {
	if i := strings.IndexByte(namespace, '.'); i >= 0 {
		namespace = namespace[i+1:]
	} else {
		namespace = ""
	}
	if namespace == "" {
		return "field"
	}
	segs := strings.Split(namespace, ".")
	for i, s := range segs {
		name, index := s, ""
		if j := strings.IndexByte(s, '['); j >= 0 {
			name, index = s[:j], s[j:]
		}
		segs[i] = lowerCamel(name) + index
	}
	return strings.Join(segs, ".")
}

func lowerCamel(s string) string {
	if s == "" {
		return ""
	}
	for _, ini := range initialisms {
		if !strings.HasPrefix(s, ini) {
			continue
		}
		rest := s[len(ini):]
		if rest == "" {
			return strings.ToLower(ini)
		}
		if r, _ := utf8.DecodeRuneInString(rest); unicode.IsUpper(r) || unicode.IsDigit(r) {
			return strings.ToLower(ini) + rest
		}
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}
