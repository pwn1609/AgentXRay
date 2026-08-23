package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pwn1609/AgentXRay/internal/ingest"
	"github.com/pwn1609/AgentXRay/internal/model"
	"github.com/pwn1609/AgentXRay/internal/store"
	"github.com/pwn1609/AgentXRay/internal/tokenize"
)

// TestReplayFixturesEndToEnd is the SPEC §12 go/no-go gate: replay real OTLP/JSON
// fixtures through the live receiver and assert sane runs land in SQLite.
func TestReplayFixturesEndToEnd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	counter, err := tokenize.NewCounter("")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}

	proc := ingest.NewProcessor(st, counter, true, nil)
	srv := ingest.NewServer("127.0.0.1:0", "127.0.0.1:0", proc, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Shutdown()

	_, httpAddr := srv.Addrs()
	endpoint := "http://" + httpAddr
	ctx := context.Background()

	for _, name := range []string{"openai_run.json", "anthropic_run.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		req, err := ingest.DecodeTracesJSON(data)
		if err != nil {
			t.Fatalf("decode fixture %s: %v", name, err)
		}
		if err := ingest.PostTraces(ctx, endpoint, req); err != nil {
			t.Fatalf("post fixture %s: %v", name, err)
		}
	}

	runs, err := st.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	byAgent := map[string]*model.Run{}
	for _, r := range runs {
		full, err := st.GetRun(ctx, r.ID)
		if err != nil {
			t.Fatalf("get run %s: %v", r.ID, err)
		}
		byAgent[full.AgentName] = full
	}

	// --- OpenAI multi-turn run ---
	oa := byAgent["trip-planner"]
	if oa == nil {
		t.Fatal("missing trip-planner run")
	}
	if oa.Status != model.RunStatusSuccess {
		t.Errorf("trip-planner status = %q, want success", oa.Status)
	}
	if len(oa.LLMCalls) != 2 {
		t.Fatalf("trip-planner LLM calls = %d, want 2", len(oa.LLMCalls))
	}
	if len(oa.ToolCalls) != 1 {
		t.Fatalf("trip-planner tool calls = %d, want 1", len(oa.ToolCalls))
	}
	if oa.ToolCalls[0].ToolName != "get_weather" || oa.ToolCalls[0].Status != model.ToolStatusSuccess {
		t.Errorf("trip-planner tool call wrong: %+v", oa.ToolCalls[0])
	}
	// Run total = sum of provider totals across both chats: 1200+150 + 1400+90.
	if oa.TotalTokens.Total != 2840 {
		t.Errorf("trip-planner total = %d, want 2840", oa.TotalTokens.Total)
	}
	if !oa.TotalTokens.Estimated {
		t.Error("trip-planner breakdown should be flagged estimated")
	}
	for i, c := range oa.LLMCalls {
		inputSum := c.TokenBreakdown.SystemPromptTokens + c.TokenBreakdown.ToolDefinitionTokens +
			c.TokenBreakdown.ConversationTokens + c.TokenBreakdown.ToolOutputTokens
		wantInput := c.TokenBreakdown.Total - c.TokenBreakdown.OutputTokens
		if inputSum != wantInput {
			t.Errorf("call %d: input categories sum %d != total-output %d", i, inputSum, wantInput)
		}
		if c.TokenBreakdown.ToolDefinitionTokens == 0 {
			t.Errorf("call %d: expected tool-definition tokens > 0", i)
		}
		if c.TokenBreakdown.SystemPromptTokens == 0 {
			t.Errorf("call %d: expected system-prompt tokens > 0", i)
		}
	}
	// The second chat feeds a tool result back, so it should have tool-output tokens.
	if oa.LLMCalls[1].TokenBreakdown.ToolOutputTokens == 0 {
		t.Error("second chat should carry tool-output tokens")
	}

	// --- Anthropic run with a failing tool ---
	an := byAgent["research-assistant"]
	if an == nil {
		t.Fatal("missing research-assistant run")
	}
	if an.Status != model.RunStatusError {
		t.Errorf("research-assistant status = %q, want error (tool failed)", an.Status)
	}
	if len(an.ToolCalls) != 1 {
		t.Fatalf("research-assistant tool calls = %d, want 1", len(an.ToolCalls))
	}
	tc := an.ToolCalls[0]
	if tc.Status != model.ToolStatusExecutionError {
		t.Errorf("web_search status = %q, want execution_error", tc.Status)
	}
	if tc.FailureDetail == "" {
		t.Error("expected a failure detail on the errored tool call")
	}
	if an.TotalTokens.Total != 930 { // 820 + 110
		t.Errorf("research-assistant total = %d, want 930", an.TotalTokens.Total)
	}
}
