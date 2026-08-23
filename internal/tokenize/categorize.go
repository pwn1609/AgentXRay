package tokenize

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/pwn1609/AgentXRay/internal/model"
)

// driftWarnPct is the relative gap between our tokenizer's raw input estimate and
// the provider-reported input total above which we emit a (non-fatal) warning.
const driftWarnPct = 0.15

// Role identifies the kind of message in an LLM request payload.
type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // a tool result fed back into the model
)

// ToolDef is one tool/function schema made available to the model this call.
type ToolDef struct {
	Name string          `json:"name"`
	Raw  json.RawMessage `json:"raw"` // serialized schema as sent to the provider
}

// Message is one message in the request (system/user/assistant/tool).
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Payload is one LLM call's request plus provider-reported usage totals. It is a
// transport-independent view: the ingest layer maps OTLP spans into this shape,
// and tests construct it directly. Categorize consumes it as a pure function.
type Payload struct {
	Model    string    `json:"model"`
	System   string    `json:"system,omitempty"` // convenience: an extra system prompt
	Tools    []ToolDef `json:"tools,omitempty"`
	Messages []Message `json:"messages,omitempty"`

	// Provider-reported authoritative totals (0 when absent from the span).
	ProviderInputTokens  int `json:"provider_input_tokens,omitempty"`
	ProviderOutputTokens int `json:"provider_output_tokens,omitempty"`

	// ResponseText is used to estimate output tokens only when the provider did
	// not report an output total.
	ResponseText string `json:"response_text,omitempty"`
}

// Diagnostics carries non-fatal observations from categorization. The engine
// never logs or panics; callers decide what to do with warnings.
type Diagnostics struct {
	Warnings []string
}

// Categorize splits one LLM call's payload into a categorized TokenBreakdown.
//
// Strategy (SPEC §6):
//   - Tokenize each segment (tool defs, system, conversation, tool outputs) to get
//     relative proportions.
//   - Prefer the provider-reported input total when present: reconcile the raw
//     per-category estimates to sum exactly to it (largest-remainder apportionment).
//   - Output tokens come straight from the provider when available, else from
//     tokenizing the response text.
//   - Any per-category input split is inherently estimated (providers report a
//     single input total, not the breakdown), so Estimated is set whenever there
//     is input content to split.
func Categorize(p Payload, c *Counter) (model.TokenBreakdown, Diagnostics) {
	var diag Diagnostics

	sys := c.Count(p.System)
	toolDef := 0
	for _, t := range p.Tools {
		toolDef += c.Count(t.Name) + c.Count(string(t.Raw))
	}
	conv, toolOut := 0, 0
	for _, m := range p.Messages {
		n := c.Count(m.Content)
		switch m.Role {
		case RoleSystem, RoleDeveloper:
			sys += n
		case RoleTool:
			toolOut += n
		default: // user, assistant, or unknown => conversation
			conv += n
		}
	}

	rawInput := sys + toolDef + conv + toolOut

	// Output tokens: provider is authoritative; fall back to tokenizing response.
	output := p.ProviderOutputTokens
	if output == 0 && p.ResponseText != "" {
		output = c.Count(p.ResponseText)
	}

	br := model.TokenBreakdown{
		SystemPromptTokens:   sys,
		ToolDefinitionTokens: toolDef,
		ConversationTokens:   conv,
		ToolOutputTokens:     toolOut,
		OutputTokens:         output,
	}

	switch {
	case p.ProviderInputTokens > 0 && rawInput > 0:
		// Reconcile our proportional estimate to the authoritative total.
		scaled := apportion(
			[]int{sys, toolDef, conv, toolOut},
			p.ProviderInputTokens,
		)
		br.SystemPromptTokens = scaled[0]
		br.ToolDefinitionTokens = scaled[1]
		br.ConversationTokens = scaled[2]
		br.ToolOutputTokens = scaled[3]
		br.Estimated = true

		if drift := relDrift(rawInput, p.ProviderInputTokens); drift > driftWarnPct {
			diag.Warnings = append(diag.Warnings, fmt.Sprintf(
				"tokenizer input estimate %d drifts %.0f%% from provider total %d; categories scaled to provider total",
				rawInput, drift*100, p.ProviderInputTokens))
		}

	case p.ProviderInputTokens > 0 && rawInput == 0:
		// No content to split (e.g. content capture disabled). We can't attribute
		// the input to categories; record it only in the total.
		diag.Warnings = append(diag.Warnings,
			"provider reported input tokens but no request content was available to categorize")

	default:
		// No provider total: raw tokenizer counts stand, flagged estimated.
		if rawInput > 0 {
			br.Estimated = true
		}
	}

	// Total: sum of categories, but never less than the authoritative provider
	// totals (covers the no-content case where categories are all zero).
	br.Total = br.SystemPromptTokens + br.ToolDefinitionTokens +
		br.ConversationTokens + br.ToolOutputTokens + br.OutputTokens
	if providerTotal := p.ProviderInputTokens + output; providerTotal > br.Total {
		br.Total = providerTotal
	}
	return br, diag
}

func relDrift(estimate, actual int) float64 {
	if actual == 0 {
		return 0
	}
	d := float64(estimate-actual) / float64(actual)
	if d < 0 {
		d = -d
	}
	return d
}

// apportion scales values proportionally so they sum exactly to target, using the
// largest-remainder method to distribute rounding without drift. values must be
// non-negative and sum to a positive number.
func apportion(values []int, target int) []int {
	sum := 0
	for _, v := range values {
		sum += v
	}
	out := make([]int, len(values))
	if sum <= 0 || target <= 0 {
		return out
	}

	type rem struct {
		idx  int
		frac float64
	}
	assigned := 0
	rems := make([]rem, len(values))
	for i, v := range values {
		exact := float64(v) * float64(target) / float64(sum)
		floor := int(exact)
		out[i] = floor
		assigned += floor
		rems[i] = rem{idx: i, frac: exact - float64(floor)}
	}

	// Distribute the leftover to the largest fractional remainders.
	leftover := target - assigned
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for i := 0; i < leftover && i < len(rems); i++ {
		out[rems[i].idx]++
	}
	return out
}
