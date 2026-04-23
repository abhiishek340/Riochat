package integration

import (
	"context"
	"strings"

	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
)

// messages converts the conversation history into provider messages. Images
// are attached only when the route's model accepts image input.
func (c *chatRun) messages(ctx context.Context) ([]llm.Message, error) {
	passback := c.req.PassbackReasoning && c.route.ReasoningPassback
	acceptsImages := modalityAllowed(c.route.InputModalities, "image")
	out := make([]llm.Message, 0, len(c.req.Messages))
	last := len(c.req.Messages) - 1
	for i, m := range c.req.Messages {
		role := llm.Role(m.Role)
		if role != llm.RoleAssistant && role != llm.RoleSystem {
			role = llm.RoleUser
		}
		msg := llm.Message{Role: role, Content: m.Content}
		if passback && role == llm.RoleAssistant {
			msg.Reasoning = m.Reasoning
		}
		if i == last && role == llm.RoleUser {
			// The per-question blocks (memories, retrieved passages) travel with
			// the current turn so the earlier history stays cacheable.
			var lead []string
			if c.prompt.memoryBlock != "" {
				lead = append(lead, c.prompt.memoryBlock)
			}
			if strings.TrimSpace(c.req.DynamicContext) != "" {
				lead = append(lead, c.req.DynamicContext)
			}
			if len(lead) > 0 {
				msg.Content = "<ctx>" + strings.Join(lead, "\n") + "</ctx>\n\n" + m.Content
			}
		}
		if len(m.Files) > 0 {
			// The attachments come first and the user's own words last, so
			// the question is what the model reads right before answering.
			for _, f := range m.Files {
				if f.Retrieved {
					continue
				}
				if part, ok := c.filePart(ctx, f, acceptsImages); ok {
					msg.Parts = append(msg.Parts, part)
				}
			}
			msg.Parts = append(msg.Parts, llm.Part{Kind: llm.PartText, Text: msg.Content})
		}
		out = append(out, msg)
	}
	return out, nil
}

func (c *chatRun) filePart(ctx context.Context, f conversation.PromptFile, acceptsImages bool) (llm.Part, bool) {
	if f.Kind == "image" {
		if !acceptsImages {
			return llm.Part{Kind: llm.PartText, Text: "[attached image omitted: " + f.FileName + "]"}, true
		}
		data := f.Data
		mime := f.MimeType
		if len(data) == 0 && c.g.h.files != nil && f.FileID != "" {
			stored, err := c.g.h.files.Open(ctx, c.req.UserID, f.FileID)
			if err != nil {
				return llm.Part{}, false
			}
			defer stored.Body.Close()
			buf := make([]byte, 0, stored.Size)
			tmp := make([]byte, 32*1024)
			for {
				n, rerr := stored.Body.Read(tmp)
				buf = append(buf, tmp[:n]...)
				if rerr != nil {
					break
				}
			}
			data = buf
			if mime == "" {
				mime = stored.MimeType
			}
		}
		if len(data) == 0 {
			return llm.Part{}, false
		}
		return llm.Part{Kind: llm.PartImage, MIME: mime, Data: data, FileName: f.FileName}, true
	}
	if strings.TrimSpace(f.Text) == "" {
		return llm.Part{Kind: llm.PartText, Text: "[attached file without readable text: " + f.FileName + "]"}, true
	}
	return llm.Part{Kind: llm.PartFile, Text: f.Text, FileName: f.FileName, MIME: f.MimeType}, true
}

// modalityAllowed treats an unknown modality list as "anything goes".
func modalityAllowed(list []string, want string) bool {
	if len(list) == 0 {
		return true
	}
	for _, m := range list {
		if strings.EqualFold(m, want) {
			return true
		}
	}
	return false
}

// options assembles the request options: the caller's own, the reasoning
// level translated for the route, the model controls the user switched, and
// the pass-back options some providers require for returned reasoning.
func (c *chatRun) options(ctx context.Context) map[string]any {
	opts := map[string]any{}
	mergeInto(opts, c.req.Options)
	if c.req.PassbackReasoning && c.route.ReasoningPassback {
		mergeInto(opts, c.route.PassbackOptions)
	}
	c.applyControls(ctx, opts)
	level := c.req.ReasoningEffort
	if v, ok := c.req.Controls["reasoning"].(string); ok && v != "" {
		level = v
	}
	if cap := c.route.Reasoning; cap != nil && level != "" && !strings.EqualFold(level, "auto") {
		mergeInto(opts, reasoningPatch(cap, channel.ResolveLevel(cap, level), c.route.Protocol))
	}
	return opts
}

// applyControls applies the patches of the model controls the user selected.
func (c *chatRun) applyControls(ctx context.Context, opts map[string]any) {
	if len(c.req.Controls) == 0 || c.g.h.channel == nil {
		return
	}
	models, err := c.g.h.channel.UserModels(ctx, c.req.UserID)
	if err != nil {
		return
	}
	for _, m := range models {
		if m.PlatformModelName != c.req.Model {
			continue
		}
		for _, ctl := range m.Controls {
			if ctl.Kind == "reasoning" {
				continue
			}
			raw, given := c.req.Controls[ctl.ID]
			if !given && !ctl.Locked {
				continue
			}
			value := ctl.NormalizeValue(raw)
			if value == "" {
				continue
			}
			if patch := ctl.Patch(value, c.route.Protocol); patch != nil {
				mergeInto(opts, patch)
			}
		}
		return
	}
}

// reasoningPatch expresses a reasoning level in the request shape of the
// capability's format.
func reasoningPatch(cap *channel.ReasoningCapability, level, protocol string) map[string]any {
	if level == "" {
		return nil
	}
	budget := cap.Budgets[level]
	switch cap.Format {
	case "anthropic_budget":
		if level == "none" {
			return map[string]any{"thinking": map[string]any{"type": "disabled"}}
		}
		return map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": budget}}
	case "gemini_budget":
		return nestedAt("generationConfig.thinkingConfig.thinkingBudget", budget)
	case "qwen":
		p := map[string]any{"enable_thinking": level != "none"}
		if budget > 0 && level != "none" {
			p["thinking_budget"] = budget
		}
		return p
	case "toggle":
		t := "enabled"
		if level == "none" {
			t = "disabled"
		}
		return map[string]any{"thinking": map[string]any{"type": t}}
	}
	path := cap.ControlPath
	if path == "" {
		switch cap.Format {
		case "anthropic_effort":
			path = "output_config.effort"
		case "gemini_level":
			if strings.Contains(protocol, "interactions") {
				path = "generation_config.thinking_level"
			} else {
				path = "generationConfig.thinkingConfig.thinkingLevel"
			}
		default:
			if strings.Contains(protocol, "responses") {
				path = "reasoning.effort"
			} else {
				path = "reasoning_effort"
			}
		}
	}
	return nestedAt(path, level)
}

func nestedAt(path string, v any) map[string]any {
	segs := strings.Split(path, ".")
	root := map[string]any{}
	cur := root
	for i, s := range segs {
		if i == len(segs)-1 {
			cur[s] = v
			break
		}
		next := map[string]any{}
		cur[s] = next
		cur = next
	}
	return root
}

// mergeInto deep-merges src into dst; scalars and arrays in src win.
func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeInto(dm, sm)
				continue
			}
			cp := map[string]any{}
			mergeInto(cp, sm)
			dst[k] = cp
			continue
		}
		dst[k] = v
	}
}
