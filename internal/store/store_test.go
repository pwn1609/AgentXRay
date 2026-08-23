package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pwn1609/AgentXRay/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleRun() *model.Run {
	start := time.UnixMilli(1_700_000_000_000).UTC()
	return &model.Run{
		ID:        "trace-abc",
		AgentName: "demo-agent",
		StartedAt: start,
		EndedAt:   start.Add(2 * time.Second),
		Status:    model.RunStatusSuccess,
		Metadata:  map[string]string{"env": "test", "version": "0.1"},
		LLMCalls: []model.LLMCall{
			{
				ID: "llm-1", RunID: "trace-abc", Model: "gpt-4o",
				StartedAt: start, DurationMs: 1200, FinishReason: "stop",
				TokenBreakdown: model.TokenBreakdown{
					SystemPromptTokens: 100, ToolDefinitionTokens: 250,
					ConversationTokens: 400, ToolOutputTokens: 50,
					OutputTokens: 80, Total: 880, Estimated: true,
				},
			},
			{
				ID: "llm-2", RunID: "trace-abc", Model: "gpt-4o",
				StartedAt: start.Add(1300 * time.Millisecond), DurationMs: 500, FinishReason: "stop",
				TokenBreakdown: model.TokenBreakdown{
					SystemPromptTokens: 100, ToolDefinitionTokens: 250,
					ConversationTokens: 500, ToolOutputTokens: 120,
					OutputTokens: 40, Total: 1010,
				},
			},
		},
		ToolCalls: []model.ToolCall{
			{
				ID: "tool-1", RunID: "trace-abc", LLMCallID: "llm-1", ToolName: "search",
				Arguments: json.RawMessage(`{"q":"weather"}`), Result: json.RawMessage(`{"temp":72}`),
				StartedAt: start.Add(1250 * time.Millisecond), DurationMs: 40,
				Status: model.ToolStatusSuccess,
			},
			{
				ID: "tool-2", RunID: "trace-abc", LLMCallID: "llm-2", ToolName: "http_get",
				Arguments: json.RawMessage(`{"url":"x"}`),
				StartedAt: start.Add(1800 * time.Millisecond), DurationMs: 10,
				Status: model.ToolStatusExecutionError, FailureDetail: "connection refused",
			},
		},
	}
}

func TestSaveAndGetRun_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	in := sampleRun()
	// TotalTokens is set here but GetRun recomputes it from the LLM calls.
	in.TotalTokens = model.TokenBreakdown{Total: 1890}

	if err := s.SaveRun(ctx, in); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, err := s.GetRun(ctx, "trace-abc")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetRun returned nil for existing run")
	}

	if got.AgentName != "demo-agent" || got.Status != model.RunStatusSuccess {
		t.Errorf("run fields wrong: %+v", got)
	}
	if !got.StartedAt.Equal(in.StartedAt) {
		t.Errorf("StartedAt: got %v want %v", got.StartedAt, in.StartedAt)
	}
	if got.Metadata["env"] != "test" || got.Metadata["version"] != "0.1" {
		t.Errorf("metadata round-trip failed: %v", got.Metadata)
	}
	if len(got.LLMCalls) != 2 {
		t.Fatalf("want 2 llm calls, got %d", len(got.LLMCalls))
	}
	if len(got.ToolCalls) != 2 {
		t.Fatalf("want 2 tool calls, got %d", len(got.ToolCalls))
	}

	// Reconstructed run-level total = sum of both LLM breakdowns.
	want := model.TokenBreakdown{
		SystemPromptTokens: 200, ToolDefinitionTokens: 500,
		ConversationTokens: 900, ToolOutputTokens: 170,
		OutputTokens: 120, Total: 1890, Estimated: true, // sticky from llm-1
	}
	if got.TotalTokens != want {
		t.Errorf("TotalTokens:\n got %+v\nwant %+v", got.TotalTokens, want)
	}

	// Spot-check a tool call's fields and JSON payloads.
	var tool2 *model.ToolCall
	for i := range got.ToolCalls {
		if got.ToolCalls[i].ID == "tool-2" {
			tool2 = &got.ToolCalls[i]
		}
	}
	if tool2 == nil {
		t.Fatal("tool-2 missing")
	}
	if tool2.Status != model.ToolStatusExecutionError || tool2.FailureDetail != "connection refused" {
		t.Errorf("tool-2 status/detail wrong: %+v", tool2)
	}
	if string(got.ToolCalls[0].Arguments) != `{"q":"weather"}` {
		t.Errorf("arguments round-trip: %s", got.ToolCalls[0].Arguments)
	}
}

func TestGetRun_NotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetRun(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing run, got %+v", got)
	}
}

func TestSaveRun_Upsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	r := sampleRun()
	if err := s.SaveRun(ctx, r); err != nil {
		t.Fatalf("first SaveRun: %v", err)
	}
	// Mutate and re-save; should update in place, not duplicate.
	r.Status = model.RunStatusError
	if err := s.SaveRun(ctx, r); err != nil {
		t.Fatalf("second SaveRun: %v", err)
	}
	runs, err := s.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("upsert produced %d runs, want 1", len(runs))
	}
	if runs[0].Status != model.RunStatusError {
		t.Errorf("status not updated: %s", runs[0].Status)
	}
}

func TestListRuns_OrderAndLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.UnixMilli(1_700_000_000_000).UTC()
	for i := 0; i < 3; i++ {
		r := &model.Run{
			ID:        "run-" + string(rune('a'+i)),
			AgentName: "a",
			StartedAt: base.Add(time.Duration(i) * time.Minute),
			Status:    model.RunStatusSuccess,
		}
		if err := s.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}
	runs, err := s.ListRuns(ctx, 2)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("limit not applied: got %d", len(runs))
	}
	// Most recent first => run-c then run-b.
	if runs[0].ID != "run-c" || runs[1].ID != "run-b" {
		t.Errorf("wrong order: %s, %s", runs[0].ID, runs[1].ID)
	}
}
