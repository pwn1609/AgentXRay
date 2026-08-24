package ingest_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/pwn1609/AgentXRay/internal/ingest"
	"github.com/pwn1609/AgentXRay/internal/model"
	"github.com/pwn1609/AgentXRay/internal/store"
	"github.com/pwn1609/AgentXRay/internal/tokenize"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func kvS(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{
		Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func kvI(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{
		Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

// testServer spins up a receiver backed by a temp-file store and returns it.
func testServer(t *testing.T, captureContent bool) (*ingest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	counter, err := tokenize.NewCounter("")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	proc := ingest.NewProcessor(st, counter, captureContent, nil)
	srv := ingest.NewServer("127.0.0.1:0", "127.0.0.1:0", proc, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Shutdown(); st.Close() })
	return srv, st
}

// A1: the gRPC path (previously only HTTP was exercised end-to-end).
func TestGRPCExport_EndToEnd(t *testing.T) {
	srv, st := testServer(t, true)
	grpcAddr, _ := srv.Addrs()

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()
	client := coltracepb.NewTraceServiceClient(conn)

	traceID := []byte("grpc-trace-00001")
	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{kvS("service.name", "grpc-agent")}},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
				TraceId: traceID, SpanId: []byte("gspan001"),
				StartTimeUnixNano: 1_700_000_000_000_000_000, EndTimeUnixNano: 1_700_000_000_900_000_000,
				Attributes: []*commonpb.KeyValue{
					kvS("gen_ai.operation.name", ingest.OpChat),
					kvS("gen_ai.request.model", "gpt-4o-mini"),
					kvI("gen_ai.usage.input_tokens", 300),
					kvI("gen_ai.usage.output_tokens", 45),
					kvS("gen_ai.request.system_instructions", "Be brief."),
				},
			}}}},
		}},
	}
	if _, err := client.Export(context.Background(), req); err != nil {
		t.Fatalf("gRPC Export: %v", err)
	}

	runs, err := st.ListRuns(context.Background(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) != 1 || runs[0].AgentName != "grpc-agent" {
		t.Fatalf("expected 1 grpc-agent run, got %+v", runs)
	}
	if runs[0].TotalTokens.Total != 345 {
		t.Errorf("total = %d, want 345", runs[0].TotalTokens.Total)
	}
}

// A2: the HTTP receiver rejects malformed input cleanly.
func TestHTTP_RejectsBadInput(t *testing.T) {
	srv, _ := testServer(t, true)
	_, httpAddr := srv.Addrs()
	url := "http://" + httpAddr + "/v1/traces"

	// Garbage protobuf body -> 400.
	resp, err := http.Post(url, "application/x-protobuf", bytes.NewReader([]byte("not-a-protobuf")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad protobuf: status = %d, want 400", resp.StatusCode)
	}

	// Malformed JSON body -> 400.
	resp, err = http.Post(url, "application/json", strings.NewReader("{ not json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: status = %d, want 400", resp.StatusCode)
	}

	// Wrong method -> 405.
	greq, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err = http.DefaultClient.Do(greq)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: status = %d, want 405", resp.StatusCode)
	}
}

// A2: with content capture disabled, tool args/results must not be persisted.
func TestCaptureContentDisabled(t *testing.T) {
	counter, _ := tokenize.NewCounter("")
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proc := ingest.NewProcessor(st, counter, false, nil)

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
				TraceId: []byte("capture-trace-01"), SpanId: []byte("captool1"),
				StartTimeUnixNano: 1_700_000_000_000_000_000, EndTimeUnixNano: 1_700_000_000_100_000_000,
				Attributes: []*commonpb.KeyValue{
					kvS("gen_ai.operation.name", ingest.OpExecuteTool),
					kvS("gen_ai.tool.name", "secret_tool"),
					kvS("gen_ai.tool.call.arguments", `{"password":"hunter2"}`),
					kvS("gen_ai.tool.call.result", `{"ok":true}`),
				},
			}}}},
		}},
	}
	if err := proc.ConsumeTraces(context.Background(), req); err != nil {
		t.Fatalf("consume: %v", err)
	}
	runs, _ := st.ListRuns(context.Background(), 10)
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	full, _ := st.GetRun(context.Background(), runs[0].ID)
	if len(full.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(full.ToolCalls))
	}
	tc := full.ToolCalls[0]
	if tc.ToolName != "secret_tool" {
		t.Errorf("tool name lost: %q", tc.ToolName)
	}
	if tc.Arguments != nil || tc.Result != nil {
		t.Errorf("content should not be captured: args=%s result=%s", tc.Arguments, tc.Result)
	}
}

// A2: two export batches for the same trace id must merge (upsert), not duplicate.
func TestMultiBatchUpsertMerge(t *testing.T) {
	counter, _ := tokenize.NewCounter("")
	st, err := store.Open(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proc := ingest.NewProcessor(st, counter, true, nil)
	ctx := context.Background()
	trace := []byte("merge-trace-0001")

	chat := func(spanID string, in, out int64) *tracepb.Span {
		return &tracepb.Span{
			TraceId: trace, SpanId: []byte(spanID),
			StartTimeUnixNano: 1_700_000_000_000_000_000, EndTimeUnixNano: 1_700_000_000_500_000_000,
			Attributes: []*commonpb.KeyValue{
				kvS("gen_ai.operation.name", ingest.OpChat),
				kvS("gen_ai.request.model", "gpt-4o"),
				kvI("gen_ai.usage.input_tokens", in),
				kvI("gen_ai.usage.output_tokens", out),
			},
		}
	}
	mk := func(spans ...*tracepb.Span) *coltracepb.ExportTraceServiceRequest {
		return &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
			Resource:   &resourcepb.Resource{Attributes: []*commonpb.KeyValue{kvS("service.name", "merger")}},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}}}
	}

	if err := proc.ConsumeTraces(ctx, mk(chat("mspan001", 100, 10))); err != nil {
		t.Fatal(err)
	}
	if err := proc.ConsumeTraces(ctx, mk(chat("mspan002", 200, 20))); err != nil {
		t.Fatal(err)
	}

	runs, _ := st.ListRuns(ctx, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 merged run, got %d", len(runs))
	}
	// ListRuns reads the stored column, which SaveRun keeps authoritative.
	if runs[0].TotalTokens.Total != 330 { // (100+10)+(200+20)
		t.Errorf("ListRuns merged total = %d, want 330", runs[0].TotalTokens.Total)
	}
	full, _ := st.GetRun(ctx, runs[0].ID)
	if len(full.LLMCalls) != 2 {
		t.Fatalf("expected 2 merged LLM calls, got %d", len(full.LLMCalls))
	}
	if full.TotalTokens.Total != 330 {
		t.Errorf("GetRun merged total = %d, want 330", full.TotalTokens.Total)
	}
	if full.Status != model.RunStatusSuccess {
		t.Errorf("merged status = %q, want success", full.Status)
	}
}
