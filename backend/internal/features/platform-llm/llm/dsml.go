package llm

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Some DeepSeek deployments emit tool calls as marked-up text instead of
// structured tool_calls. dsmlFilter withholds that text while it might still
// become a tool-call block and converts completed blocks to ToolCalls.

var dsmlMarkers = []string{"\uff5c", "|", "||", "\uff5c\uff5c"}
var dsmlBlockNames = []string{"tool_calls", "function_calls"}

const dsmlMarkerPattern = `(?:\x{ff5c}\x{ff5c}|\|\||\x{ff5c}|\|)`

var (
	dsmlOpenRe   = regexp.MustCompile(`<` + dsmlMarkerPattern + `DSML` + dsmlMarkerPattern + `(?:tool_calls|function_calls)>`)
	dsmlCloseRe  = regexp.MustCompile(`</` + dsmlMarkerPattern + `DSML` + dsmlMarkerPattern + `(?:tool_calls|function_calls)>`)
	dsmlInvokeRe = regexp.MustCompile(`(?s)<` + dsmlMarkerPattern + `DSML` + dsmlMarkerPattern + `invoke\s+([^>]*)>(.*?)</` + dsmlMarkerPattern + `DSML` + dsmlMarkerPattern + `invoke\s*>`)
	dsmlParamRe  = regexp.MustCompile(`(?s)<` + dsmlMarkerPattern + `DSML` + dsmlMarkerPattern + `parameter\s+([^>]*)>(.*?)</` + dsmlMarkerPattern + `DSML` + dsmlMarkerPattern + `parameter\s*>`)
	dsmlAttrRe   = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"`)
)

var dsmlOpeners = func() []string {
	var out []string
	for _, m := range dsmlMarkers {
		for _, n := range dsmlBlockNames {
			out = append(out, "<"+m+"DSML"+m+n+">")
		}
	}
	return out
}()

type dsmlFilter struct {
	pending string
	inBlock bool
	calls   []ToolCall
}

func isDeepSeek(x *exchange) bool {
	return strings.Contains(strings.ToLower(x.model), "deepseek") || strings.Contains(strings.ToLower(x.route.BaseURL), "deepseek")
}

// longestPrefixSuffix returns the length of the longest suffix of s that is a
// proper prefix of an opener.
func longestPrefixSuffix(s string) int {
	best := 0
	for _, op := range dsmlOpeners {
		max := len(op) - 1
		if max > len(s) {
			max = len(s)
		}
		for n := max; n > best; n-- {
			if strings.HasSuffix(s, op[:n]) {
				best = n
				break
			}
		}
	}
	return best
}

// push adds streamed text and returns the part that is safe to show.
func (f *dsmlFilter) push(s string) string {
	f.pending += s
	var out strings.Builder
	for {
		if f.inBlock {
			loc := dsmlCloseRe.FindStringIndex(f.pending)
			if loc == nil {
				return out.String()
			}
			f.parseBlock(f.pending[:loc[1]])
			f.pending = f.pending[loc[1]:]
			f.inBlock = false
			continue
		}
		loc := dsmlOpenRe.FindStringIndex(f.pending)
		if loc != nil {
			out.WriteString(f.pending[:loc[0]])
			f.pending = f.pending[loc[0]:]
			f.inBlock = true
			continue
		}
		hold := longestPrefixSuffix(f.pending)
		out.WriteString(f.pending[:len(f.pending)-hold])
		f.pending = f.pending[len(f.pending)-hold:]
		return out.String()
	}
}

var errDSMLIncomplete = fmt.Errorf("the model's tool-call block ended before it was complete")

// finish flushes remaining text. A block left open is an error.
func (f *dsmlFilter) finish() (string, error) {
	if f.inBlock {
		return "", &Error{Kind: KindParse, Message: errDSMLIncomplete.Error(), Cause: errDSMLIncomplete, Accepted: true}
	}
	rest := f.pending
	f.pending = ""
	return rest, nil
}

func (f *dsmlFilter) parseBlock(block string) {
	for _, im := range dsmlInvokeRe.FindAllStringSubmatch(block, -1) {
		attrs := parseAttrs(im[1])
		name := attrs["name"]
		if name == "" {
			continue
		}
		args := map[string]any{}
		void := false
		for _, pm := range dsmlParamRe.FindAllStringSubmatch(im[2], -1) {
			pa := parseAttrs(pm[1])
			pn := pa["name"]
			if pn == "" {
				continue
			}
			if _, dup := args[pn]; dup {
				void = true
				break
			}
			raw := html.UnescapeString(pm[2])
			if pa["string"] == "true" {
				args[pn] = raw
				continue
			}
			var v any
			if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &v); err == nil {
				args[pn] = v
			} else {
				args[pn] = raw
			}
		}
		if void {
			continue
		}
		b, _ := json.Marshal(args)
		f.calls = append(f.calls, ToolCall{ID: fmt.Sprintf("dsml_call_%d", len(f.calls)+1), Name: name, Arguments: string(b)})
	}
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range dsmlAttrRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// extractDSML converts a complete text in one pass.
func extractDSML(text string) (string, []ToolCall, error) {
	var f dsmlFilter
	visible := f.push(text)
	rest, err := f.finish()
	if err != nil {
		return "", nil, err
	}
	return visible + rest, f.calls, nil
}
