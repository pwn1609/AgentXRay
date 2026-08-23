package tokenize

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustCounter(t *testing.T) *Counter {
	t.Helper()
	c, err := NewCounter("")
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	return c
}

// openAIShaped mimics an OpenAI chat.completions request: system message,
// function-style tool schemas, alternating user/assistant turns, and a tool result.
func openAIShaped() Payload {
	weatherSchema, _ := json.Marshal(map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "get_weather",
			"description": "Get the current weather for a city",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}},
				"required":   []string{"city"},
			},
		},
	})
	return Payload{
		Model:  "gpt-4o",
		System: "You are a helpful assistant that answers concisely.",
		Tools:  []ToolDef{{Name: "get_weather", Raw: weatherSchema}},
		Messages: []Message{
			{Role: RoleUser, Content: "What's the weather in Paris and should I bring an umbrella today?"},
			{Role: RoleAssistant, Content: "Let me check the current conditions for Paris."},
			{Role: RoleTool, Content: `{"city":"Paris","temp_c":14,"condition":"light rain","precip_mm":2.1}`},
		},
		ProviderInputTokens:  500,
		ProviderOutputTokens: 60,
	}
}

// anthropicShaped mimics an Anthropic messages request: top-level system string,
// tool definitions with input_schema, and a tool_result content block.
func anthropicShaped() Payload {
	searchSchema, _ := json.Marshal(map[string]any{
		"name":        "web_search",
		"description": "Search the web for a query",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
			"required":   []string{"query"},
		},
	})
	return Payload{
		Model:  "claude-3-5-sonnet",
		System: "You are Claude, an AI assistant made by Anthropic. Be helpful and honest.",
		Tools:  []ToolDef{{Name: "web_search", Raw: searchSchema}},
		Messages: []Message{
			{Role: RoleUser, Content: "Find the latest news about renewable energy investments in 2026."},
			{Role: RoleAssistant, Content: "I'll search for recent renewable energy investment news."},
			{Role: RoleTool, Content: `{"results":[{"title":"Solar investment hits record high","url":"https://example.com/a"}]}`},
		},
		ProviderInputTokens:  800,
		ProviderOutputTokens: 120,
	}
}

func TestCategorize_ScalesToProviderTotal(t *testing.T) {
	c := mustCounter(t)
	for _, tc := range []struct {
		name string
		p    Payload
	}{
		{"openai", openAIShaped()},
		{"anthropic", anthropicShaped()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			br, diag := Categorize(tc.p, c)

			inputSum := br.SystemPromptTokens + br.ToolDefinitionTokens +
				br.ConversationTokens + br.ToolOutputTokens
			if inputSum != tc.p.ProviderInputTokens {
				t.Errorf("input categories sum to %d, want provider total %d",
					inputSum, tc.p.ProviderInputTokens)
			}
			if br.OutputTokens != tc.p.ProviderOutputTokens {
				t.Errorf("OutputTokens = %d, want %d", br.OutputTokens, tc.p.ProviderOutputTokens)
			}
			if br.Total != tc.p.ProviderInputTokens+tc.p.ProviderOutputTokens {
				t.Errorf("Total = %d, want %d", br.Total,
					tc.p.ProviderInputTokens+tc.p.ProviderOutputTokens)
			}
			if !br.Estimated {
				t.Error("expected Estimated=true for a provider-total split")
			}
			// Every category should be populated for these rich payloads.
			if br.SystemPromptTokens == 0 || br.ToolDefinitionTokens == 0 ||
				br.ConversationTokens == 0 || br.ToolOutputTokens == 0 {
				t.Errorf("expected all input categories > 0, got %+v", br)
			}
			_ = diag
		})
	}
}

func TestCategorize_NoProviderTotals_RawEstimate(t *testing.T) {
	c := mustCounter(t)
	p := openAIShaped()
	p.ProviderInputTokens = 0
	p.ProviderOutputTokens = 0
	p.ResponseText = "Yes, bring an umbrella — light rain is expected in Paris today."

	br, _ := Categorize(p, c)

	if !br.Estimated {
		t.Error("expected Estimated=true without provider totals")
	}
	if br.OutputTokens == 0 {
		t.Error("expected output tokens estimated from ResponseText")
	}
	inputSum := br.SystemPromptTokens + br.ToolDefinitionTokens +
		br.ConversationTokens + br.ToolOutputTokens
	if inputSum == 0 {
		t.Error("expected raw tokenized input > 0")
	}
	if br.Total != inputSum+br.OutputTokens {
		t.Errorf("Total = %d, want %d", br.Total, inputSum+br.OutputTokens)
	}
}

func TestCategorize_NoContentButProviderTotal(t *testing.T) {
	c := mustCounter(t)
	p := Payload{
		Model:                "gpt-4o",
		ProviderInputTokens:  1000,
		ProviderOutputTokens: 200,
	}
	br, diag := Categorize(p, c)

	if br.SystemPromptTokens != 0 || br.ToolDefinitionTokens != 0 ||
		br.ConversationTokens != 0 || br.ToolOutputTokens != 0 {
		t.Errorf("expected no category attribution without content, got %+v", br)
	}
	if br.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", br.OutputTokens)
	}
	// Total must still reflect the authoritative provider totals.
	if br.Total != 1200 {
		t.Errorf("Total = %d, want 1200", br.Total)
	}
	if len(diag.Warnings) == 0 {
		t.Error("expected a warning about uncategorizable input")
	}
}

func TestCategorize_DriftWarning(t *testing.T) {
	c := mustCounter(t)
	p := openAIShaped()
	// Force a large gap between our estimate and the "provider" total.
	p.ProviderInputTokens = 5
	br, diag := Categorize(p, c)

	if br.SystemPromptTokens+br.ToolDefinitionTokens+br.ConversationTokens+br.ToolOutputTokens != 5 {
		t.Errorf("categories should still scale to provider total 5, got %+v", br)
	}
	if len(diag.Warnings) == 0 || !strings.Contains(diag.Warnings[0], "drift") {
		t.Errorf("expected a drift warning, got %v", diag.Warnings)
	}
}

func TestApportion(t *testing.T) {
	cases := []struct {
		name   string
		values []int
		target int
	}{
		{"even", []int{10, 10, 10, 10}, 100},
		{"skewed", []int{1, 2, 3, 94}, 50},
		{"rounding", []int{1, 1, 1}, 10}, // 10/3 => needs remainder distribution
		{"zeros", []int{0, 0, 0, 0}, 100},
		{"one-nonzero", []int{0, 7, 0, 0}, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := apportion(tc.values, tc.target)
			sum := 0
			for _, v := range out {
				if v < 0 {
					t.Fatalf("negative apportioned value: %v", out)
				}
				sum += v
			}
			inputSum := 0
			for _, v := range tc.values {
				inputSum += v
			}
			want := tc.target
			if inputSum == 0 {
				want = 0 // nothing to distribute
			}
			if sum != want {
				t.Errorf("apportion(%v,%d) sums to %d, want %d (%v)",
					tc.values, tc.target, sum, want, out)
			}
		})
	}
}
