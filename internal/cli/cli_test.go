package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pwn1609/AgentXRay/internal/model"
	"github.com/pwn1609/AgentXRay/internal/store"
)

// seedDB creates a temp database with two runs and returns its path.
func seedDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cli.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	start := time.UnixMilli(1_700_000_000_000)
	runs := []*model.Run{
		{
			ID: "aaaa1111bbbb2222", AgentName: "planner", Status: model.RunStatusSuccess,
			StartedAt: start, EndedAt: start.Add(2 * time.Second),
			LLMCalls: []model.LLMCall{{ID: "c1", RunID: "aaaa1111bbbb2222", Model: "gpt-4o",
				TokenBreakdown: model.TokenBreakdown{ToolDefinitionTokens: 800, OutputTokens: 100, Total: 900, Estimated: true}}},
		},
		{
			ID: "cccc3333dddd4444", AgentName: "researcher", Status: model.RunStatusError,
			StartedAt: start.Add(time.Minute), EndedAt: start.Add(time.Minute + time.Second),
			LLMCalls: []model.LLMCall{{ID: "c2", RunID: "cccc3333dddd4444", Model: "claude-3-5-sonnet",
				TokenBreakdown: model.TokenBreakdown{ConversationTokens: 400, OutputTokens: 50, Total: 450}}},
			ToolCalls: []model.ToolCall{{ID: "t1", RunID: "cccc3333dddd4444", ToolName: "web_search",
				Status: model.ToolStatusExecutionError, DurationMs: 900, FailureDetail: "timeout"}},
		},
	}
	for _, r := range runs {
		if err := st.SaveRun(context.Background(), r); err != nil {
			t.Fatalf("seed save: %v", err)
		}
	}
	return path
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestCLI_RunsList(t *testing.T) {
	db := seedDB(t)
	out, err := run(t, "runs", "list", "--db", db)
	if err != nil {
		t.Fatalf("runs list: %v", err)
	}
	for _, want := range []string{"planner", "researcher", "success", "error", "900", "450"} {
		if !strings.Contains(out, want) {
			t.Errorf("runs list output missing %q:\n%s", want, out)
		}
	}
	// researcher is more recent, should appear before planner.
	if strings.Index(out, "researcher") > strings.Index(out, "planner") {
		t.Errorf("runs not ordered most-recent-first:\n%s", out)
	}
}

func TestCLI_RunsShow_ByPrefix(t *testing.T) {
	db := seedDB(t)
	out, err := run(t, "runs", "show", "cccc3333", "--db", db)
	if err != nil {
		t.Fatalf("runs show: %v", err)
	}
	for _, want := range []string{"researcher", "TOKEN BREAKDOWN", "web_search", "execution_error", "timeout"} {
		if !strings.Contains(out, want) {
			t.Errorf("runs show output missing %q:\n%s", want, out)
		}
	}
}

func TestCLI_RunsShow_NotFound(t *testing.T) {
	db := seedDB(t)
	_, err := run(t, "runs", "show", "zzzz9999", "--db", db)
	if err == nil {
		t.Fatal("expected error for unknown run id")
	}
	if !strings.Contains(err.Error(), "no run found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCLI_RunsList_Empty(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.db")
	out, err := run(t, "runs", "list", "--db", empty)
	if err != nil {
		t.Fatalf("runs list: %v", err)
	}
	if !strings.Contains(out, "No runs yet") {
		t.Errorf("expected empty hint, got:\n%s", out)
	}
}

func TestCLI_ConfigValidate(t *testing.T) {
	// Valid: the checked-in example config.
	example := filepath.Join("..", "..", "config", "agentxray.example.yaml")
	out, err := run(t, "config", "validate", "--config", example)
	if err != nil {
		t.Fatalf("config validate (example): %v", err)
	}
	if !strings.Contains(out, "is valid") {
		t.Errorf("expected valid confirmation, got:\n%s", out)
	}

	// Invalid: grpc == http.
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("otlp:\n  grpc: \":4317\"\n  http: \":4317\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "config", "validate", "--config", bad); err == nil {
		t.Error("expected error for grpc==http config")
	}
}
