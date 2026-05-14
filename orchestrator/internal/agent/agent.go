// Package agent defines the runner contract shared by concrete coding-agent
// integrations. Symphony's reference runtime is Codex app-server; Spur's
// current production runtime is Claude Code headless.
package agent

import (
	"context"
	"encoding/json"
	"time"
)

// EventType matches Symphony spec §10.4's important emitted events.
type EventType string

const (
	EventSessionStarted EventType = "session_started"
	EventStartupFailed  EventType = "startup_failed"
	EventTurnCompleted  EventType = "turn_completed"
	EventTurnFailed     EventType = "turn_failed"
	EventTurnCancelled  EventType = "turn_cancelled"
	EventTurnTimedOut   EventType = "turn_timed_out"
	EventTurnStalled    EventType = "turn_stalled"
	EventRateLimits     EventType = "rate_limits_updated"
	EventOtherMessage   EventType = "other_message"
	EventMalformed      EventType = "malformed"
)

// Event is the normalized form the orchestrator consumes. Symphony §10.4.
type Event struct {
	Type       EventType
	Timestamp  time.Time
	SessionID  string
	ThreadID   string
	TurnID     string
	Raw        json.RawMessage
	Usage      Usage
	RateLimits *RateLimitSnapshot
	Error      string
}

// Usage captures per-event token counters. Symphony §13.5 says the
// orchestrator should prefer absolute thread totals over deltas when present.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// RateLimitSnapshot mirrors Codex app-server's account/rateLimits/updated
// payload. All fields are optional because app-server can omit buckets that do
// not apply to a given account or plan.
type RateLimitSnapshot struct {
	LimitID              string           `json:"limit_id,omitempty"`
	LimitName            string           `json:"limit_name,omitempty"`
	PlanType             string           `json:"plan_type,omitempty"`
	RateLimitReachedType string           `json:"rate_limit_reached_type,omitempty"`
	Primary              *RateLimitWindow `json:"primary,omitempty"`
	Secondary            *RateLimitWindow `json:"secondary,omitempty"`
	Credits              *CreditsSnapshot `json:"credits,omitempty"`
}

type RateLimitWindow struct {
	UsedPercent        int    `json:"used_percent"`
	ResetsAt           *int64 `json:"resets_at,omitempty"`
	WindowDurationMins *int64 `json:"window_duration_mins,omitempty"`
}

type CreditsSnapshot struct {
	Balance    string `json:"balance,omitempty"`
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
}

// DynamicTool is a host-side tool advertised to Codex app-server. It lets the
// orchestrator keep privileged credentials on the host while the in-workspace
// agent sees only a constrained tool call surface.
type DynamicTool struct {
	Name        string
	Namespace   string
	Description string
	InputSchema json.RawMessage
	Handle      DynamicToolHandler
}

type DynamicToolCall struct {
	ThreadID  string
	TurnID    string
	CallID    string
	Tool      string
	Namespace string
	Arguments json.RawMessage
}

type DynamicToolResult struct {
	Success bool
	Text    string
}

type DynamicToolHandler func(context.Context, DynamicToolCall) (DynamicToolResult, error)

// RunResult summarizes how a turn ended.
type RunResult struct {
	Type       EventType
	SessionID  string // resumable handle for the concrete runner
	ThreadID   string
	TurnID     string
	Error      string
	Usage      Usage
	RateLimits *RateLimitSnapshot
	Duration   time.Duration
}

// TurnConfig is the per-turn runtime overlay supplied by WORKFLOW.md config
// and host-held credentials. Concrete runners may ignore fields they do not
// support, but should never mutate the supplied map.
type TurnConfig struct {
	Command      string
	TurnTimeout  time.Duration
	StallTimeout time.Duration
	Env          map[string]string
	DynamicTools []DynamicTool
}

// Runner launches one coding-agent turn inside the issue workspace.
type Runner interface {
	Run(ctx context.Context, vmIP, prompt, resume string, onEvent func(Event)) (RunResult, error)
}

// ConfigurableRunner can produce a per-turn copy with WORKFLOW.md settings
// and secrets applied. This keeps shared runner instances immutable while
// still allowing config reloads to affect newly started turns.
type ConfigurableRunner interface {
	Runner
	WithTurnConfig(TurnConfig) Runner
}
