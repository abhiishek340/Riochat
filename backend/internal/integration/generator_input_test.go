package integration

import (
	"context"
	"testing"

	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
)

// The user's own text is the last part of an attachment-bearing turn, so a
// model (or an echoing test model) answers the question, not the file.
func TestMessagesPutQuestionAfterAttachments(t *testing.T) {
	c := &chatRun{route: &channel.Route{}, req: conversation.GenerateRequest{Messages: []conversation.PromptMessage{{
		Role: "user", Content: "what is the code word?",
		Files: []conversation.PromptFile{
			{FileID: "f1", FileName: "a.txt", Kind: "document", MimeType: "text/plain", Text: "the code word is pelican"},
			{FileID: "f2", FileName: "b.txt", Kind: "document", MimeType: "text/plain", Text: "second file"},
		},
	}}}}
	msgs, err := c.messages(context.Background())
	if err != nil || len(msgs) != 1 {
		t.Fatalf("%v %+v", err, msgs)
	}
	p := msgs[0].Parts
	if len(p) != 3 || p[0].Kind != llm.PartFile || p[1].Kind != llm.PartFile || p[2].Kind != llm.PartText || p[2].Text != "what is the code word?" {
		t.Fatalf("parts: %+v", p)
	}
}
