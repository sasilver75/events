package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/domain"
	"github.com/sasilver75/events/orchestrator/internal/workflow"
	"github.com/sasilver75/events/orchestrator/internal/workspace/tart"
)

// SpurWorker is the concrete Worker that ties the Tart Workspace Manager,
// WORKFLOW.md hooks, and configured Agent Runner together for one issue.
//
// Lifecycle (one Run call):
//  1. EnsureWorkspace — clone-or-boot the per-issue VM.
//  2. RunHook(after_create) iff Workspace.CreatedNow.
//  3. RunHook(before_run).
//  4. Render the WORKFLOW.md prompt with this issue.
//  5. AgentRunner.Run(prompt) — the selected coding agent inside the VM.
//  6. RunHook(after_run).
//  7. Return result; orchestrator decides whether to clean up.
type SpurWorker struct {
	mu sync.RWMutex

	Workflow *workflow.Definition
	Config   workflow.ServiceConfig

	WorkspaceMgr *tart.Manager
	AgentRunner  agent.ConfigurableRunner
	DynamicTools []agent.DynamicTool

	// HarnessCreds holds the per-VM credential bundle that the
	// before_run hook injects. Read once at orchestrator startup from
	// host env vars; never written to disk on the host.
	HarnessCreds Credentials

	// RunLogRoot is where per-run logs/artifacts get scped out of the
	// VM. Default: ~/.cache/spur-orchestrator/runs/<SAM-N>/<timestamp>/
	RunLogRoot string

	Logger *slog.Logger
}

// Credentials are injected into per-issue VMs at run time. See
// docs/agents/harness.md §Credentials.
type Credentials struct {
	GitHubToken     string
	LinearToken     string
	CodexHarnessDir string // optional host path to a filtered ~/.codex/ snapshot
}

// Run executes one full ticket lifecycle and returns a WorkerResult.
// Errors are captured in the result, not returned, so the orchestrator
// can always integrate the outcome into its state machine.
func (w *SpurWorker) Run(ctx context.Context, issue domain.Issue, attempt *int, resumeSessionID string) WorkerResult {
	def, cfg := w.workflowSnapshot()
	logger := w.Logger.With("issue", issue.Identifier)
	logger.Info("worker starting", "title", issue.Title, "state", issue.State)

	// 1. Workspace.
	workspace, vmIP, err := w.WorkspaceMgr.EnsureWorkspace(ctx, issue.Identifier)
	if err != nil {
		return WorkerResult{Issue: issue, Status: domain.RunStatusFailed, Error: "ensure_workspace: " + err.Error()}
	}
	logger.Info("workspace ready", "vm", workspace.Path, "ip", vmIP, "created_now", workspace.CreatedNow)

	runLogDir := w.runLogDirFor(issue, attempt)

	issueJSON, _ := json.Marshal(issue) // best-effort; rendering errors are non-fatal here

	linearToken := w.HarnessCreds.LinearToken
	if cfg.LinearAccessMode() == "host_proxy" {
		linearToken = ""
	}

	hookEnv := tart.HookEnv{
		VMName:          workspace.Path,
		VMIP:            vmIP,
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		IssueJSON:       string(issueJSON),
		GitHubToken:     w.HarnessCreds.GitHubToken,
		LinearToken:     linearToken,
		RunLogDir:       runLogDir,
		SSHKey:          w.WorkspaceMgr.SSHKey,
		HarnessCodexDir: w.HarnessCreds.CodexHarnessDir,
	}

	// 2. after_create (only on first creation).
	hookTimeout := time.Duration(cfg.Hooks.TimeoutMs) * time.Millisecond
	if workspace.CreatedNow && cfg.Hooks.AfterCreate != "" {
		logger.Info("running after_create hook")
		if out, err := tart.RunHook(ctx, "after_create", cfg.Hooks.AfterCreate, hookEnv, hookTimeout); err != nil {
			logger.Error("after_create hook failed", "err", err, "out", trim(string(out), 400))
			return WorkerResult{Issue: issue, Status: domain.RunStatusFailed, Error: "after_create: " + err.Error()}
		}
	}

	// 3. before_run.
	if cfg.Hooks.BeforeRun != "" {
		logger.Info("running before_run hook")
		if out, err := tart.RunHook(ctx, "before_run", cfg.Hooks.BeforeRun, hookEnv, hookTimeout); err != nil {
			logger.Error("before_run hook failed", "err", err, "out", trim(string(out), 400))
			return WorkerResult{Issue: issue, Status: domain.RunStatusFailed, Error: "before_run: " + err.Error()}
		}
	}

	// 4. Render prompt.
	prompt, err := workflow.Render(def.PromptTemplate, issue, attempt)
	if err != nil {
		return WorkerResult{Issue: issue, Status: domain.RunStatusFailed, Error: "prompt_render: " + err.Error()}
	}

	// 5. Agent.
	agentRunner := w.AgentRunner.WithTurnConfig(w.agentTurnConfig(cfg))
	logger.Info("launching agent")
	agentResult, runErr := agentRunner.Run(ctx, vmIP, prompt, resumeSessionID, func(ev agent.Event) {
		// Lightweight live logging; high-volume fan-out goes to a log
		// sink in a real deployment.
		if ev.Type == agent.EventSessionStarted {
			logger.Info("agent session started", "session_id", ev.SessionID)
		}
	})
	if runErr != nil {
		logger.Error("agent run errored", "err", runErr)
	}
	logger.Info("agent finished",
		"event", string(agentResult.Type),
		"session_id", agentResult.SessionID,
		"thread_id", agentResult.ThreadID,
		"turn_id", agentResult.TurnID,
		"duration", agentResult.Duration,
		"tokens", agentResult.Usage.TotalTokens,
		"rate_limit", agentResult.RateLimits,
	)

	// 6. after_run (always, regardless of outcome).
	if cfg.Hooks.AfterRun != "" {
		if out, err := tart.RunHook(ctx, "after_run", cfg.Hooks.AfterRun, hookEnv, hookTimeout); err != nil {
			logger.Warn("after_run hook failed (continuing)", "err", err, "out", trim(string(out), 400))
		}
	}

	// 7. Map agent event → domain.RunStatus for orchestrator.
	return WorkerResult{
		Issue:        issue,
		Status:       mapAgentEventToStatus(agentResult.Type),
		Error:        agentResult.Error,
		SessionID:    agentResult.SessionID,
		ThreadID:     agentResult.ThreadID,
		TurnID:       agentResult.TurnID,
		InputTokens:  agentResult.Usage.InputTokens,
		OutputTokens: agentResult.Usage.OutputTokens,
		TotalTokens:  agentResult.Usage.TotalTokens,
		RateLimits:   agentResult.RateLimits,
	}
}

// Cleanup runs the terminal workspace cleanup path for an issue. It boots an
// existing VM if needed so before_remove can collect artifacts, then stops and
// deletes the Tart workspace.
func (w *SpurWorker) Cleanup(ctx context.Context, issue domain.Issue) error {
	_, cfg := w.workflowSnapshot()
	vmName := tart.VMNameFor(issue.Identifier)
	exists, err := w.WorkspaceMgr.Exists(ctx, vmName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	workspace, vmIP, err := w.WorkspaceMgr.EnsureWorkspace(ctx, issue.Identifier)
	if err != nil {
		return err
	}

	issueJSON, _ := json.Marshal(issue)
	linearToken := w.HarnessCreds.LinearToken
	if cfg.LinearAccessMode() == "host_proxy" {
		linearToken = ""
	}
	hookEnv := tart.HookEnv{
		VMName:          workspace.Path,
		VMIP:            vmIP,
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		IssueJSON:       string(issueJSON),
		GitHubToken:     w.HarnessCreds.GitHubToken,
		LinearToken:     linearToken,
		RunLogDir:       w.runLogDirFor(issue, nil),
		SSHKey:          w.WorkspaceMgr.SSHKey,
		HarnessCodexDir: w.HarnessCreds.CodexHarnessDir,
	}

	hookTimeout := time.Duration(cfg.Hooks.TimeoutMs) * time.Millisecond
	if cfg.Hooks.BeforeRemove != "" {
		if out, err := tart.RunHook(ctx, "before_remove", cfg.Hooks.BeforeRemove, hookEnv, hookTimeout); err != nil {
			w.Logger.Warn("before_remove hook failed (continuing with VM delete)",
				"issue", issue.Identifier, "err", err, "out", trim(string(out), 400))
		}
	}
	return w.WorkspaceMgr.RemoveWorkspace(ctx, workspace.Path)
}

func (w *SpurWorker) UpdateWorkflow(def *workflow.Definition, cfg workflow.ServiceConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Workflow = def
	w.Config = cfg
}

func (w *SpurWorker) workflowSnapshot() (*workflow.Definition, workflow.ServiceConfig) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.Workflow, w.Config
}

func (w *SpurWorker) agentTurnConfig(cfg workflow.ServiceConfig) agent.TurnConfig {
	// Inject per-run secrets directly into the agent subprocess env. In
	// host_proxy mode the Linear token stays on the host and Codex receives a
	// dynamic tool instead.
	env := map[string]string{
		"GITHUB_TOKEN": w.HarnessCreds.GitHubToken,
	}
	var dynamicTools []agent.DynamicTool
	if cfg.LinearAccessMode() == "host_proxy" {
		dynamicTools = append(dynamicTools, w.DynamicTools...)
	} else {
		env["LINEAR_API_KEY"] = w.HarnessCreds.LinearToken
	}
	return agent.TurnConfig{
		Command:      cfg.AgentCommand(),
		TurnTimeout:  time.Duration(cfg.AgentTurnTimeoutMs()) * time.Millisecond,
		StallTimeout: time.Duration(cfg.AgentStallTimeoutMs()) * time.Millisecond,
		Env:          env,
		DynamicTools: dynamicTools,
	}
}

func mapAgentEventToStatus(t agent.EventType) domain.RunStatus {
	switch t {
	case agent.EventTurnCompleted:
		return domain.RunStatusSucceeded
	case agent.EventTurnFailed:
		return domain.RunStatusFailed
	case agent.EventTurnCancelled:
		return domain.RunStatusCanceledByReconciliation
	case agent.EventTurnTimedOut:
		return domain.RunStatusTimedOut
	case agent.EventTurnStalled:
		return domain.RunStatusStalled
	case agent.EventStartupFailed:
		return domain.RunStatusFailed
	default:
		return domain.RunStatusFailed
	}
}

func (w *SpurWorker) runLogDirFor(issue domain.Issue, attempt *int) string {
	root := w.RunLogRoot
	if root == "" {
		root = "/tmp/spur-orchestrator/runs"
	}
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	if attempt != nil {
		return filepath.Join(root, issue.Identifier, fmt.Sprintf("%s-attempt-%d", timestamp, *attempt))
	}
	return filepath.Join(root, issue.Identifier, timestamp)
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
