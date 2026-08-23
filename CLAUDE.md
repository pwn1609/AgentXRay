# CLAUDE.md — AgentXRay

Project rules and conventions for working in this repo. See `SPEC.md` for the full
product spec and `PLAN.md` for the phased MVP (v0.1) implementation plan.

## What this is
AgentXRay is a single-binary Go tool that ingests agent run telemetry (OTLP) and
surfaces (1) a **token breakdown by category** per run and (2) **tool-call statistics**.
The MVP (v0.1) is CLI-only: OTLP ingest → token categorization → SQLite → CLI.

## Golden rules
- **Protect the single-binary, zero-external-dependency property.** No Postgres, Redis,
  Kafka. Embedded SQLite only. No separate frontend build in the MVP.
- **Resist scope creep.** If a feature does not directly serve token-breakdown or
  tool-failure visibility, it does not belong in the MVP (SPEC §2).
- **Centralize `gen_ai.*` attribute names** behind one mapping table (SPEC §5.1). The
  GenAI semantic conventions are pre-stable; a rename must be a one-file change.
- **Token categorization is the novel core** (`internal/tokenize`). Keep it a pure
  function (`payload → TokenBreakdown`), transport-independent, and unit-tested.
- Prefer provider-reported `gen_ai.usage.*` totals; use our tokenizer only to *split*
  totals into categories, and label such splits "estimated".

## Tech stack
- Go (module `github.com/pwn1609/AgentXRay`), CLI via `spf13/cobra`.
- Storage: pure-Go `modernc.org/sqlite` (no CGO).
- Tokenizer: `pkoukk/tiktoken-go`.
- OTLP: OpenTelemetry Go collector/proto components.

## Layout
```
/cmd/agentxray/     main entrypoint
/internal/cli/      cobra command surface
/internal/model/    core domain types (Run, LLMCall, ToolCall, TokenBreakdown)
/internal/ingest/   OTLP receiver + gen_ai.* adapter layer
/internal/tokenize/ token categorization engine (pure, unit-tested)
/internal/classify/ tool-call failure classification (v0.1: success/execution_error only)
/internal/store/    SQLite storage + migrations + queries
/config/            example config
/testdata/          OTLP span fixtures (OpenAI/Anthropic shapes)
```

## Conventions
- Standard Go style: `gofmt`/`go vet` clean. Package-level doc comments on every package.
- Errors: wrap with `fmt.Errorf("...: %w", err)`; return errors, don't `log.Fatal` in libs.
- Keep exported surface small; prefer `internal/` packages.
- No CGO — keep builds cross-compilable and single-binary.

## Build / test / run
- Build: `go build ./cmd/agentxray`
- Test: `go test ./...` (categorizer suite: `go test ./internal/tokenize/...`)
- Vet/format: `go vet ./...` and `gofmt -l .`
- Run: `agentxray serve` (starts OTLP receiver), then `agentxray runs list` / `runs show <id>`

## Testing bar (MVP)
Prototype quality overall, EXCEPT `internal/tokenize` which gets a real unit-test suite
across OpenAI- and Anthropic-shaped payloads. Add one end-to-end fixture-replay
integration test at Phase 4.

## Out of scope for v0.1 (do not build yet)
Dashboard, full failure taxonomy (schema_error/timeout/suspected_misuse), worst-offenders
view, Mode B SDKs, DuckDB, packaging/distribution. These are v0.2+ (see PLAN.md).
