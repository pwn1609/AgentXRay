# AgentXRay — MVP (v0.1) Implementation Plan

> Derived from `SPEC.md`. This plan covers the **v0.1 MVP only**. Later milestones
> (failure classification, dashboard, Mode B SDKs, distribution) are explicitly deferred.

## Locked Decisions

| Decision | Choice |
|---|---|
| **Scope** | v0.1 only: OTLP ingest → token categorization → SQLite → CLI |
| **Language** | Go (cobra CLI, `go.opentelemetry.io/collector` receiver components) |
| **First data source** | Synthetic/replay OTLP fixtures (OpenAI + Anthropic shapes) |
| **Storage** | SQLite via pure-Go `modernc.org/sqlite` |
| **Interface** | CLI only (`serve`, `runs list`, `runs show`) |
| **Transport** | Both gRPC `:4317` + HTTP/protobuf `:4318` |
| **Token accuracy** | Prefer provider `gen_ai.usage.*` totals; tokenizer only *splits* totals into categories; label splits "estimated" |
| **Tool calls** | Captured & stored now; only `success` / `execution_error` status (smart classification deferred to v0.2) |
| **Quality bar** | Prototype — end-to-end working, minimal tests. Exception: token categorizer gets a real unit-test suite (it's the novel core). |
| **Name** | `agentxray` (short binary alias `axray` acceptable) |

### Out of scope for MVP
- Local web dashboard (v0.3)
- Failure taxonomy: `schema_error` / `timeout` / `suspected_misuse` (v0.2)
- Worst-offenders ranked view (v0.2)
- Mode B manual SDKs (Go/Python) (v0.2)
- DuckDB
- Packaging/distribution (`go install`, Homebrew, prebuilt binaries) (v0.3+)

### Config knobs (minimal — only what the MVP needs)
- OTLP gRPC + HTTP listen addresses/ports
- Content-capture toggle (whether tool args/results are stored)
- Tokenizer model/encoding used for category splitting
- DB file path

---

## Phased Plan

### Phase 0 — Project skeleton & scaffolding
- `go mod init github.com/pwn1609/AgentXRay`
- Layout: `/cmd/agentxray`, `/internal/{ingest,tokenize,classify,store,model}`, `/config`, `/testdata`
- Wire cobra CLI with stub commands (`serve`, `runs list`, `runs show`, `config validate`)
- Add `CLAUDE.md` with Go conventions + project rules
- **Exit:** `go build` produces a binary; `agentxray --help` lists commands

### Phase 1 — Data model & storage layer
- Go structs: `Run`, `LLMCall`, `TokenBreakdown`, `ToolCall` (SPEC §4.1)
- SQLite store (`modernc.org/sqlite`): embedded migrations for the 3 tables + indexes (SPEC §4.2)
- CRUD/upsert helpers + query functions (`ListRuns`, `GetRun` with joined LLM/tool calls)
- **Exit:** unit test round-trips a Run with nested calls through SQLite

### Phase 2 — Token categorization engine (novel core)
- Pure function `Categorize(payload) → TokenBreakdown` in `/internal/tokenize`, transport-independent
- Integrate `pkoukk/tiktoken-go`; isolate & tokenize each segment (tool defs, system, conversation, tool outputs); take `output_tokens` from provider
- "Prefer provider total, split with tokenizer, flag estimated" logic + drift sanity-check (warn, don't crash) (SPEC §6)
- **Unit tests** across OpenAI + Anthropic-shaped payloads
- **Exit:** `go test ./internal/tokenize/...` green; breakdown sums ≈ provider totals within tolerance

### Phase 3 — Ingestion layer (OTLP receiver + adapter)
- OTLP receiver on gRPC `:4317` + HTTP `:4318`
- **Adapter/attribute-map** in one file (SPEC §5.1) — all `gen_ai.*` names centralized so a spec rename is a one-file change
- Span router by `gen_ai.operation.name` → `invoke_agent` / `chat` / `execute_tool`
- Span→Run aggregator; feed `chat` payloads into the categorizer; persist to SQLite
- **Exit:** `agentxray serve` accepts spans and writes runs to the DB

### Phase 4 — Fixtures & end-to-end wiring
- Hand-crafted OTLP span fixtures (OpenAI + Anthropic shapes) in `/testdata` + a small replay helper (`make seed`) that posts them to the receiver
- One integration test: replay fixtures → assert rows land with sane breakdown
- **Exit / go-no-go gate (SPEC §12):** replay a realistic session → `runs show` prints a sane token breakdown

### Phase 5 — CLI presentation & polish
- `runs list` (recent runs: id, agent, tokens, status, time)
- `runs show <id>` (token category breakdown + LLM calls + tool calls, with "estimated" labels)
- `config validate` reads a YAML config (ports, content-capture, tokenizer, db path)
- Terminal-nice tables (README-GIF-worthy, SPEC §8)
- Short README (quickstart: `serve` + replay + `runs show`)
- **Exit:** full loop demoable from a clean checkout

---

## Sequencing & Risk
- Phases 0 → 1 → 2 can begin immediately.
- **Phase 2 is highest-risk/highest-value** (the token-split math) → gets the most care and the only real test suite.
- **Phase 3 (OTLP plumbing)** is the second risk area (pre-stable `gen_ai.*` conventions → mitigated by the centralized adapter).
- Phases 4–5 are integration + UX.

## Deferred (post-MVP, per SPEC milestones)
- **v0.2:** failure classification engine + `tools stats` worst-offenders; Mode B Go SDK
- **v0.3:** local web dashboard; README GIF; distribution
- **v1.0:** launch (Show HN / r/LocalLLaMA)
