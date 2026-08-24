package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pwn1609/AgentXRay/internal/model"
	"github.com/pwn1609/AgentXRay/internal/tokenize"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// RunSink persists assembled runs. *store.Store satisfies this.
type RunSink interface {
	SaveRun(ctx context.Context, r *model.Run) error
}

// Processor turns batches of OTLP trace spans into runs and persists them.
type Processor struct {
	sink           RunSink
	counter        *tokenize.Counter
	captureContent bool
	log            *slog.Logger
}

// NewProcessor builds a Processor. If log is nil, a discard logger is used.
func NewProcessor(sink RunSink, counter *tokenize.Counter, captureContent bool, log *slog.Logger) *Processor {
	if log == nil {
		log = slog.New(slog.NewTextHandler(nopWriter{}, nil))
	}
	return &Processor{sink: sink, counter: counter, captureContent: captureContent, log: log}
}

// spanWithResource pairs a span with its owning resource attributes.
type spanWithResource struct {
	span *tracepb.Span
	res  attrView
}

// ConsumeTraces processes one OTLP export request: it groups spans by trace id,
// assembles a Run per trace, and upserts each. Because SaveRun upserts, repeated
// exports for the same trace update it incrementally.
func (p *Processor) ConsumeTraces(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	byTrace := map[string][]spanWithResource{}
	for _, rs := range req.GetResourceSpans() {
		var res attrView
		if rs.Resource != nil {
			res = resourceView(rs.Resource.Attributes)
		} else {
			res = newAttrView(nil)
		}
		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				tid := hexID(s.TraceId)
				if tid == "" {
					tid = hexID(s.SpanId) // orphan span: treat as its own run
				}
				byTrace[tid] = append(byTrace[tid], spanWithResource{span: s, res: res})
			}
		}
	}

	p.log.Debug("grouped spans into traces", "traces", len(byTrace))

	var firstErr error
	for tid, spans := range byTrace {
		run := p.assembleRun(tid, spans)
		if run == nil {
			p.log.Info("trace dropped", "run", tid, "spans", len(spans),
				"reason", "no gen_ai spans (no chat/tool/invoke_agent)")
			continue
		}
		p.log.Debug("run assembled", "run", tid, "agent", run.AgentName,
			"llm_calls", len(run.LLMCalls), "tool_calls", len(run.ToolCalls),
			"total_tokens", run.TotalTokens.Total, "status", run.Status)
		if err := p.sink.SaveRun(ctx, run); err != nil {
			p.log.Error("save run failed", "run", tid, "err", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("save run %s: %w", tid, err)
			}
		}
	}
	return firstErr
}

// assembleRun builds a Run from all spans sharing a trace id. An invoke_agent
// span, when present, supplies run-level metadata; otherwise the run is
// synthesized from the chat/tool spans.
func (p *Processor) assembleRun(traceID string, spans []spanWithResource) *model.Run {
	run := &model.Run{ID: traceID, Status: model.RunStatusSuccess}

	var minStart, maxEnd time.Time
	anyError := false
	haveAgentSpan := false

	for _, sw := range spans {
		s := sw.span
		start, end, _ := spanTimes(s)
		if !start.IsZero() && (minStart.IsZero() || start.Before(minStart)) {
			minStart = start
		}
		if end.After(maxEnd) {
			maxEnd = end
		}
		if isError(s) {
			anyError = true
		}

		switch operationOf(s) {
		case OpInvokeAgent:
			haveAgentSpan = true
			v := newAttrView(s.Attributes)
			run.AgentName = firstNonEmpty(v.str(attrAgentName), sw.res.str(attrAgentName))
			run.Metadata = sw.res.allStrings()
			if isError(s) {
				run.Status = model.RunStatusError
			}
		case OpChat:
			call, warnings := llmCallFromSpan(traceID, s, p.counter)
			run.LLMCalls = append(run.LLMCalls, call)
			run.TotalTokens = run.TotalTokens.Add(call.TokenBreakdown)
			for _, w := range warnings {
				p.log.Warn("token categorization", "run", traceID, "llm_call", call.ID, "warning", w)
			}
		case OpExecuteTool:
			run.ToolCalls = append(run.ToolCalls, toolCallFromSpan(traceID, s, p.captureContent))
		default:
			// Non-GenAI span in this trace; ignored (we are a specialized consumer).
			p.log.Debug("span ignored", "run", traceID, "span", s.Name,
				"operation", operationOf(s), "reason", "not a gen_ai chat/tool/invoke_agent span")
		}
	}

	if len(run.LLMCalls) == 0 && len(run.ToolCalls) == 0 && !haveAgentSpan {
		return nil // nothing relevant in this trace
	}

	run.StartedAt = minStart
	run.EndedAt = maxEnd
	if anyError {
		run.Status = model.RunStatusError
	}
	if run.AgentName == "" && len(spans) > 0 {
		run.AgentName = spans[0].res.str(attrAgentName)
	}
	if run.Metadata == nil && len(spans) > 0 {
		run.Metadata = spans[0].res.allStrings()
	}
	return run
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
