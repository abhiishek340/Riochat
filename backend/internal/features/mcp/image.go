package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Image analysis budgets.
const (
	maxAnalysisRunes       = 6000
	totalAnalysisRunes     = 16000
	maxProcessorImageBytes = 40 << 20
)

// ProcessorInfo describes the tool that analyzes uploaded images in a run.
type ProcessorInfo struct {
	ToolID      uint
	ServerID    uint
	Name        string
	DisplayName string
}

// Processor returns the run's image processor, if one is selected.
func (rt *RunTools) Processor() (ProcessorInfo, bool) {
	if rt.processor == nil {
		return ProcessorInfo{}, false
	}
	p := rt.processor
	return ProcessorInfo{ToolID: p.toolID, ServerID: p.serverID, Name: p.toolName, DisplayName: p.displayName}, true
}

// ImageInput is one prepared (already downscaled) image.
type ImageInput struct {
	FileName string
	MimeType string
	Data     []byte
}

// ImageAnalysis is the processor's answer for one image.
type ImageAnalysis struct {
	// Label names the tool and the file, for the injected context.
	Label string
	// Text is the excerpted analysis.
	Text    string
	Outcome CallOutcome
}

// Format renders the analysis as a block of context text.
func (a ImageAnalysis) Format() string { return "[" + a.Label + "]\n" + a.Text }

// excerpt cuts text to at most n runes.
func excerpt(text string, n int) string {
	if n <= 0 || utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n])
}

// resultText joins the text parts of a tools/call result.
func resultText(raw string) string {
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(raw), &res) != nil {
		return ""
	}
	var parts []string
	for _, c := range res.Content {
		if t := strings.TrimSpace(c.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// AnalyzeImages sends every image to the selected processor and returns the
// excerpted analyses. Every attempted call yields an outcome to record; the
// first failure of any kind stops the run with ErrImageProcessing. The images'
// bytes are never stored: the outcome input is an audit summary.
func (rt *RunTools) AnalyzeImages(ctx context.Context, images []ImageInput, userText string, meta CallRequest) ([]ImageAnalysis, error) {
	p := rt.processor
	if p == nil || len(images) == 0 {
		return nil, nil
	}
	if len(images) > rt.cfg.MaxToolCallsPerRun {
		return nil, ErrImageProcessing.WithCause(errors.New("too many images for one run"))
	}
	var total int64
	for _, im := range images {
		total += int64(len(im.Data))
	}
	if total > maxProcessorImageBytes {
		return nil, ErrImageProcessing.WithCause(errors.New("images are too large"))
	}
	perImage := min(maxAnalysisRunes, totalAnalysisRunes/len(images))
	label := strings.TrimSpace(p.displayName)
	if label == "" {
		label = p.toolName
	}
	prompt := strings.TrimSpace(userText)
	var out []ImageAnalysis
	for _, im := range images {
		encoded := base64.StdEncoding.EncodeToString(im.Data)
		if p.attach.Encoding == EncodingDataURL {
			encoded = "data:" + im.MimeType + ";base64," + encoded
		}
		args := map[string]any{p.attach.Argument: encoded}
		if p.attach.Prompt != "" {
			args[p.attach.Prompt] = prompt
		}
		argText, _ := json.Marshal(args)
		audit, _ := json.Marshal(map[string]any{
			"fileName": im.FileName, "mimeType": im.MimeType, "encoding": p.attach.Encoding, "bytes": len(im.Data),
		})
		base := CallOutcome{
			ToolType: ToolTypeAttachment, ModelName: p.modelName, ToolName: p.toolName,
			ServerID: p.serverID, ServerName: p.serverName, Input: string(audit),
		}
		item := ImageAnalysis{Label: label + " / " + im.FileName}
		normalized, err := prepareArguments(string(argText), p.schema)
		if err != nil {
			base.Status, base.Error = CallError, err.Error()
			item.Outcome = base
			out = append(out, item)
			return out, ErrImageProcessing.WithCause(err)
		}
		res := rt.execute(ctx, p, base, meta, normalized)
		res.Input = string(audit)
		if res.Status == CallSuccess {
			res.Output = SanitizeToolOutput(res.Output)
			text := resultText(res.Output)
			if text == "" {
				// The upstream call succeeded and stays metered.
				res.Status, res.Error, res.Output = CallError, "the image tool returned no text", ""
			} else {
				item.Text = excerpt(text, perImage)
			}
		}
		res.ModelOutput = ""
		item.Outcome = res
		out = append(out, item)
		if res.Status != CallSuccess {
			return out, ErrImageProcessing.WithCause(fmt.Errorf("image tool call failed: %s", res.Error))
		}
	}
	return out, nil
}
