# Agent Tool Observability — Implementation Spec (v0.1)

**Working name:** TBD (suggestions at end). Referred to below as **"the tool."**

## 1. Problem Statement

Developers building custom agents / sub-agents cannot easily answer three questions:

1. **Where are my tokens going?** Not "this run cost $0.14" — but how much of that is tool *definitions* (schemas re-sent every turn), conversation history, tool *outputs*, and actual model generation.
2. **How many tool calls happened, and to which tools?** Per run, per session, per tool — to spot chatty/expensive tools.
3. **When and why do tool calls fail?** Not just "errored" — but *malformed arguments* (schema validation failure), *execution failure* (the tool itself threw/timed out), or *suspected misuse* (agent picked the wrong tool, or retried the same tool with different args, suggesting confusion).

Existing observability platforms (Langfuse, Phoenix, Laminar, Braintrust, generic OTel backends) capture the raw trace data needed to answer these questions, but none of them surface the answers as first-class metrics. Users currently build this themselves with ad hoc queries, if at all.

## 2. Goals & Non-Goals

**Goals (v1):**
- Ingest agent run telemetry (OTel-native where available, minimal manual SDK otherwise).
- Compute and display a **token breakdown by category** per run and aggregated over time.
- Compute and display **tool call statistics**: count, latency, success/failure rate, per tool.
- Classify tool call failures into a **small, useful taxonomy** and surface the worst offenders.
- Ship as a **single binary**, zero required external dependencies (embedded DB), runnable locally or as a long-lived daemon.
- Provide both a CLI (quick local use) and a lightweight local web dashboard (visual use).

**Non-goals (v1 — resist scope creep):**
- Prompt management / prompt versioning (Langfuse's territory).
- LLM-as-judge quality evaluation / hallucination scoring (Phoenix/Braintrust's territory).
- Multi-tenant hosted SaaS, auth, billing.
- Being a general-purpose observability backend (no arbitrary span types, no infra/HTTP tracing beyond what's needed for context).
- Full OTel Collector replacement — you are a specialized consumer/processor, not a general collector, at least initially.

If asked "should this also do X" during implementation, default answer is no unless X directly serves token-breakdown or tool-failure visibility.

## 3. High-Level Architecture

```
                     ┌─────────────────────────────┐
  Agent / Framework  │   Instrumentation sources    │
  (opencode, custom  │                              │
  Go/Python agent,   │  A) Native OTel/OTLP export  │
  LangChain, etc.)   │  B) Manual SDK (fallback)    │
                     └──────────────┬───────────────┘
                                    │ OTLP (gRPC/HTTP)
                                    ▼
                     ┌─────────────────────────────┐
                     │   Ingest Layer (this tool)   │
                     │  - OTLP receiver             │
                     │  - gen_ai.* attribute parser │
                     │  - adapter layer (schema     │
                     │    drift tolerance)          │
                     └──────────────┬───────────────┘
                                    ▼
                     ┌─────────────────────────────┐
                     │   Processing Layer           │
                     │  - Token categorizer         │
                     │  - Tool-call failure          │
                     │    classifier                │
                     │  - Span → Run aggregator      │
                     └──────────────┬───────────────┘
                                    ▼
                     ┌─────────────────────────────┐
                     │   Storage (embedded)         │
                     │  - SQLite or DuckDB          │
                     └──────────────┬───────────────┘
                                    ▼
                     ┌─────────────────────────────┐
                     │   Query + Presentation       │
                     │  - CLI                       │
                     │  - Local HTTP + web dashboard│
                     └─────────────────────────────┘
```

Everything above runs in **one process, one binary**. No Kafka, no Postgres, no Redis. This is your core differentiator vs. every existing platform — protect it.

## 4. Data Model

### 4.1 Core entities

**Run** — one agent execution (maps to `invoke_agent` span in OTel GenAI conventions).
```go
type Run struct {
    ID          string    // trace_id from OTel, or generated if absent
    AgentName   string
    StartedAt   time.Time
    EndedAt     time.Time
    Status      string    // "success" | "error" | "in_progress"
    TotalTokens TokenBreakdown
    ToolCalls   []ToolCall
    LLMCalls    []LLMCall
    Metadata    map[string]string // arbitrary resource attributes
}
```

**LLMCall** — one `chat` span (one model invocation).
```go
type LLMCall struct {
    ID              string
    RunID           string
    ParentSpanID    string // for nested sub-agent calls
    Model           string
    StartedAt       time.Time
    DurationMs      int64
    FinishReason    string
    TokenBreakdown  TokenBreakdown
}
```

**TokenBreakdown** — the categorized token count. This is the core novel data structure.
```go
type TokenBreakdown struct {
    SystemPromptTokens   int
    ToolDefinitionTokens int  // schemas for all available tools sent this call
    ConversationTokens   int  // prior message history
    ToolOutputTokens     int  // results fed back in from prior tool calls
    OutputTokens         int  // what the model generated this call
    Total                int
}
```

**ToolCall** — one `execute_tool` span.
```go
type ToolCall struct {
    ID            string
    RunID         string
    LLMCallID     string // which model turn triggered this
    ToolName      string
    Arguments     json.RawMessage
    Result        json.RawMessage
    StartedAt     time.Time
    DurationMs    int64
    Status        string // "success" | "schema_error" | "execution_error" | "timeout" | "suspected_misuse"
    FailureDetail string // human-readable classification reason
}
```

### 4.2 Storage schema (SQLite DDL sketch)

```sql
CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    agent_name TEXT,
    started_at INTEGER,
    ended_at INTEGER,
    status TEXT,
    total_tokens INTEGER,
    metadata JSON
);

CREATE TABLE llm_calls (
    id TEXT PRIMARY KEY,
    run_id TEXT REFERENCES runs(id),
    parent_span_id TEXT,
    model TEXT,
    started_at INTEGER,
    duration_ms INTEGER,
    finish_reason TEXT,
    system_prompt_tokens INTEGER,
    tool_definition_tokens INTEGER,
    conversation_tokens INTEGER,
    tool_output_tokens INTEGER,
    output_tokens INTEGER
);

CREATE TABLE tool_calls (
    id TEXT PRIMARY KEY,
    run_id TEXT REFERENCES runs(id),
    llm_call_id TEXT REFERENCES llm_calls(id),
    tool_name TEXT,
    arguments JSON,
    result JSON,
    started_at INTEGER,
    duration_ms INTEGER,
    status TEXT,
    failure_detail TEXT
);

CREATE INDEX idx_tool_calls_tool_name ON tool_calls(tool_name);
CREATE INDEX idx_tool_calls_status ON tool_calls(status);
CREATE INDEX idx_llm_calls_run_id ON llm_calls(run_id);
```

Use DuckDB instead of SQLite if you want fast columnar aggregation queries ("top 10 tools by token cost over last 7 days") to scale better — worth a quick spike to decide (see Section 13).

## 5. Ingestion Layer

### 5.1 Mode A — OTLP receiver (primary path)

Stand up an OTLP receiver (gRPC on `4317`, HTTP on `4318` — match standard OTel Collector ports so it's a drop-in `OTEL_EXPORTER_OTLP_ENDPOINT` target). Accept spans from:
- Native OTel-emitting agents (opencode, Claude Code, Codex, etc.)
- OpenLLMetry/OpenInference-instrumented custom apps

For each incoming span:
1. Check `gen_ai.operation.name` to classify span type (`invoke_agent`, `chat`, `execute_tool`).
2. Extract known-stable attributes: `gen_ai.request.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.response.finish_reasons`.
3. Extract tool-call attributes: tool name, arguments, result (when content capture is enabled).
4. Route to the Processing Layer.

**Adapter layer requirement:** GenAI semantic conventions are pre-stable and attribute names may shift (confirmed as of mid-2026 — no pinned schema version exists yet). Do not hardcode attribute name strings throughout the codebase. Centralize all `gen_ai.*` attribute name lookups behind a single mapping table/config so a spec rename is a one-file change, not a refactor.

### 5.2 Mode B — Manual SDK (fallback for non-instrumented agents)

Many custom/hand-rolled agents won't emit OTel at all. Ship a minimal SDK (start with Go and Python — Python because that's where most agent code lives even if your tool is Go) that wraps:
```go
tool.RecordLLMCall(ctx, model, systemPrompt, toolDefs, history, toolOutputs, response)
tool.RecordToolCall(ctx, toolName, args, result, err)
```
Internally, this SDK just emits the same OTLP spans your Mode A receiver already understands — so Mode B is a thin producer in front of the same pipeline, not a separate code path. This is important: **build one ingestion pipeline, two producers.**

### 5.3 Token counting

You will frequently receive raw text (system prompt, tool schemas, conversation, tool outputs) rather than pre-counted tokens, especially in Mode B. You need a tokenizer:
- Go: use a `tiktoken`-compatible library (e.g. `pkoukk/tiktoken-go`) for OpenAI-family models; note this is an approximation for non-OpenAI models (Anthropic, Gemini) — flag counts as "estimated" in the UI when the exact provider count isn't available in the span.
- Prefer provider-reported `gen_ai.usage.*` totals when present (authoritative), and use your own tokenizer only to **split** that total into categories when the provider doesn't break it down itself (which is the common case — providers report total input tokens, not tool-defs-vs-history-vs-output).

## 6. Token Categorization Engine

Given one LLM call's raw request payload (or reconstructed equivalent from span attributes):

1. Isolate the `tools`/`functions` parameter → tokenize → `ToolDefinitionTokens`.
2. Isolate the system/developer message → tokenize → `SystemPromptTokens`.
3. Isolate prior conversation messages (everything before the newest user/tool turn) → tokenize → `ConversationTokens`.
4. Isolate tool result messages fed back in this turn → tokenize → `ToolOutputTokens`.
5. Take `gen_ai.usage.output_tokens` directly → `OutputTokens`.
6. Sanity-check: sum of categories should roughly equal `gen_ai.usage.input_tokens` + `output_tokens`; log a warning (don't crash) if there's meaningful drift, since provider tokenizers won't match yours exactly.

This categorization is the single most novel piece of logic in the tool — invest the most design/testing time here. Write it as a pure function (`payload → TokenBreakdown`) with a strong unit test suite across OpenAI/Anthropic/Gemini-shaped payloads, independent of the ingestion transport, so you can test it without a live agent.

## 7. Tool Call Failure Classification Engine

Rules-based for v1 (no ML needed):

| Classification | Detection rule |
|---|---|
| `schema_error` | Arguments fail JSON-schema validation against the tool's declared schema, before execution |
| `execution_error` | The `execute_tool` span itself has OTel span status `ERROR`, or result contains an exception/non-2xx equivalent |
| `timeout` | Span duration exceeds a configurable per-tool threshold with no result |
| `suspected_misuse` | Heuristic: same tool called again within N turns with materially different arguments (signals the agent didn't get what it needed), OR a tool call is immediately followed by another call to a *different* tool covering similar intent (signals wrong-tool selection) |
| `success` | None of the above |

Make thresholds (N turns, timeout duration, "materially different" argument diff logic) configurable in a YAML/TOML config file — you will need to tune these against real usage, and users' tolerance will vary by tool.

Surface a **"worst offenders" view**: tools ranked by (a) failure rate, (b) average token cost per call, (c) call frequency — this ranked list is the single most actionable output of the whole tool and should be the default CLI/dashboard view.

## 8. CLI

Suggested command surface (Go, using `cobra` or similar):

```
toolname serve                 # start OTLP receiver + dashboard, foreground or daemon
toolname runs list             # list recent runs, with total tokens + status
toolname runs show <run-id>    # full breakdown for one run: token categories, tool calls, failures
toolname tools stats           # ranked "worst offenders" table across all runs
toolname tools stats --tool X  # drill into one tool: failure history, token cost trend
toolname sdk init              # scaffold Mode B integration snippet for Go/Python
toolname config validate       # sanity-check the config file (thresholds, endpoints)
```

Design the CLI output to be genuinely useful standalone (no dashboard needed) — this matters for adoption by people living in a terminal, and it's a fast way to get a good README GIF.

## 9. Dashboard (local web UI)

Minimal, not a SPA framework project. A single Go binary serving:
- An overview page: token breakdown pie/bar per run, over time.
- A tools page: the "worst offenders" ranked table, sortable by failure rate / token cost / call count.
- A run detail page: waterfall-style view of the run's LLM calls + tool calls, click into any tool call to see args/result/failure reason.

Recommendation: server-rendered HTML + htmx, or a small embedded static React/Vite build served by the Go binary — avoid a separate frontend deploy/build pipeline as a maintenance burden for a solo 10–20 hr/week project. Keep it genuinely optional; CLI must work with zero dashboard.

## 10. Tech Stack Decision

**Recommendation: Go, not Rust, for v1.**

Reasoning specific to this project:
- The OTel Collector itself, and the official OTel Go SDK/receiver components, are Go-native and mature — you can build directly on `go.opentelemetry.io/collector` components (or use the Collector Builder to scaffold a custom receiver/processor) rather than hand-rolling OTLP protobuf handling. Rust's OTel ecosystem is comparatively immature for this specific use case.
- Every comparable successful single-binary tool in this space that you're benchmarking against (`llama-swap`, `mcpproxy-go`, `mcpjungle`) is Go — the audience (self-hosted infra/CLI users) has strong Go-tool affinity already.
- Embedded SQLite (`mattn/go-sqlite3` or the pure-Go `modernc.org/sqlite`) and DuckDB (via `marcboeker/go-duckdb`) both have solid Go bindings.
- You still get single-binary distribution, cross-compilation, and low resource usage — the properties you actually wanted from Rust — without fighting an immature OTel-in-Rust ecosystem on a 10–20 hr/week budget.

Reconsider Rust only if profiling shows the OTLP ingestion path itself becomes a genuine performance bottleneck at a scale where Go's GC pauses matter — unlikely for a local/self-hosted tool at the audience size you're targeting.

## 11. Project Layout (suggested)

```
/cmd/toolname/           # main entrypoint, CLI commands
/internal/ingest/        # OTLP receiver, span parsing, adapter/attribute-mapping layer
/internal/tokenize/      # token categorization engine (pure functions, heavily unit-tested)
/internal/classify/      # tool-call failure classification engine
/internal/store/         # SQLite/DuckDB storage layer, migrations
/internal/api/           # local HTTP API for the dashboard
/web/                    # dashboard static assets (if separate build)
/sdk/go/                 # Mode B Go SDK
/sdk/python/             # Mode B Python SDK
/config/                 # example config files
/docs/                   # README, architecture docs
```

## 12. Milestones

**v0.1 (weeks 1–3): Ingest + categorize, CLI only, no dashboard.**
- OTLP receiver accepting spans from at least one real source (opencode, or OpenLLMetry-instrumented sample app).
- Token categorization engine with unit tests across 2–3 payload shapes (OpenAI, Anthropic).
- SQLite storage.
- `runs list` / `runs show` CLI commands.
- Go/no-go: can you point it at a real opencode session and get a sane token breakdown? If the categorization math is clearly wrong or attribute parsing is too fragile, fix here before adding scope.

**v0.2 (weeks 4–6): Failure classification + "worst offenders."**
- Implement the classification rules table.
- `tools stats` CLI command with ranked output.
- Mode B SDK (Go first) for one hand-rolled test agent, to validate the fallback path works end-to-end.

**v0.3 (weeks 7–9): Dashboard + polish for launch.**
- Local web dashboard (overview, tools, run detail pages).
- README with a strong GIF/screenshot showing a real token-breakdown + worst-offenders view.
- `go install`/Homebrew/prebuilt-binary distribution.

**v1.0 launch (week 10+): Distribution.**
- Show HN + r/LocalLLaMA / relevant agent-framework Discord or subreddit post.
- Target: 50+ stars in the first week as a signal to keep investing; 200–300 within ~3 months per your original goal.

## 13. Open Questions / Risks (resolve early)

1. **DuckDB vs SQLite** — spike both against realistic aggregation queries ("token cost by tool over 30 days across 10k runs") before committing; SQLite is simpler to embed, DuckDB is faster for analytical rollups. Don't over-index on this early — SQLite is very likely fine at solo/small-team scale.
2. **Tokenizer accuracy across providers** — your counts will be *approximate* for non-OpenAI models unless you find/port a matching tokenizer; decide early whether to label estimated counts as such in the UI (recommended, for credibility).
3. **`gen_ai.*` schema drift** — re-check the spec's status periodically during v0.1–v0.2 build; the adapter layer (Section 5.1) is your insurance against this, don't skip it to save time.
4. **opencode's flaky OTel export** — validate early against a real opencode session rather than assuming the docs are accurate; budget time to work around gaps/bugs in their emitter if you pick opencode as your first integration target.
5. **"Suspected misuse" heuristic quality** — this is the riskiest, least mechanical part of the spec. Expect to tune it against real failure examples; don't over-engineer before you have real data to tune against.

## 14. Naming

Not decided — brainstorm candidates that signal "tool economics / tool health" rather than generic "observability" (which is an overloaded, crowded term). Check npm/crates.io/GitHub for collisions before settling.

---

*This spec is intentionally scoped to be buildable by one person at 10–20 hrs/week within ~2–3 months to first public launch. Resist adding scope beyond Section 2's stated goals until v1.0 ships and gets real user feedback.*