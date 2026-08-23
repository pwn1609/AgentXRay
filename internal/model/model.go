package model

import (
	"encoding/json"
	"time"
)

// Run status values.
const (
	RunStatusInProgress = "in_progress"
	RunStatusSuccess    = "success"
	RunStatusError      = "error"
)

// ToolCall status values. The full failure taxonomy (schema_error, timeout,
// suspected_misuse) is v0.2; v0.1 only distinguishes success vs execution_error.
const (
	ToolStatusSuccess        = "success"
	ToolStatusExecutionError = "execution_error"
)

// Run is one agent execution (maps to an invoke_agent span).
type Run struct {
	ID          string            `json:"id"` // trace_id, or generated if absent
	AgentName   string            `json:"agent_name"`
	StartedAt   time.Time         `json:"started_at"`
	EndedAt     time.Time         `json:"ended_at"`
	Status      string            `json:"status"`
	TotalTokens TokenBreakdown    `json:"total_tokens"`
	ToolCalls   []ToolCall        `json:"tool_calls,omitempty"`
	LLMCalls    []LLMCall         `json:"llm_calls,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// LLMCall is one chat span (one model invocation).
type LLMCall struct {
	ID             string         `json:"id"`
	RunID          string         `json:"run_id"`
	ParentSpanID   string         `json:"parent_span_id,omitempty"` // for nested sub-agent calls
	Model          string         `json:"model"`
	StartedAt      time.Time      `json:"started_at"`
	DurationMs     int64          `json:"duration_ms"`
	FinishReason   string         `json:"finish_reason,omitempty"`
	TokenBreakdown TokenBreakdown `json:"token_breakdown"`
}

// TokenBreakdown is the categorized token count — the core novel data structure.
type TokenBreakdown struct {
	SystemPromptTokens   int `json:"system_prompt_tokens"`
	ToolDefinitionTokens int `json:"tool_definition_tokens"` // schemas for all available tools sent this call
	ConversationTokens   int `json:"conversation_tokens"`    // prior message history
	ToolOutputTokens     int `json:"tool_output_tokens"`     // results fed back from prior tool calls
	OutputTokens         int `json:"output_tokens"`          // what the model generated this call
	Total                int `json:"total"`
	// Estimated is true when the per-category split was derived from our own
	// tokenizer rather than authoritative provider-reported counts.
	Estimated bool `json:"estimated,omitempty"`
}

// Add returns the element-wise sum of two breakdowns. The Estimated flag is
// sticky: the result is estimated if either operand was.
func (t TokenBreakdown) Add(o TokenBreakdown) TokenBreakdown {
	return TokenBreakdown{
		SystemPromptTokens:   t.SystemPromptTokens + o.SystemPromptTokens,
		ToolDefinitionTokens: t.ToolDefinitionTokens + o.ToolDefinitionTokens,
		ConversationTokens:   t.ConversationTokens + o.ConversationTokens,
		ToolOutputTokens:     t.ToolOutputTokens + o.ToolOutputTokens,
		OutputTokens:         t.OutputTokens + o.OutputTokens,
		Total:                t.Total + o.Total,
		Estimated:            t.Estimated || o.Estimated,
	}
}

// ToolCall is one execute_tool span.
type ToolCall struct {
	ID            string          `json:"id"`
	RunID         string          `json:"run_id"`
	LLMCallID     string          `json:"llm_call_id"` // which model turn triggered this
	ToolName      string          `json:"tool_name"`
	Arguments     json.RawMessage `json:"arguments,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	StartedAt     time.Time       `json:"started_at"`
	DurationMs    int64           `json:"duration_ms"`
	Status        string          `json:"status"`
	FailureDetail string          `json:"failure_detail,omitempty"`
}
