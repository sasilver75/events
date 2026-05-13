package domain

import "time"

// RunAttempt is one execution attempt for one issue. Symphony spec §4.1.5.
type RunAttempt struct {
	IssueID         string
	IssueIdentifier string

	// Attempt is nil for the first run, >=1 for retries or continuation.
	// Spec §4.1.5: "null for first run, >=1 for retries/continuation".
	Attempt *int

	WorkspacePath string
	StartedAt     time.Time
	Status        RunStatus
	Error         string
}

// RunStatus captures the terminal reason for a RunAttempt. Distinct reasons
// matter because retry logic and logs differ. Symphony spec §7.2.
type RunStatus string

const (
	RunStatusPreparingWorkspace       RunStatus = "preparing_workspace"
	RunStatusBuildingPrompt           RunStatus = "building_prompt"
	RunStatusLaunchingAgentProcess    RunStatus = "launching_agent_process"
	RunStatusInitializingSession      RunStatus = "initializing_session"
	RunStatusStreamingTurn            RunStatus = "streaming_turn"
	RunStatusFinishing                RunStatus = "finishing"
	RunStatusSucceeded                RunStatus = "succeeded"
	RunStatusFailed                   RunStatus = "failed"
	RunStatusTimedOut                 RunStatus = "timed_out"
	RunStatusStalled                  RunStatus = "stalled"
	RunStatusCanceledByReconciliation RunStatus = "canceled_by_reconciliation"
)

// LiveSession holds the state tracked while a coding-agent subprocess is
// running. Symphony spec §4.1.6.
type LiveSession struct {
	// SessionID is composed as "<ThreadID>-<TurnID>" per spec §4.2.
	SessionID string
	ThreadID  string
	TurnID    string

	AgentPID string

	LastEvent     string
	LastTimestamp time.Time
	LastMessage   string

	InputTokens  int
	OutputTokens int
	TotalTokens  int

	LastReportedInputTokens  int
	LastReportedOutputTokens int
	LastReportedTotalTokens  int

	// TurnCount is the number of coding-agent turns started within the
	// current worker lifetime. The worker may run multiple back-to-back
	// turns on the same thread before exiting (spec §7.1).
	TurnCount int
}

// RetryEntry is scheduled retry state for an issue. Symphony spec §4.1.7.
type RetryEntry struct {
	IssueID    string
	Identifier string

	// Attempt is 1-based for the retry queue.
	Attempt int

	// DueAtMs is a monotonic clock timestamp in milliseconds.
	DueAtMs int64

	// TimerHandle is the runtime-specific cancellation token.
	// In Go this can be a *time.Timer; abstracted as any to keep
	// this package free of runtime-specific imports.
	TimerHandle any

	Error string
}

// OrchestratorRuntimeState is the single authoritative in-memory state
// owned by the orchestrator. All scheduling mutations flow through one
// authority (spec §7.4) to avoid duplicate dispatch.
//
// Symphony spec §4.1.8.
type OrchestratorRuntimeState struct {
	PollIntervalMs      int
	MaxConcurrentAgents int

	Running       map[string]RunningEntry // issue_id -> running entry
	Claimed       map[string]struct{}     // issue IDs reserved/running/retrying
	RetryAttempts map[string]RetryEntry   // issue_id -> RetryEntry
	Completed     map[string]struct{}     // bookkeeping; not used for dispatch gating

	CodexTotals     CodexTotals
	CodexRateLimits any // latest rate-limit snapshot from agent events
}

// RunningEntry tracks one in-flight run. Composes the live agent session
// with the original issue snapshot so reconciliation can compare.
type RunningEntry struct {
	Issue     Issue
	Session   LiveSession
	StartedAt time.Time
	Attempt   *int
}

// CodexTotals aggregates token + runtime telemetry across runs.
// Symphony spec §4.1.8 / §13.5.
type CodexTotals struct {
	InputTokens    int
	OutputTokens   int
	TotalTokens    int
	SecondsRunning int64
}
