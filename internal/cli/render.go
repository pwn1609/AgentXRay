package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pwn1609/AgentXRay/internal/model"
)

// shortID truncates a trace/span id for compact display.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func fmtDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return "-"
	}
	d := end.Sub(start)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// renderRunsList prints a table of runs (most recent first).
func renderRunsList(w io.Writer, runs []model.Run) {
	if len(runs) == 0 {
		fmt.Fprintln(w, "No runs yet. Start the receiver with `agentxray serve` and send some traffic.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tAGENT\tSTATUS\tTOKENS\tSTARTED")
	for _, r := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			shortID(r.ID), dash(r.AgentName), statusLabel(r.Status),
			r.TotalTokens.Total, fmtTime(r.StartedAt))
	}
	tw.Flush()
}

// renderRunShow prints the full breakdown for a single run.
func renderRunShow(w io.Writer, r *model.Run) {
	est := ""
	if r.TotalTokens.Estimated {
		est = "  (~ = estimated split)"
	}
	fmt.Fprintf(w, "Run %s%s\n", r.ID, est)
	fmt.Fprintf(w, "  agent:    %s\n", dash(r.AgentName))
	fmt.Fprintf(w, "  status:   %s\n", statusLabel(r.Status))
	fmt.Fprintf(w, "  started:  %s\n", fmtTime(r.StartedAt))
	fmt.Fprintf(w, "  duration: %s\n", fmtDuration(r.StartedAt, r.EndedAt))
	fmt.Fprintf(w, "  llm calls: %d    tool calls: %d\n", len(r.LLMCalls), len(r.ToolCalls))

	fmt.Fprintln(w, "\nTOKEN BREAKDOWN")
	renderBreakdown(w, r.TotalTokens)

	if len(r.LLMCalls) > 0 {
		fmt.Fprintln(w, "\nLLM CALLS")
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "  #\tMODEL\tSYSTEM\tTOOLDEFS\tCONV\tTOOLOUT\tOUTPUT\tTOTAL\tFINISH")
		for i, c := range r.LLMCalls {
			b := c.TokenBreakdown
			fmt.Fprintf(tw, "  %d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
				i+1, dash(c.Model), b.SystemPromptTokens, b.ToolDefinitionTokens,
				b.ConversationTokens, b.ToolOutputTokens, b.OutputTokens, b.Total, dash(c.FinishReason))
		}
		tw.Flush()
	}

	if len(r.ToolCalls) > 0 {
		fmt.Fprintln(w, "\nTOOL CALLS")
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "  TOOL\tSTATUS\tDURATION\tDETAIL")
		for _, tc := range r.ToolCalls {
			fmt.Fprintf(tw, "  %s\t%s\t%dms\t%s\n",
				dash(tc.ToolName), toolStatusLabel(tc.Status), tc.DurationMs, dash(tc.FailureDetail))
		}
		tw.Flush()
	}
}

// renderBreakdown prints each token category with its share and a simple bar.
func renderBreakdown(w io.Writer, b model.TokenBreakdown) {
	type row struct {
		label string
		val   int
	}
	rows := []row{
		{"system prompt", b.SystemPromptTokens},
		{"tool definitions", b.ToolDefinitionTokens},
		{"conversation", b.ConversationTokens},
		{"tool outputs", b.ToolOutputTokens},
		{"output", b.OutputTokens},
	}
	total := b.Total
	tw := tabwriter.NewWriter(w, 0, 2, 1, ' ', 0)
	for _, r := range rows {
		pct := 0.0
		if total > 0 {
			pct = float64(r.val) / float64(total) * 100
		}
		fmt.Fprintf(tw, "  %-16s\t%7d\t%5.1f%%\t%s\n", r.label, r.val, pct, bar(pct))
	}
	fmt.Fprintf(tw, "  %-16s\t%7d\t%5s\t\n", "TOTAL", total, "")
	tw.Flush()
}

// bar renders a 20-cell proportion bar.
func bar(pct float64) string {
	const width = 20
	n := int(pct/100*width + 0.5)
	if n > width {
		n = width
	}
	return strings.Repeat("█", n) + strings.Repeat("·", width-n)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func statusLabel(s string) string {
	switch s {
	case model.RunStatusSuccess:
		return "success"
	case model.RunStatusError:
		return "error"
	case model.RunStatusInProgress:
		return "in_progress"
	default:
		return dash(s)
	}
}

func toolStatusLabel(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
