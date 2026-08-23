package ingest

import (
	"context"
	"testing"

	"github.com/pwn1609/AgentXRay/internal/model"
	"github.com/pwn1609/AgentXRay/internal/tokenize"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func kvStr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{
		Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func kvInt(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{
		Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

type captureSink struct{ runs []*model.Run }

func (c *captureSink) SaveRun(_ context.Context, r *model.Run) error {
	c.runs = append(c.runs, r)
	return nil
}

func TestConsumeTraces_AssemblesRun(t *testing.T) {
	counter, err := tokenize.NewCounter("")
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}

	traceID := []byte("0123456789abcdef") // 16 bytes
	agentSpanID := []byte("agentaaa")     // 8 bytes
	chatSpanID := []byte("chataaaa")
	toolSpanID := []byte("toolaaaa")

	const base = uint64(1_700_000_000_000_000_000)

	agentSpan := &tracepb.Span{
		TraceId: traceID, SpanId: agentSpanID,
		StartTimeUnixNano: base, EndTimeUnixNano: base + 2_000_000_000,
		Attributes: []*commonpb.KeyValue{
			kvStr("gen_ai.operation.name", OpInvokeAgent),
			kvStr("gen_ai.agent.name", "weather-bot"),
		},
	}
	chatSpan := &tracepb.Span{
		TraceId: traceID, SpanId: chatSpanID, ParentSpanId: agentSpanID,
		StartTimeUnixNano: base, EndTimeUnixNano: base + 1_200_000_000,
		Attributes: []*commonpb.KeyValue{
			kvStr("gen_ai.operation.name", OpChat),
			kvStr("gen_ai.request.model", "gpt-4o"),
			kvInt("gen_ai.usage.input_tokens", 500),
			kvInt("gen_ai.usage.output_tokens", 60),
			kvStr("gen_ai.response.finish_reasons", "stop"),
			kvStr("gen_ai.request.system_instructions", "You are a concise weather assistant."),
			kvStr("gen_ai.request.tools", `[{"name":"get_weather","description":"Get weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]`),
			kvStr("gen_ai.request.messages", `[{"role":"user","content":"Weather in Paris? Umbrella needed?"},{"role":"tool","content":"{\"temp_c\":14,\"condition\":\"light rain\"}"}]`),
			kvStr("gen_ai.response.text", "Yes, bring an umbrella; light rain in Paris."),
		},
	}
	toolSpan := &tracepb.Span{
		TraceId: traceID, SpanId: toolSpanID, ParentSpanId: chatSpanID,
		StartTimeUnixNano: base + 300_000_000, EndTimeUnixNano: base + 340_000_000,
		Attributes: []*commonpb.KeyValue{
			kvStr("gen_ai.operation.name", OpExecuteTool),
			kvStr("gen_ai.tool.name", "get_weather"),
			kvStr("gen_ai.tool.call.arguments", `{"city":"Paris"}`),
		},
		Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "upstream 503"},
	}

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				kvStr("service.name", "weather-service"),
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{agentSpan, chatSpan, toolSpan},
			}},
		}},
	}

	sink := &captureSink{}
	proc := NewProcessor(sink, counter, true, nil)
	if err := proc.ConsumeTraces(context.Background(), req); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}

	if len(sink.runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(sink.runs))
	}
	run := sink.runs[0]

	if run.AgentName != "weather-bot" {
		t.Errorf("AgentName = %q, want weather-bot", run.AgentName)
	}
	if run.Status != model.RunStatusError { // tool span errored
		t.Errorf("Status = %q, want error", run.Status)
	}
	if run.Metadata["service.name"] != "weather-service" {
		t.Errorf("metadata missing service.name: %v", run.Metadata)
	}
	if len(run.LLMCalls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(run.LLMCalls))
	}
	llm := run.LLMCalls[0]
	if llm.Model != "gpt-4o" || llm.FinishReason != "stop" {
		t.Errorf("llm fields wrong: %+v", llm)
	}
	inputSum := llm.TokenBreakdown.SystemPromptTokens + llm.TokenBreakdown.ToolDefinitionTokens +
		llm.TokenBreakdown.ConversationTokens + llm.TokenBreakdown.ToolOutputTokens
	if inputSum != 500 {
		t.Errorf("input categories sum = %d, want 500 (scaled to provider total)", inputSum)
	}
	if llm.TokenBreakdown.ToolDefinitionTokens == 0 {
		t.Error("expected tool-definition tokens > 0 (tools were provided)")
	}
	if llm.TokenBreakdown.OutputTokens != 60 {
		t.Errorf("OutputTokens = %d, want 60", llm.TokenBreakdown.OutputTokens)
	}
	if !llm.TokenBreakdown.Estimated {
		t.Error("expected Estimated=true")
	}

	if len(run.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(run.ToolCalls))
	}
	tc := run.ToolCalls[0]
	if tc.ToolName != "get_weather" || tc.Status != model.ToolStatusExecutionError {
		t.Errorf("tool call wrong: %+v", tc)
	}
	if tc.FailureDetail != "upstream 503" {
		t.Errorf("FailureDetail = %q, want 'upstream 503'", tc.FailureDetail)
	}
	if string(tc.Arguments) != `{"city":"Paris"}` {
		t.Errorf("Arguments = %s", tc.Arguments)
	}
	// Run-level total should equal the LLM call's total.
	if run.TotalTokens.Total != llm.TokenBreakdown.Total {
		t.Errorf("run total %d != llm total %d", run.TotalTokens.Total, llm.TokenBreakdown.Total)
	}
}

func TestConsumeTraces_SyntheticRunWithoutAgentSpan(t *testing.T) {
	counter, _ := tokenize.NewCounter("")
	traceID := []byte("fedcba9876543210")
	chatSpanID := []byte("chatbbbb")

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				kvStr("service.name", "solo-agent"),
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					TraceId: traceID, SpanId: chatSpanID,
					StartTimeUnixNano: 1_700_000_000_000_000_000,
					EndTimeUnixNano:   1_700_000_000_500_000_000,
					Attributes: []*commonpb.KeyValue{
						kvStr("gen_ai.operation.name", OpChat),
						kvStr("gen_ai.request.model", "claude-3-5-sonnet"),
						kvInt("gen_ai.usage.input_tokens", 200),
						kvInt("gen_ai.usage.output_tokens", 30),
					},
				}},
			}},
		}},
	}

	sink := &captureSink{}
	proc := NewProcessor(sink, counter, false, nil)
	if err := proc.ConsumeTraces(context.Background(), req); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if len(sink.runs) != 1 {
		t.Fatalf("expected 1 synthesized run, got %d", len(sink.runs))
	}
	run := sink.runs[0]
	if run.AgentName != "solo-agent" {
		t.Errorf("AgentName = %q, want solo-agent (from service.name)", run.AgentName)
	}
	if run.Status != model.RunStatusSuccess {
		t.Errorf("Status = %q, want success", run.Status)
	}
	if len(run.LLMCalls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(run.LLMCalls))
	}
	// No content captured, but provider totals must still surface in the total.
	if run.LLMCalls[0].TokenBreakdown.Total != 230 {
		t.Errorf("Total = %d, want 230", run.LLMCalls[0].TokenBreakdown.Total)
	}
}
