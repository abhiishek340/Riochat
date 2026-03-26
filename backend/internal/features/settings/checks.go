package settings

import (
	"encoding/json"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// check validates one trimmed value. It returns nil when the value is valid.
type check func(v string) *violation

func bad(rule, param string) *violation { return &violation{rule: rule, param: param} }

// allOf runs the checks in order and returns the first failure.
func allOf(cs ...check) check {
	return func(v string) *violation {
		for _, c := range cs {
			if c == nil {
				continue
			}
			if f := c(v); f != nil {
				return f
			}
		}
		return nil
	}
}

func isNonEmpty() check {
	return func(v string) *violation {
		if v == "" {
			return bad("required", "")
		}
		return nil
	}
}

func isBool() check {
	return func(v string) *violation {
		if _, err := strconv.ParseBool(v); err != nil {
			return bad("bool", "")
		}
		return nil
	}
}

func parseInt(v string) (int64, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

func isInt() check {
	return func(v string) *violation {
		if _, ok := parseInt(v); !ok {
			return bad("integer", "")
		}
		return nil
	}
}

func pair(a, b int64) string { return strconv.FormatInt(a, 10) + "," + strconv.FormatInt(b, 10) }

func intRange(min, max int64) check {
	return func(v string) *violation {
		n, ok := parseInt(v)
		if !ok {
			return bad("integer", "")
		}
		if n < min || n > max {
			return bad("integer_range", pair(min, max))
		}
		return nil
	}
}

func intMin(min int64) check {
	return func(v string) *violation {
		n, ok := parseInt(v)
		if !ok {
			return bad("integer", "")
		}
		if n < min {
			return bad("integer_min", strconv.FormatInt(min, 10))
		}
		return nil
	}
}

// optIntRange accepts an empty value, zero, or an integer within [min, max].
func optIntRange(min, max int64) check {
	return func(v string) *violation {
		if v == "" {
			return nil
		}
		n, ok := parseInt(v)
		if !ok || (n != 0 && (n < min || n > max)) {
			return bad("optional_integer_range", pair(min, max))
		}
		return nil
	}
}

// optIntMin accepts an empty value or an integer of at least min.
func optIntMin(min int64) check {
	return func(v string) *violation {
		if v == "" {
			return nil
		}
		n, ok := parseInt(v)
		if !ok || n < min {
			return bad("optional_integer_min", strconv.FormatInt(min, 10))
		}
		return nil
	}
}

func floatRange(min, max float64) check {
	param := strconv.FormatFloat(min, 'f', -1, 64) + "," + strconv.FormatFloat(max, 'f', -1, 64)
	return func(v string) *violation {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < min || f > max {
			return bad("float_range", param)
		}
		return nil
	}
}

func maxChars(n int) check {
	return func(v string) *violation {
		if utf8.RuneCountInString(v) > n {
			return bad("max_length", strconv.Itoa(n))
		}
		return nil
	}
}

func maxBytes(n int) check {
	return func(v string) *violation {
		if len(v) > n {
			return bad("max_length", strconv.Itoa(n))
		}
		return nil
	}
}

func oneOf(values ...string) check {
	param := strings.Join(values, ",")
	return func(v string) *violation {
		for _, x := range values {
			if v == x {
				return nil
			}
		}
		return bad("enum", param)
	}
}

// parseHTTPURL parses an absolute http or https URL with a hostname.
func parseHTTPURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, false
	}
	return u, true
}

// optHTTPURL accepts an empty value or an absolute http(s) URL.
func optHTTPURL() check {
	return func(v string) *violation {
		if v == "" {
			return nil
		}
		if _, ok := parseHTTPURL(v); !ok {
			return bad("http_url", "")
		}
		return nil
	}
}

// trustedHTTPURL accepts an empty value or an absolute http(s) URL without
// embedded credentials. Private and internal origins are allowed because the
// administrator chooses them; user-supplied addresses use a stricter policy
// elsewhere.
func trustedHTTPURL() check {
	return func(v string) *violation {
		if v == "" {
			return nil
		}
		u, ok := parseHTTPURL(v)
		if !ok || u.User != nil {
			return bad("trusted_http_url", "")
		}
		return nil
	}
}

// ValidDesktopDownloadURL reports whether raw is a usable desktop download
// target: at most 200 bytes, an absolute http(s) URL with a hostname and no
// embedded credentials.
func ValidDesktopDownloadURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 200 {
		return false
	}
	u, ok := parseHTTPURL(raw)
	return ok && u.User == nil
}

func downloadURLCheck() check {
	return func(v string) *violation {
		if v == "" {
			return bad("required", "")
		}
		if len(v) > 200 {
			return bad("max_length", "200")
		}
		if !ValidDesktopDownloadURL(v) {
			return bad("download_url", "")
		}
		return nil
	}
}

// loginPathCheck validates the default post-login path: a local path.
func loginPathCheck() check {
	return func(v string) *violation {
		if v == "" {
			return bad("required", "")
		}
		if utf8.RuneCountInString(v) > 120 {
			return bad("max_length", "120")
		}
		if !validLoginPath(v) {
			return bad("local_path", "")
		}
		return nil
	}
}

func validLoginPath(v string) bool {
	return strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//")
}

// splitWords splits on commas and any whitespace, dropping empty items.
func splitWords(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

func domainListCheck() check {
	return func(v string) *violation {
		for _, entry := range splitWords(v) {
			e := strings.TrimPrefix(strings.ToLower(entry), "@")
			if !strings.Contains(e, ".") || strings.Contains(e, "@") || strings.Contains(e, "://") {
				return bad("domain", "")
			}
		}
		return nil
	}
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			default:
				return false
			}
		}
	}
	return true
}

func hostListCheck() check {
	return func(v string) *violation {
		for _, entry := range splitWords(v) {
			host := strings.TrimPrefix(entry, "*.")
			// An IP literal (v4 or v6, without a port) is a valid blocked host.
			if net.ParseIP(host) != nil {
				continue
			}
			if !validHostname(host) {
				return bad("domain", "")
			}
		}
		return nil
	}
}

func mimeListCheck() check {
	return func(v string) *violation {
		if v == "" {
			return bad("required", "")
		}
		for _, item := range strings.Split(v, ",") {
			if !strings.Contains(strings.TrimSpace(item), "/") {
				return bad("mime", "")
			}
		}
		return nil
	}
}

var minerUFileTypes = map[string]bool{"pdf": true, "word": true, "presentation": true, "excel": true}

func fileTypeListCheck() check {
	return func(v string) *violation {
		for _, item := range strings.Split(v, ",") {
			t := strings.ToLower(strings.TrimSpace(item))
			if t == "" {
				continue
			}
			if !minerUFileTypes[t] {
				return bad("file_type", "")
			}
		}
		return nil
	}
}

// parseProviders splits a payment provider list. Names are lower-cased and
// trimmed; blanks, "disabled" and duplicates are ignored. ok is false when an
// unknown provider appears.
func parseProviders(v string) (list []string, ok bool) {
	ok = true
	seen := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		switch p {
		case "", "disabled":
		case "stripe", "epay":
			if !seen[p] {
				seen[p] = true
				list = append(list, p)
			}
		default:
			ok = false
		}
	}
	return list, ok
}

func providerListCheck() check {
	return func(v string) *violation {
		if _, ok := parseProviders(v); !ok {
			return bad("payment_provider", "stripe,epay")
		}
		return nil
	}
}

// epayGatewayCheck accepts an empty value or an absolute http(s) URL with a
// hostname, without credentials, query or fragment.
func epayGatewayCheck() check {
	return func(v string) *violation {
		if v == "" {
			return nil
		}
		if !validEpayGateway(v) {
			return bad("epay_url", "")
		}
		return nil
	}
}

func validEpayGateway(v string) bool {
	u, ok := parseHTTPURL(strings.TrimSpace(v))
	return ok && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func validPaymentTypeCode(t string) bool {
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return t != ""
}

// epayTypesCheck validates the list of EPay payment methods.
func epayTypesCheck() check {
	return func(v string) *violation {
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(v), &raw); err != nil || raw == nil {
			return bad("json_array", "")
		}
		if len(raw) < 1 || len(raw) > 10 {
			return bad("payment_count", "1,10")
		}
		seen := map[string]bool{}
		for _, item := range raw {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(item, &obj); err != nil || obj == nil {
				return bad("payment_fields", "")
			}
			var name, typ string
			if json.Unmarshal(obj["name"], &name) != nil || json.Unmarshal(obj["type"], &typ) != nil {
				return bad("payment_fields", "")
			}
			name, typ = strings.TrimSpace(name), strings.TrimSpace(typ)
			if name == "" || typ == "" {
				return bad("payment_fields", "")
			}
			if len(name) > 64 {
				return bad("payment_item_length", "name,64")
			}
			if len(typ) > 32 {
				return bad("payment_item_length", "type,32")
			}
			if !validPaymentTypeCode(typ) {
				return bad("payment_type_chars", "")
			}
			if seen[typ] {
				return bad("payment_type_unique", "")
			}
			seen[typ] = true
		}
		return nil
	}
}

func triggerPercentCheck() check {
	return func(v string) *violation {
		if v == "" {
			return nil
		}
		n, ok := parseInt(v)
		if !ok {
			return bad("integer", "")
		}
		if n != 0 && (n < 10 || n > 95) {
			return bad("integer_range", "10,95")
		}
		return nil
	}
}
