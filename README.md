# AgentXRay

**Where are your agent's tokens actually going?**

AgentXRay is a single-binary, zero-dependency tool that ingests agent run
telemetry (OpenTelemetry / OTLP) and answers two questions existing observability
platforms bury:

1. **Token breakdown by category** — not "this run cost $0.14", but how much went to
   **tool definitions** (schemas re-sent every turn), system prompt, conversation
   history, tool outputs, and actual model output.
2. **Tool-call statistics** — which tools ran, how long they took, and which failed.

It speaks standard OTLP on the standard ports, so it's a drop-in
`OTEL_EXPORTER_OTLP_ENDPOINT` target. Everything runs in one process backed by an
embedded SQLite database — no Postgres, Redis, or Kafka.

> **Status: v0.1 (MVP).** OTLP ingest → token categorization → SQLite → CLI.
> See [`PLAN.md`](PLAN.md) for the roadmap and [`SPEC.md`](SPEC.md) for the full spec.

## Install / build

```sh
go build -o agentxray ./cmd/agentxray
```

No CGO, cross-compilable, single binary.

## Quickstart

Start the receiver (OTLP gRPC on `:4317`, HTTP on `:4318`):

```sh
agentxray serve --db agentxray.db
```

Point any OTel-emitting agent at it:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
```

…or seed it from the bundled fixtures to see it work immediately:

```sh
agentxray replay testdata/openai_run.json testdata/anthropic_run.json
```

List recent runs:

```sh
agentxray runs list
```

```
RUN ID        AGENT               STATUS   TOKENS  STARTED
616e7468726f  research-assistant  error    930     2023-11-14 15:15:00
6f70656e6169  trip-planner        success  2840    2023-11-14 15:13:20
```

Drill into one run (run-id prefixes work):

```sh
agentxray runs show 6f70656e
```

```
Run 6f70656e61692d74726163652d303031  (~ = estimated split)
  agent:    trip-planner
  status:   success
  duration: 3.50s
  llm calls: 2    tool calls: 1

TOKEN BREAKDOWN
  system prompt        372  13.1% ███·················
  tool definitions    1731  61.0% ████████████········
  conversation         314  11.1% ██··················
  tool outputs         183   6.4% █···················
  output               240   8.5% ██··················
  TOTAL               2840

LLM CALLS
  #  MODEL   SYSTEM  TOOLDEFS  CONV  TOOLOUT  OUTPUT  TOTAL  FINISH
  1  gpt-4o  189     880       131   0        150     1350   tool_calls
  2  gpt-4o  183     851       183   183      90      1490   stop

TOOL CALLS
  TOOL         STATUS   DURATION  DETAIL
  get_weather  success  60ms      -
```

Here **tool definitions are 61% of the tokens** — the exact "schemas re-sent every
turn" cost that's otherwise invisible.

## How token categorization works

Providers report a single input-token total, not a breakdown. AgentXRay tokenizes
each request segment (tool schemas, system prompt, conversation, tool outputs) to
find their **proportions**, then reconciles those proportions to the authoritative
`gen_ai.usage.*` total. Because the per-category split is derived from our own
tokenizer, such splits are labeled **estimated** (`~`). Counts are exact for
OpenAI-family models and approximate for others.

The tokenizer vocab is embedded in the binary (offline) — no network calls.

## Commands

| Command | Description |
|---|---|
| `agentxray serve` | Start the OTLP receiver (gRPC `:4317` + HTTP `:4318`) and persist runs |
| `agentxray runs list` | List recent runs with total tokens and status |
| `agentxray runs show <id>` | Full token & tool breakdown for one run (prefix ok) |
| `agentxray replay <file.json>…` | Replay OTLP/JSON fixtures into a running receiver |
| `agentxray config validate` | Validate a config file |

Key `serve` flags: `--grpc`, `--http`, `--db`, `--encoding`, `--capture-content`.

## Configuration

See [`config/agentxray.example.yaml`](config/agentxray.example.yaml). Validate with:

```sh
agentxray config validate --config config/agentxray.example.yaml
```

## Development

```sh
go test ./...                    # full suite
go test ./internal/tokenize/...  # the token categorization engine
go vet ./... && gofmt -l .
```

## Scope

v0.1 is intentionally CLI-only and focused on token-breakdown + tool-call
visibility. Failure classification & "worst offenders", a web dashboard, and Mode B
SDKs are planned for later milestones — see [`PLAN.md`](PLAN.md).
