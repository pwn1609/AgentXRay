package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pwn1609/AgentXRay/internal/model"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

//go:embed schema.sql
var schemaSQL string

// Store is the embedded SQLite storage layer.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the schema.
// Use ":memory:" for an in-memory database (tests).
func Open(path string) (*Store, error) {
	dsn := path
	if path == ":memory:" {
		// Shared cache keeps the in-memory DB alive across pooled connections.
		dsn = "file::memory:?cache=shared"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// SaveRun upserts a run and all of its LLM and tool calls in a single transaction.
func (s *Store) SaveRun(ctx context.Context, r *model.Run) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	meta := "{}"
	if len(r.Metadata) > 0 {
		b, err := json.Marshal(r.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
		meta = string(b)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO runs (id, agent_name, started_at, ended_at, status, total_tokens, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent_name=excluded.agent_name, started_at=excluded.started_at,
			ended_at=excluded.ended_at, status=excluded.status,
			total_tokens=excluded.total_tokens, metadata=excluded.metadata`,
		r.ID, r.AgentName, millis(r.StartedAt), millis(r.EndedAt), r.Status,
		r.TotalTokens.Total, meta,
	); err != nil {
		return fmt.Errorf("upsert run %s: %w", r.ID, err)
	}

	for i := range r.LLMCalls {
		c := &r.LLMCalls[i]
		tb := c.TokenBreakdown
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO llm_calls (id, run_id, parent_span_id, model, started_at, duration_ms,
				finish_reason, system_prompt_tokens, tool_definition_tokens, conversation_tokens,
				tool_output_tokens, output_tokens, total_tokens, estimated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				run_id=excluded.run_id, parent_span_id=excluded.parent_span_id, model=excluded.model,
				started_at=excluded.started_at, duration_ms=excluded.duration_ms,
				finish_reason=excluded.finish_reason, system_prompt_tokens=excluded.system_prompt_tokens,
				tool_definition_tokens=excluded.tool_definition_tokens, conversation_tokens=excluded.conversation_tokens,
				tool_output_tokens=excluded.tool_output_tokens, output_tokens=excluded.output_tokens,
				total_tokens=excluded.total_tokens, estimated=excluded.estimated`,
			c.ID, r.ID, c.ParentSpanID, c.Model, millis(c.StartedAt), c.DurationMs,
			c.FinishReason, tb.SystemPromptTokens, tb.ToolDefinitionTokens, tb.ConversationTokens,
			tb.ToolOutputTokens, tb.OutputTokens, tb.Total, boolToInt(tb.Estimated),
		); err != nil {
			return fmt.Errorf("upsert llm_call %s: %w", c.ID, err)
		}
	}

	for i := range r.ToolCalls {
		c := &r.ToolCalls[i]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tool_calls (id, run_id, llm_call_id, tool_name, arguments, result,
				started_at, duration_ms, status, failure_detail)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				run_id=excluded.run_id, llm_call_id=excluded.llm_call_id, tool_name=excluded.tool_name,
				arguments=excluded.arguments, result=excluded.result, started_at=excluded.started_at,
				duration_ms=excluded.duration_ms, status=excluded.status, failure_detail=excluded.failure_detail`,
			c.ID, r.ID, nullStr(c.LLMCallID), c.ToolName, rawOrNil(c.Arguments), rawOrNil(c.Result),
			millis(c.StartedAt), c.DurationMs, c.Status, c.FailureDetail,
		); err != nil {
			return fmt.Errorf("upsert tool_call %s: %w", c.ID, err)
		}
	}

	// Make the stored run-level total authoritative: recompute it from all LLM
	// calls now attached to the run. This keeps `runs list` correct even when a
	// trace's spans arrive across multiple export batches. Runs with no LLM calls
	// (tool-only) keep the total provided above.
	if _, err := tx.ExecContext(ctx, `
		UPDATE runs SET total_tokens = (
			SELECT COALESCE(SUM(total_tokens), 0) FROM llm_calls WHERE run_id = ?
		) WHERE id = ? AND EXISTS (SELECT 1 FROM llm_calls WHERE run_id = ?)`,
		r.ID, r.ID, r.ID,
	); err != nil {
		return fmt.Errorf("recompute run total %s: %w", r.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListRuns returns up to limit runs ordered by most recent first. Child calls are
// not populated (use GetRun for the full breakdown).
func (s *Store) ListRuns(ctx context.Context, limit int) ([]model.Run, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_name, started_at, ended_at, status, total_tokens, metadata
		FROM runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()

	var out []model.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun returns a single run with its LLM calls and tool calls populated. The
// run-level TotalTokens breakdown is reconstructed by summing the LLM calls.
// Returns (nil, nil) if no run with the given id exists.
func (s *Store) GetRun(ctx context.Context, id string) (*model.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, agent_name, started_at, ended_at, status, total_tokens, metadata
		FROM runs WHERE id = ?`, id)
	r, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if r.LLMCalls, err = s.llmCallsForRun(ctx, id); err != nil {
		return nil, err
	}
	if r.ToolCalls, err = s.toolCallsForRun(ctx, id); err != nil {
		return nil, err
	}

	var sum model.TokenBreakdown
	for _, c := range r.LLMCalls {
		sum = sum.Add(c.TokenBreakdown)
	}
	r.TotalTokens = sum
	return &r, nil
}

func (s *Store) llmCallsForRun(ctx context.Context, runID string) ([]model.LLMCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, parent_span_id, model, started_at, duration_ms, finish_reason,
			system_prompt_tokens, tool_definition_tokens, conversation_tokens,
			tool_output_tokens, output_tokens, total_tokens, estimated
		FROM llm_calls WHERE run_id = ? ORDER BY started_at ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("query llm_calls: %w", err)
	}
	defer rows.Close()

	var out []model.LLMCall
	for rows.Next() {
		var c model.LLMCall
		var startedAt int64
		var est int
		if err := rows.Scan(&c.ID, &c.RunID, &c.ParentSpanID, &c.Model, &startedAt, &c.DurationMs,
			&c.FinishReason, &c.TokenBreakdown.SystemPromptTokens, &c.TokenBreakdown.ToolDefinitionTokens,
			&c.TokenBreakdown.ConversationTokens, &c.TokenBreakdown.ToolOutputTokens,
			&c.TokenBreakdown.OutputTokens, &c.TokenBreakdown.Total, &est); err != nil {
			return nil, fmt.Errorf("scan llm_call: %w", err)
		}
		c.StartedAt = fromMillis(startedAt)
		c.TokenBreakdown.Estimated = est != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) toolCallsForRun(ctx context.Context, runID string) ([]model.ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, llm_call_id, tool_name, arguments, result, started_at,
			duration_ms, status, failure_detail
		FROM tool_calls WHERE run_id = ? ORDER BY started_at ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("query tool_calls: %w", err)
	}
	defer rows.Close()

	var out []model.ToolCall
	for rows.Next() {
		var c model.ToolCall
		var startedAt int64
		var args, result, llmCallID sql.NullString
		if err := rows.Scan(&c.ID, &c.RunID, &llmCallID, &c.ToolName, &args, &result,
			&startedAt, &c.DurationMs, &c.Status, &c.FailureDetail); err != nil {
			return nil, fmt.Errorf("scan tool_call: %w", err)
		}
		c.LLMCallID = llmCallID.String
		c.StartedAt = fromMillis(startedAt)
		if args.Valid {
			c.Arguments = json.RawMessage(args.String)
		}
		if result.Valid {
			c.Result = json.RawMessage(result.String)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanner abstracts *sql.Row and *sql.Rows for shared run scanning.
type scanner interface {
	Scan(dest ...any) error
}

func scanRun(sc scanner) (model.Run, error) {
	var r model.Run
	var startedAt, endedAt int64
	var total int
	var meta sql.NullString
	if err := sc.Scan(&r.ID, &r.AgentName, &startedAt, &endedAt, &r.Status, &total, &meta); err != nil {
		return r, err
	}
	r.StartedAt = fromMillis(startedAt)
	r.EndedAt = fromMillis(endedAt)
	r.TotalTokens = model.TokenBreakdown{Total: total}
	if meta.Valid && meta.String != "" && meta.String != "{}" {
		if err := json.Unmarshal([]byte(meta.String), &r.Metadata); err != nil {
			return r, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	return r, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func rawOrNil(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	return string(r)
}

// nullStr binds empty strings as SQL NULL (used for optional foreign keys).
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
