package claudecode

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/workspace/tart"
)

// Runner launches Claude Code headless inside a per-issue Tart VM and
// streams normalized events back to the caller.
//
// One Runner instance per process is fine — it's stateless across runs.
type Runner struct {
	Workspace *tart.Manager

	// Command is the Claude Code invocation, less the prompt. From WORKFLOW.md
	// claudecode.command. Typically:
	//   "claude --print --output-format=stream-json --permission-mode bypassPermissions"
	Command string

	// TurnTimeout bounds a single turn (spec §10.6 `turn_timeout_ms`).
	TurnTimeout time.Duration

	// StallTimeout terminates the turn if no event arrives within this
	// window (spec §10.6 `stall_timeout_ms`). Set to 0 to disable.
	StallTimeout time.Duration

	// WorkingDir inside the VM where Claude Code launches.
	// Typically "/Users/admin/events".
	WorkingDir string

	// Env are key=value pairs exported into the shell before the agent
	// invocation. Lets the orchestrator inject per-run secrets like
	// GITHUB_TOKEN and LINEAR_API_KEY without round-tripping through a
	// file on the VM filesystem.
	Env map[string]string
}

type RunResult = agent.RunResult

// Run invokes Claude Code once inside the VM at `vmIP` with `prompt` as
// the user message. Events are streamed to `onEvent` as they arrive.
// Returns when the turn reaches a terminal state.
//
// For continuation turns, pass the same SessionID into the next call's
// `resume` argument so Claude Code re-attaches to the existing thread
// (Symphony spec §7.1: "Reuse the same thread_id for all continuation
// turns inside one worker run").
func (r *Runner) Run(ctx context.Context, vmIP, prompt, resume string, onEvent func(Event)) (RunResult, error) {
	if r.TurnTimeout <= 0 {
		r.TurnTimeout = 60 * time.Minute
	}

	turnCtx, cancel := context.WithTimeout(ctx, r.TurnTimeout)
	defer cancel()

	command := r.buildCommand(prompt, resume)
	cmd := r.Workspace.SSHCmd(turnCtx, vmIP, command)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{Type: EventStartupFailed}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{Type: EventStartupFailed}, fmt.Errorf("stderr pipe: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return RunResult{Type: EventStartupFailed, Duration: time.Since(start)}, fmt.Errorf("start: %w", err)
	}

	// Stderr is not part of the protocol stream (spec §10.3). Drain it
	// into a buffer for diagnostics without parsing.
	var stderrBuf strings.Builder
	go func() { _, _ = io.Copy(&stderrBuf, stderr) }()

	// Stall detection is the orchestrator's responsibility per spec §8.5
	// (it lives in the reconciliation tick, not the agent runner). The
	// runner records a last-event timestamp on the RunResult so the
	// orchestrator can compare against StallTimeout from its loop.
	lastEventAt := time.Now()
	bumpLastEvent := func() { lastEventAt = time.Now() }
	_ = lastEventAt // keep for the result; assigned via bumpLastEvent below

	// Stream events: read each JSONL line, normalize, fan out.
	result := RunResult{Type: EventTurnFailed}
	scanner := bufio.NewScanner(stdout)
	// Bump scanner buffer — single events can carry large payloads.
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		ev := normalize(append([]byte(nil), line...))
		bumpLastEvent()
		if ev.SessionID != "" && result.SessionID == "" {
			result.SessionID = ev.SessionID
		}
		if ev.ThreadID != "" {
			result.ThreadID = ev.ThreadID
		}
		if ev.TurnID != "" {
			result.TurnID = ev.TurnID
		}
		if onEvent != nil {
			onEvent(ev)
		}
		if ev.Usage.TotalTokens > 0 {
			result.Usage = ev.Usage
		}
		if isTerminal(ev.Type) {
			result.Type = ev.Type
			result.Error = ev.Error
		}
	}
	if err := scanner.Err(); err != nil {
		return RunResult{Type: EventTurnFailed, Error: err.Error(), Duration: time.Since(start), SessionID: result.SessionID, ThreadID: result.ThreadID, TurnID: result.TurnID},
			fmt.Errorf("scanner: %w (stderr: %s)", err, truncate(stderrBuf.String(), 200))
	}

	waitErr := cmd.Wait()
	result.Duration = time.Since(start)

	// Map subprocess exit conditions:
	//   - context deadline exceeded → turn_timeout
	//   - context canceled (ours) → turn_cancelled
	//   - exit error with no terminal event seen → turn_failed
	//   - clean exit with terminal event → use that event's type
	switch {
	case errors.Is(turnCtx.Err(), context.DeadlineExceeded):
		result.Type = EventTurnTimedOut
		result.Error = fmt.Sprintf("turn exceeded %s timeout", r.TurnTimeout)
	case errors.Is(turnCtx.Err(), context.Canceled) && result.Type != EventTurnCompleted:
		result.Type = EventTurnCancelled
		result.Error = "canceled by orchestrator"
	case waitErr != nil && result.Type == EventTurnFailed:
		result.Error = fmt.Sprintf("subprocess: %v (stderr: %s)", waitErr, truncate(stderrBuf.String(), 200))
	}

	return result, nil
}

func (r *Runner) WithTurnConfig(cfg agent.TurnConfig) agent.Runner {
	copy := *r
	if cfg.Command != "" {
		copy.Command = cfg.Command
	}
	copy.TurnTimeout = cfg.TurnTimeout
	copy.StallTimeout = cfg.StallTimeout
	copy.Env = map[string]string{}
	for k, v := range cfg.Env {
		copy.Env[k] = v
	}
	return &copy
}

func (r *Runner) buildCommand(prompt, resume string) string {
	// Construct: eval brew shellenv && [export K=V ...] && cd <WorkingDir> && <Command> [--resume <id>]
	//
	// We prefix `eval "$(brew shellenv)"` so Homebrew binaries (claude, gh,
	// supabase, etc.) land on PATH inside the non-interactive shell that
	// SSH starts. macOS's default zsh doesn't run .zshrc non-interactively.
	//
	// We shell-escape the prompt by base64-encoding it and piping. That
	// avoids quoting hazards entirely for arbitrary template output.
	cmd := r.Command
	if resume != "" {
		cmd += " --resume " + shellQuote(resume)
	}
	encoded := base64Encode(prompt)
	envPrefix := ""
	for k, v := range r.Env {
		envPrefix += "export " + k + "=" + shellQuote(v) + " && "
	}
	return fmt.Sprintf(`eval "$(/opt/homebrew/bin/brew shellenv)" && %scd %s && echo %s | base64 -d | %s`,
		envPrefix, shellQuote(r.WorkingDir), encoded, cmd)
}

func isTerminal(t EventType) bool {
	switch t {
	case EventTurnCompleted, EventTurnFailed, EventTurnCancelled, EventTurnTimedOut:
		return true
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
