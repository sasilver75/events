package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// StatusSnapshot is a compact operator-facing view of scheduler state.
// It is intentionally derived from the authoritative in-memory state instead
// of becoming another source of truth.
type StatusSnapshot struct {
	GeneratedAt         time.Time         `json:"generated_at"`
	PollIntervalMs      int               `json:"poll_interval_ms"`
	MaxConcurrentAgents int               `json:"max_concurrent_agents"`
	AgentRunner         string            `json:"agent_runner"`
	LinearAccess        string            `json:"linear_access"`
	Running             []StatusRun       `json:"running"`
	Retrying            []StatusRetry     `json:"retrying"`
	NeedsHuman          []StatusHuman     `json:"needs_human"`
	RecentRuns          []StatusRunResult `json:"recent_runs"`
	ClaimedCount        int               `json:"claimed_count"`
	CompletedCount      int               `json:"completed_count"`
	WorkflowPath        string            `json:"workflow_path,omitempty"`
	WorkflowModTime     *time.Time        `json:"workflow_mod_time,omitempty"`
	CodexTotals         StatusTokenInfo   `json:"codex_totals"`
	CodexRateLimits     any               `json:"codex_rate_limits,omitempty"`
}

type StatusRun struct {
	IssueID    string    `json:"issue_id"`
	Identifier string    `json:"identifier"`
	State      string    `json:"state"`
	Attempt    *int      `json:"attempt,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	SessionID  string    `json:"session_id,omitempty"`
	DurationMs int64     `json:"duration_ms"`
}

type StatusRetry struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	Attempt    int    `json:"attempt"`
	DueAtMs    int64  `json:"due_at_ms"`
	Error      string `json:"error,omitempty"`
}

type StatusHuman struct {
	IssueID         string     `json:"issue_id"`
	Identifier      string     `json:"identifier"`
	Reason          string     `json:"reason"`
	Attempts        int        `json:"attempts"`
	Since           time.Time  `json:"since"`
	EscalatedAt     *time.Time `json:"escalated_at,omitempty"`
	EscalationError string     `json:"escalation_error,omitempty"`
}

type StatusRunResult struct {
	IssueID    string          `json:"issue_id"`
	Identifier string          `json:"identifier"`
	Attempt    *int            `json:"attempt,omitempty"`
	SessionID  string          `json:"session_id,omitempty"`
	ThreadID   string          `json:"thread_id,omitempty"`
	TurnID     string          `json:"turn_id,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	DurationMs int64           `json:"duration_ms"`
	TokenInfo  StatusTokenInfo `json:"token_info"`
	RateLimits any             `json:"rate_limits,omitempty"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
}

type StatusTokenInfo struct {
	InputTokens    int   `json:"input_tokens"`
	OutputTokens   int   `json:"output_tokens"`
	TotalTokens    int   `json:"total_tokens"`
	SecondsRunning int64 `json:"seconds_running"`
}

func (o *Orchestrator) StatusSnapshot(now time.Time) StatusSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()

	running := make([]StatusRun, 0, len(o.state.Running))
	for id, entry := range o.state.Running {
		running = append(running, StatusRun{
			IssueID:    id,
			Identifier: entry.Issue.Identifier,
			State:      entry.Issue.State,
			Attempt:    entry.Attempt,
			StartedAt:  entry.StartedAt,
			SessionID:  entry.Session.SessionID,
			DurationMs: now.Sub(entry.StartedAt).Milliseconds(),
		})
	}

	retrying := make([]StatusRetry, 0, len(o.state.RetryAttempts))
	for id, entry := range o.state.RetryAttempts {
		retrying = append(retrying, StatusRetry{
			IssueID:    id,
			Identifier: entry.Identifier,
			Attempt:    entry.Attempt,
			DueAtMs:    entry.DueAtMs,
			Error:      entry.Error,
		})
	}

	needsHuman := make([]StatusHuman, 0, len(o.state.NeedsHuman))
	for id, entry := range o.state.NeedsHuman {
		var escalatedAt *time.Time
		if !entry.EscalatedAt.IsZero() {
			t := entry.EscalatedAt
			escalatedAt = &t
		}
		needsHuman = append(needsHuman, StatusHuman{
			IssueID:         id,
			Identifier:      entry.Identifier,
			Reason:          entry.Reason,
			Attempts:        entry.Attempts,
			Since:           entry.Since,
			EscalatedAt:     escalatedAt,
			EscalationError: entry.EscalationError,
		})
	}

	var workflowModTime *time.Time
	if !o.workflowModTime.IsZero() {
		t := o.workflowModTime
		workflowModTime = &t
	}
	recentRuns := make([]StatusRunResult, 0, len(o.state.RecentRuns))
	for _, run := range o.state.RecentRuns {
		durationMs := int64(0)
		if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
			durationMs = run.FinishedAt.Sub(run.StartedAt).Milliseconds()
			if durationMs < 0 {
				durationMs = 0
			}
		}
		recentRuns = append(recentRuns, StatusRunResult{
			IssueID:    run.IssueID,
			Identifier: run.IssueIdentifier,
			Attempt:    run.Attempt,
			SessionID:  run.SessionID,
			ThreadID:   run.ThreadID,
			TurnID:     run.TurnID,
			StartedAt:  run.StartedAt,
			FinishedAt: run.FinishedAt,
			DurationMs: durationMs,
			TokenInfo: StatusTokenInfo{
				InputTokens:  run.InputTokens,
				OutputTokens: run.OutputTokens,
				TotalTokens:  run.TotalTokens,
			},
			RateLimits: run.RateLimits,
			Status:     string(run.Status),
			Error:      run.Error,
		})
	}

	return StatusSnapshot{
		GeneratedAt:         now,
		PollIntervalMs:      o.state.PollIntervalMs,
		MaxConcurrentAgents: o.state.MaxConcurrentAgents,
		AgentRunner:         o.Config.AgentRunnerName(),
		LinearAccess:        o.Config.LinearAccessMode(),
		Running:             running,
		Retrying:            retrying,
		NeedsHuman:          needsHuman,
		RecentRuns:          recentRuns,
		ClaimedCount:        len(o.state.Claimed),
		CompletedCount:      len(o.state.Completed),
		WorkflowPath:        o.workflowPath,
		WorkflowModTime:     workflowModTime,
		CodexTotals: StatusTokenInfo{
			InputTokens:    o.state.CodexTotals.InputTokens,
			OutputTokens:   o.state.CodexTotals.OutputTokens,
			TotalTokens:    o.state.CodexTotals.TotalTokens,
			SecondsRunning: o.state.CodexTotals.SecondsRunning,
		},
		CodexRateLimits: o.state.CodexRateLimits,
	}
}

func (o *Orchestrator) writeStatusSnapshot(ctx context.Context) error {
	if o.StatusFile == "" {
		return nil
	}
	if err := writeJSONAtomic(ctx, o.StatusFile, o.StatusSnapshot(time.Now().UTC())); err != nil {
		o.Logger.Warn("status snapshot write failed", "path", o.StatusFile, "err", err)
		return err
	}
	return nil
}

func writeJSONAtomic(ctx context.Context, path string, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
