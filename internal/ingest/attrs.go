package ingest

// This file is the ONE place that knows OpenTelemetry GenAI (`gen_ai.*`)
// attribute name strings (SPEC §5.1). The GenAI semantic conventions are
// pre-stable and names may shift; a spec rename must be a one-file change here,
// never a codebase-wide refactor. Everywhere else refers to these logical keys.
//
// Each logical field lists candidate attribute names in priority order; the
// first present on a span wins. This tolerates drift and cross-instrumentation
// differences (OTel GenAI, OpenLLMetry, OpenInference).

import (
	"strconv"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// Span operation classification (value of gen_ai.operation.name).
const (
	OpInvokeAgent = "invoke_agent"
	OpChat        = "chat"
	OpExecuteTool = "execute_tool"
)

// Logical attribute keys → candidate attribute names (priority order).
var (
	attrOperation = []string{"gen_ai.operation.name"}
	attrSystem    = []string{"gen_ai.system", "gen_ai.provider.name"}
	attrModel     = []string{"gen_ai.request.model", "gen_ai.response.model", "llm.model_name"}
	attrAgentName = []string{"gen_ai.agent.name", "service.name"}

	attrInputTokens  = []string{"gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens", "llm.token_count.prompt"}
	attrOutputTokens = []string{"gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens", "llm.token_count.completion"}
	attrFinishReason = []string{"gen_ai.response.finish_reasons", "gen_ai.response.finish_reason"}

	// Content used to categorize a chat request. Carried as JSON on span
	// attributes when content capture is enabled.
	attrSystemPrompt = []string{"gen_ai.request.system_instructions", "gen_ai.request.system"}
	attrToolDefs     = []string{"gen_ai.request.tools", "llm.tools"}         // JSON array
	attrMessages     = []string{"gen_ai.request.messages", "gen_ai.prompt"}  // JSON array of {role,content}
	attrResponseText = []string{"gen_ai.response.text", "gen_ai.completion"} // string

	// Tool-call span fields.
	attrToolName      = []string{"gen_ai.tool.name", "tool.name"}
	attrToolArguments = []string{"gen_ai.tool.call.arguments", "gen_ai.tool.arguments", "tool.parameters"}
	attrToolResult    = []string{"gen_ai.tool.call.result", "gen_ai.tool.result", "tool.result"}
)

// attrView provides typed lookups over a span's (or resource's) attributes.
type attrView struct {
	m map[string]*commonpb.AnyValue
}

func newAttrView(kvs []*commonpb.KeyValue) attrView {
	m := make(map[string]*commonpb.AnyValue, len(kvs))
	for _, kv := range kvs {
		if kv != nil {
			m[kv.Key] = kv.Value
		}
	}
	return attrView{m: m}
}

func (a attrView) raw(keys []string) (*commonpb.AnyValue, bool) {
	for _, k := range keys {
		if v, ok := a.m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// str returns the first present value rendered as a string (arrays are joined).
func (a attrView) str(keys []string) string {
	v, ok := a.raw(keys)
	if !ok {
		return ""
	}
	return anyToString(v)
}

// intVal returns the first present value coerced to int (0 if absent/unparseable).
func (a attrView) intVal(keys []string) int {
	v, ok := a.raw(keys)
	if !ok {
		return 0
	}
	switch x := v.Value.(type) {
	case *commonpb.AnyValue_IntValue:
		return int(x.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return int(x.DoubleValue)
	case *commonpb.AnyValue_StringValue:
		if n, err := strconv.Atoi(strings.TrimSpace(x.StringValue)); err == nil {
			return n
		}
	}
	return 0
}

func anyToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'f', -1, 64)
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	case *commonpb.AnyValue_ArrayValue:
		parts := make([]string, 0, len(x.ArrayValue.Values))
		for _, e := range x.ArrayValue.Values {
			parts = append(parts, anyToString(e))
		}
		return strings.Join(parts, ",")
	}
	return ""
}

// allStrings flattens attributes into a plain string map (for Run.Metadata).
func (a attrView) allStrings() map[string]string {
	out := make(map[string]string, len(a.m))
	for k, v := range a.m {
		out[k] = anyToString(v)
	}
	return out
}
