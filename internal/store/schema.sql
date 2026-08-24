-- AgentXRay storage schema (v0.1). Timestamps are Unix milliseconds.

CREATE TABLE IF NOT EXISTS runs (
    id           TEXT PRIMARY KEY,
    agent_name   TEXT,
    started_at   INTEGER,
    ended_at     INTEGER,
    status       TEXT,
    total_tokens INTEGER,
    metadata     TEXT -- JSON object
);

CREATE TABLE IF NOT EXISTS llm_calls (
    id                     TEXT PRIMARY KEY,
    run_id                 TEXT REFERENCES runs(id),
    parent_span_id         TEXT,
    model                  TEXT,
    started_at             INTEGER,
    duration_ms            INTEGER,
    finish_reason          TEXT,
    system_prompt_tokens   INTEGER,
    tool_definition_tokens INTEGER,
    conversation_tokens    INTEGER,
    tool_output_tokens     INTEGER,
    output_tokens          INTEGER,
    total_tokens           INTEGER,
    estimated              INTEGER -- 0/1
);

CREATE TABLE IF NOT EXISTS tool_calls (
    id             TEXT PRIMARY KEY,
    run_id         TEXT REFERENCES runs(id),
    -- No FK on llm_call_id: a tool call's triggering span may be a non-chat span
    -- (e.g. the agent span) or may arrive out of order, so it is not guaranteed
    -- to reference a recorded llm_calls row. Stored NULL when unknown.
    llm_call_id    TEXT,
    tool_name      TEXT,
    arguments      TEXT, -- JSON
    result         TEXT, -- JSON
    started_at     INTEGER,
    duration_ms    INTEGER,
    status         TEXT,
    failure_detail TEXT
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_name ON tool_calls(tool_name);
CREATE INDEX IF NOT EXISTS idx_tool_calls_status ON tool_calls(status);
CREATE INDEX IF NOT EXISTS idx_tool_calls_run_id ON tool_calls(run_id);
CREATE INDEX IF NOT EXISTS idx_llm_calls_run_id ON llm_calls(run_id);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);
