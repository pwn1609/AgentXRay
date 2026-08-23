package ingest

import (
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/pwn1609/AgentXRay/internal/model"
	"github.com/pwn1609/AgentXRay/internal/tokenize"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func spanTimes(s *tracepb.Span) (start, end time.Time, durMs int64) {
	if s.StartTimeUnixNano > 0 {
		start = time.Unix(0, int64(s.StartTimeUnixNano)).UTC()
	}
	if s.EndTimeUnixNano > 0 {
		end = time.Unix(0, int64(s.EndTimeUnixNano)).UTC()
	}
	if !start.IsZero() && !end.IsZero() {
		durMs = end.Sub(start).Milliseconds()
	}
	return
}

func isError(s *tracepb.Span) bool {
	return s.Status != nil && s.Status.Code == tracepb.Status_STATUS_CODE_ERROR
}

func hexID(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

// operationOf returns the gen_ai.operation.name for a span (empty if not a GenAI span).
func operationOf(s *tracepb.Span) string {
	return newAttrView(s.Attributes).str(attrOperation)
}

// llmCallFromSpan converts a `chat` span into an LLMCall with a categorized
// TokenBreakdown. resource carries resource-level attributes (unused today but
// kept for future model/provider fallbacks). Any categorization warnings are
// returned for the caller to surface.
func llmCallFromSpan(runID string, s *tracepb.Span, c *tokenize.Counter) (model.LLMCall, []string) {
	v := newAttrView(s.Attributes)
	start, _, durMs := spanTimes(s)

	payload := payloadFromChat(v)
	br, diag := tokenize.Categorize(payload, c)

	return model.LLMCall{
		ID:             hexID(s.SpanId),
		RunID:          runID,
		ParentSpanID:   hexID(s.ParentSpanId),
		Model:          payload.Model,
		StartedAt:      start,
		DurationMs:     durMs,
		FinishReason:   v.str(attrFinishReason),
		TokenBreakdown: br,
	}, diag.Warnings
}

// payloadFromChat builds a transport-independent tokenize.Payload from a chat span.
func payloadFromChat(v attrView) tokenize.Payload {
	p := tokenize.Payload{
		Model:                v.str(attrModel),
		System:               v.str(attrSystemPrompt),
		ProviderInputTokens:  v.intVal(attrInputTokens),
		ProviderOutputTokens: v.intVal(attrOutputTokens),
		ResponseText:         v.str(attrResponseText),
	}
	if raw := v.str(attrToolDefs); raw != "" {
		p.Tools = parseToolDefs(raw)
	}
	if raw := v.str(attrMessages); raw != "" {
		p.Messages = parseMessages(raw)
	}
	return p
}

// toolCallFromSpan converts an `execute_tool` span into a ToolCall. When
// captureContent is false, arguments and results are not persisted.
func toolCallFromSpan(runID string, s *tracepb.Span, captureContent bool) model.ToolCall {
	v := newAttrView(s.Attributes)
	start, _, durMs := spanTimes(s)

	status := model.ToolStatusSuccess
	detail := ""
	if isError(s) {
		status = model.ToolStatusExecutionError
		if s.Status != nil {
			detail = s.Status.Message
		}
	}

	tc := model.ToolCall{
		ID:            hexID(s.SpanId),
		RunID:         runID,
		LLMCallID:     hexID(s.ParentSpanId), // parent chat turn that triggered the tool
		ToolName:      v.str(attrToolName),
		StartedAt:     start,
		DurationMs:    durMs,
		Status:        status,
		FailureDetail: detail,
	}
	if captureContent {
		tc.Arguments = rawJSON(v.str(attrToolArguments))
		tc.Result = rawJSON(v.str(attrToolResult))
	}
	return tc
}

// parseToolDefs unmarshals a JSON array of tool schemas into []ToolDef, extracting
// a name from common shapes (top-level "name", or nested "function.name").
func parseToolDefs(raw string) []tokenize.ToolDef {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		// Not an array — treat the whole blob as one unnamed tool definition.
		return []tokenize.ToolDef{{Raw: json.RawMessage(raw)}}
	}
	out := make([]tokenize.ToolDef, 0, len(arr))
	for _, item := range arr {
		out = append(out, tokenize.ToolDef{Name: toolNameOf(item), Raw: item})
	}
	return out
}

func toolNameOf(item json.RawMessage) string {
	var probe struct {
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	_ = json.Unmarshal(item, &probe)
	if probe.Name != "" {
		return probe.Name
	}
	return probe.Function.Name
}

// parseMessages unmarshals a JSON array of {role, content} into tokenize.Message.
// Content may be a JSON string or a structured block; both are reduced to text.
func parseMessages(raw string) []tokenize.Message {
	var arr []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	out := make([]tokenize.Message, 0, len(arr))
	for _, m := range arr {
		out = append(out, tokenize.Message{
			Role:    tokenize.Role(m.Role),
			Content: contentToText(m.Content),
		})
	}
	return out
}

func contentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Non-string content (e.g. content blocks): use the raw JSON as a proxy so
	// its tokens are still counted.
	return string(raw)
}

func rawJSON(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

// resourceView builds an attrView from resource attributes.
func resourceView(attrs []*commonpb.KeyValue) attrView { return newAttrView(attrs) }
