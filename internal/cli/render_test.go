package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/pwn1609/AgentXRay/internal/model"
)

func TestRenderRunsList_Empty(t *testing.T) {
	var buf bytes.Buffer
	renderRunsList(&buf, nil)
	if !strings.Contains(buf.String(), "No runs yet") {
		t.Errorf("expected empty hint, got %q", buf.String())
	}
}

func TestRenderRunShow(t *testing.T) {
	start := time.UnixMilli(1_700_000_000_000)
	run := &model.Run{
		ID:        "abcdef0123456789",
		AgentName: "demo",
		Status:    model.RunStatusError,
		StartedAt: start,
		EndedAt:   start.Add(1500 * time.Millisecond),
		TotalTokens: model.TokenBreakdown{
			SystemPromptTokens: 100, ToolDefinitionTokens: 600, ConversationTokens: 200,
			ToolOutputTokens: 50, OutputTokens: 50, Total: 1000, Estimated: true,
		},
		LLMCalls: []model.LLMCall{{Model: "gpt-4o", TokenBreakdown: model.TokenBreakdown{
			ToolDefinitionTokens: 600, Total: 1000, OutputTokens: 50}}},
		ToolCalls: []model.ToolCall{{
			ToolName: "search", Status: model.ToolStatusExecutionError,
			DurationMs: 42, FailureDetail: "boom"}},
	}
	var buf bytes.Buffer
	renderRunShow(&buf, run)
	out := buf.String()

	for _, want := range []string{
		"Run abcdef0123456789", "estimated", "TOKEN BREAKDOWN",
		"tool definitions", "60.0%", "LLM CALLS", "gpt-4o",
		"TOOL CALLS", "execution_error", "boom",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestBar(t *testing.T) {
	if got := bar(0); strings.Count(got, "█") != 0 {
		t.Errorf("bar(0) should have no filled cells: %q", got)
	}
	if got := bar(100); strings.Count(got, "█") != 20 {
		t.Errorf("bar(100) should be full: %q", got)
	}
	if got := bar(50); strings.Count(got, "█") != 10 {
		t.Errorf("bar(50) should be half: %q", got)
	}
}
