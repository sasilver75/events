// Command spur-orchestrator implements OpenAI's Symphony spec adapted for
// Spur. Polls Linear, dispatches per-issue Tart VM workspaces, runs Codex
// app-server agents inside them, reconciles state.
//
// Reference: https://github.com/openai/symphony/blob/main/SPEC.md
// Adaptations: docs/agents/harness.md
//
// Two modes:
//
//	spur-orchestrator              → daemon (poll loop on the interval
//	                                 from WORKFLOW.md polling.interval_ms)
//	spur-orchestrator --once       → single tick; exits after current
//	                                 work completes
//	spur-orchestrator --once --issue SAM-12
//	                               → dispatch one specific issue and exit
//	spur-orchestrator --once --issue SAM-12 --preflight
//	                               → validate credentials, tracker eligibility,
//	                                 and Codex app-server without dispatching
//	spur-orchestrator --once --issue SAM-12 --review-pr 123
//	                               → run one reviewer-agent pass for a
//	                                 harness-created PR, optionally followed
//	                                 by one bounded implementer response
//	spur-orchestrator --once --preflight
//	                               → list eligible Codex candidates
//	spur-orchestrator --codex-canary-checklist --issue SAM-12
//	                               → print the evidence checklist for deciding
//	                                 whether a Codex run succeeded
//	spur-orchestrator --codex-canary-verify-status --issue SAM-12 --status-file /tmp/spur-orchestrator/SAM-12-codex.json
//	                               → validate local machine-readable status
//	                                 evidence from a Codex run without Linear
//	                                 credentials
//	spur-orchestrator --status-file /tmp/spur-orchestrator/status.json
//	                               → daemon mode with JSON status snapshots
//	spur-orchestrator --codex-smoke → check configured Codex app-server protocol
//	                                 startup and exit
//
// Required env vars at startup:
//
//	LINEAR_API_KEY                 → tracker reads only when tracker.api_key is
//	                                 omitted from WORKFLOW.md or set to
//	                                 $LINEAR_API_KEY
//	SPUR_HARNESS_GITHUB_TOKEN      → injected into per-VM credentials
//	SPUR_HARNESS_LINEAR_BOT_TOKEN  → optional fallback for vm_env mode;
//	                                 production host_proxy mode keeps Linear
//	                                 credentials on the host
//	SPUR_HARNESS_SSH_KEY           → path to harness private key
//	                                 (default: ~/.ssh/spur-agent-vm)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/agent/codex"
	"github.com/sasilver75/events/orchestrator/internal/domain"
	"github.com/sasilver75/events/orchestrator/internal/orchestrator"
	"github.com/sasilver75/events/orchestrator/internal/tracker/linear"
	"github.com/sasilver75/events/orchestrator/internal/workflow"
	"github.com/sasilver75/events/orchestrator/internal/workspace/tart"
)

func main() {
	var (
		workflowPath         = flag.String("workflow", "WORKFLOW.md", "Path to WORKFLOW.md (Symphony spec §5.1)")
		once                 = flag.Bool("once", false, "Dispatch one tick and exit (used by scripts/spur-agent)")
		issue                = flag.String("issue", "", "When --once is set, dispatch only this specific Linear issue identifier (e.g. SAM-12)")
		validate             = flag.Bool("validate", false, "Validate WORKFLOW.md and exit without running")
		codexSmoke           = flag.Bool("codex-smoke", false, "Start configured codex app-server, initialize it, and exit")
		codexCanary          = flag.Bool("codex-canary", false, "Deprecated no-op; Codex is the production runner")
		codexCanaryChecklist = flag.Bool("codex-canary-checklist", false, "Print the post-run evidence checklist for a Codex run and exit")
		codexCanaryVerify    = flag.Bool("codex-canary-verify-status", false, "Validate a local Codex status JSON proof file without contacting Linear, then exit")
		preflight            = flag.Bool("preflight", false, "Validate credentials, tracker access, target issue eligibility, and Codex handshake without dispatching")
		reviewPR             = flag.String("review-pr", "", "Run reviewer-agent mode for this GitHub PR number or URL; requires --once and --issue")
		reviewImplementer    = flag.Bool("review-allow-implementer-response", false, "Allow at most one bounded implementer-agent response turn when reviewer-agent reports actionable comments")
		reviewImplTimeoutMs  = flag.Int("review-implementer-timeout-ms", 1800000, "Timeout for the optional implementer-agent response turn")
		statusFile           = flag.String("status-file", "", "Optional path for runtime JSON status snapshots, or status verifier input")
		verbose              = flag.Bool("verbose", false, "Log at debug level")
	)
	flag.Parse()

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// 1. Load + validate WORKFLOW.md.
	def, err := workflow.Load(*workflowPath)
	if err != nil {
		fatal(logger, "workflow load failed", "path", *workflowPath, "err", err)
	}
	cfg := workflow.NewServiceConfig(def.Config)
	if err := applyCLIConfigOverrides(&cfg, *codexCanary, *once, *preflight, *issue); err != nil {
		fatal(logger, "invalid CLI override", "err", err)
	}
	if err := validateWorkflowConfigForMode(cfg, *codexCanaryVerify); err != nil {
		fatal(logger, "workflow validation failed", "err", err)
	}
	if *codexCanaryChecklist {
		writeCodexCanaryChecklist(os.Stdout, *issue)
		return
	}
	if *codexCanaryVerify {
		if err := verifyCodexCanaryStatusArgs(*issue, *statusFile); err != nil {
			fatal(logger, "codex status verification failed", "err", err)
		}
		if err := verifyCodexCanaryStatusFile(*statusFile, *issue, os.Stdout); err != nil {
			fatal(logger, "codex status verification failed", "err", err)
		}
		return
	}
	logger.Info("workflow loaded",
		"tracker", cfg.Tracker.Kind,
		"project_slug", cfg.Tracker.ProjectSlug,
		"active_states", cfg.Tracker.ActiveStates,
		"max_concurrent_agents", cfg.Agent.MaxConcurrentAgents,
		"agent_runner", cfg.AgentRunnerName(),
		"linear_access", cfg.LinearAccessMode(),
		"workspace_base_image", cfg.Workspace.BaseImage,
	)
	if *validate {
		fmt.Println("WORKFLOW.md is valid.")
		return
	}
	if *codexSmoke {
		result, err := codex.SmokeCheck(context.Background(), cfg.Codex.Command, []agent.DynamicTool{codex.SmokeLinearGraphQLTool()})
		if err != nil {
			fatal(logger, "codex smoke check failed", "err", err)
		}
		fmt.Printf("Codex app-server smoke check passed: user_agent=%q codex_home=%q platform=%q thread_id=%q\n", result.UserAgent, result.CodexHome, result.Platform, result.ThreadID)
		return
	}
	if *preflight {
		if err := validatePreflightLocalReadiness(cfg, *issue != ""); err != nil {
			fatal(logger, "preflight readiness failed", "err", err)
		}
	}
	if err := validateReviewerModeArgs(*reviewPR, *once, *issue); err != nil {
		fatal(logger, "reviewer mode argument error", "err", err)
	}

	// 2. Resolve credentials from env.
	linearAPIKey, err := cfg.ResolveTrackerAPIKey()
	if err != nil {
		fatal(logger, "missing Linear API key", "err", err)
	}
	githubToken := os.Getenv("SPUR_HARNESS_GITHUB_TOKEN")
	linearBotToken := os.Getenv("SPUR_HARNESS_LINEAR_BOT_TOKEN")
	if linearBotToken == "" {
		linearBotToken = linearAPIKey // fall back to the read key if no bot configured
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 3. Build the dependency graph.
	tracker, err := linear.New(linear.Config{
		Endpoint:    cfg.Tracker.Endpoint,
		APIKey:      linearAPIKey,
		ProjectSlug: cfg.Tracker.ProjectSlug,
	})
	if err != nil {
		fatal(logger, "linear client init failed", "err", err)
	}
	eligibility := linear.SpurDefault
	viewerID, err := tracker.ViewerID(ctx)
	if err != nil {
		fatal(logger, "linear viewer lookup failed", "err", err)
	}
	eligibility.CurrentUserID = viewerID
	if *preflight {
		if *issue != "" {
			if err := validatePreflightCredentials(githubToken); err != nil {
				fatal(logger, "preflight failed", "err", err)
			}
		}
		if err := runPreflight(ctx, cfg, tracker, eligibility, *issue); err != nil {
			fatal(logger, "preflight failed", "err", err)
		}
		if cfg.AgentRunnerName() == "codex" {
			result, err := codex.SmokeCheck(ctx, cfg.Codex.Command, []agent.DynamicTool{tracker.DynamicTool()})
			if err != nil {
				fatal(logger, "codex preflight failed", "err", err)
			}
			fmt.Printf("Codex preflight passed: user_agent=%q codex_home=%q platform=%q thread_id=%q\n", result.UserAgent, result.CodexHome, result.Platform, result.ThreadID)
		}
		return
	}
	if err := validatePreflightCredentials(githubToken); err != nil {
		fatal(logger, "missing GitHub token", "err", err)
	}
	sshKey := resolvedSSHKey()
	if _, err := os.Stat(sshKey); err != nil {
		fatal(logger, "harness SSH key not found", "path", sshKey, "err", err)
	}
	wsManager := tart.New(cfg.Workspace.BaseImage, "admin", sshKey)
	agentRunner := buildAgentRunner(cfg, wsManager)
	codexHarnessDir := os.Getenv("SPUR_HARNESS_CODEX_DIR")
	worker := &orchestrator.SpurWorker{
		Workflow:     def,
		Config:       cfg,
		WorkspaceMgr: wsManager,
		AgentRunner:  agentRunner,
		DynamicTools: []agent.DynamicTool{tracker.DynamicTool()},
		HarnessCreds: orchestrator.Credentials{
			GitHubToken:     githubToken,
			LinearToken:     linearBotToken,
			CodexHarnessDir: codexHarnessDir,
		},
		Logger: logger,
	}
	if *reviewPR != "" {
		reviewIssue, err := tracker.FetchIssueByIdentifier(ctx, *issue)
		if err != nil {
			fatal(logger, "review issue fetch failed", "issue", *issue, "err", err)
		}
		result := worker.RunReviewLoop(ctx, orchestrator.ReviewLoopRequest{
			Issue:                    reviewIssue,
			PullRequest:              *reviewPR,
			AllowImplementerResponse: *reviewImplementer,
			ImplementerTurnTimeoutMs: *reviewImplTimeoutMs,
		}, nil)
		if *statusFile != "" {
			if err := writeJSONFile(*statusFile, result); err != nil {
				fatal(logger, "review status write failed", "path", *statusFile, "err", err)
			}
		}
		fmt.Printf("Review loop complete: issue=%s pr=%s state=%s actionable=%t needs_human=%t\n",
			result.IssueIdentifier, result.PullRequest, result.State, result.ActionableComments, result.NeedsHuman)
		if result.NeedsHuman {
			reason := result.ReviewerError
			if reason == "" {
				reason = result.ImplementerError
			}
			if reason == "" {
				reason = "reviewer_agent_needs_human"
			}
			if err := tracker.EscalateNeedsHuman(ctx, reviewIssue, reason, 1); err != nil {
				fatal(logger, "needs-human escalation failed", "err", err)
			}
			os.Exit(2)
		}
		return
	}
	orch := orchestrator.New(def, cfg, tracker, worker, logger)
	orch.Eligibility = eligibility
	orch.StatusFile = *statusFile
	if err := orch.EnableWorkflowReload(*workflowPath); err != nil {
		fatal(logger, "workflow reload setup failed", "path", *workflowPath, "err", err)
	}

	if *once {
		if err := orch.RunOnce(ctx, *issue); err != nil {
			logger.Error("once mode failed", "err", err)
			os.Exit(2)
		}
		return
	}
	if err := orch.RunDaemon(ctx); err != nil {
		logger.Error("daemon failed", "err", err)
		os.Exit(2)
	}
}

func requireEnv(preferredVar, fallbackVar string) (string, error) {
	if preferredVar != "" {
		if v := os.Getenv(preferredVar); v != "" {
			return v, nil
		}
	}
	if v := os.Getenv(fallbackVar); v != "" {
		return v, nil
	}
	if preferredVar == "" || preferredVar == fallbackVar {
		return "", fmt.Errorf("env var %s is not set", fallbackVar)
	}
	return "", fmt.Errorf("env var %s (or %s) is not set", preferredVar, fallbackVar)
}

func buildAgentRunner(cfg workflow.ServiceConfig, wsManager *tart.Manager) agent.ConfigurableRunner {
	return &codex.Runner{
		Workspace:    wsManager,
		Command:      cfg.Codex.Command,
		TurnTimeout:  time.Duration(cfg.Codex.TurnTimeoutMs) * time.Millisecond,
		StallTimeout: time.Duration(cfg.Codex.StallTimeoutMs) * time.Millisecond,
		WorkingDir:   "/Users/admin/events",
	}
}

func applyCLIConfigOverrides(cfg *workflow.ServiceConfig, codexCanary, once, preflight bool, issueIdentifier string) error {
	if cfg == nil || !codexCanary {
		return nil
	}
	// Kept for old scripts while Codex moved from canary to production.
	if !once {
		return fmt.Errorf("--codex-canary requires --once")
	}
	if issueIdentifier == "" && !preflight {
		return fmt.Errorf("--codex-canary requires --issue unless --preflight is set")
	}
	cfg.Agent.Runner = "codex"
	cfg.Credentials.LinearAccess = "host_proxy"
	return nil
}

func validateWorkflowConfigForMode(cfg workflow.ServiceConfig, offlineStatusVerification bool) error {
	if offlineStatusVerification {
		return cfg.ValidateWithoutTrackerAPIKey()
	}
	return cfg.Validate()
}

func validatePreflightCredentials(githubToken string) error {
	if githubToken == "" {
		return errors.New("SPUR_HARNESS_GITHUB_TOKEN is not set")
	}
	return nil
}

func validateReviewerModeArgs(reviewPR string, once bool, issueIdentifier string) error {
	if reviewPR == "" {
		return nil
	}
	if !once {
		return errors.New("--review-pr requires --once")
	}
	if issueIdentifier == "" {
		return errors.New("--review-pr requires --issue")
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func validatePreflightLocalReadiness(cfg workflow.ServiceConfig, requireRunCredentials bool) error {
	var problems []string
	if _, err := cfg.ResolveTrackerAPIKey(); err != nil {
		problems = append(problems, err.Error())
	}
	if requireRunCredentials {
		if err := validatePreflightCredentials(os.Getenv("SPUR_HARNESS_GITHUB_TOKEN")); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if codexDir := os.Getenv("SPUR_HARNESS_CODEX_DIR"); codexDir != "" {
		info, err := os.Stat(codexDir)
		if err != nil {
			problems = append(problems, fmt.Sprintf("SPUR_HARNESS_CODEX_DIR not found at %s: %v", codexDir, err))
		} else if !info.IsDir() {
			problems = append(problems, fmt.Sprintf("SPUR_HARNESS_CODEX_DIR is not a directory: %s", codexDir))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

func resolvedSSHKey() string {
	if sshKey := os.Getenv("SPUR_HARNESS_SSH_KEY"); sshKey != "" {
		return sshKey
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "spur-agent-vm")
}

type preflightTracker interface {
	FetchCandidateIssues(ctx context.Context, activeStates []string) ([]domain.Issue, error)
}

func runPreflight(ctx context.Context, cfg workflow.ServiceConfig, tracker preflightTracker, eligibility linear.EligibilityFilter, issueIdentifier string) error {
	return runPreflightWithOutput(ctx, cfg, tracker, eligibility, issueIdentifier, os.Stdout)
}

func runPreflightWithOutput(ctx context.Context, cfg workflow.ServiceConfig, tracker preflightTracker, eligibility linear.EligibilityFilter, issueIdentifier string, out io.Writer) error {
	candidates, err := tracker.FetchCandidateIssues(ctx, cfg.Tracker.ActiveStates)
	if err != nil {
		return fmt.Errorf("fetch candidate issues: %w", err)
	}
	eligible, rejected := eligibility.Apply(candidates)
	if issueIdentifier == "" {
		writePreflightCandidateSummary(out, cfg, candidates, eligible, rejected)
		return nil
	}

	for _, issue := range eligible {
		if issue.Identifier == issueIdentifier {
			_, _ = fmt.Fprintf(out, "Preflight passed: issue=%s runner=%s linear_access=%s\n", issue.Identifier, cfg.AgentRunnerName(), cfg.LinearAccessMode())
			return nil
		}
	}
	for _, rejection := range rejected {
		if rejection.Issue.Identifier == issueIdentifier {
			return fmt.Errorf("issue %s is not eligible: %s", issueIdentifier, rejection.Reason)
		}
	}
	return fmt.Errorf("issue not found among active candidates: %s", issueIdentifier)
}

func writePreflightCandidateSummary(out io.Writer, cfg workflow.ServiceConfig, candidates, eligible []domain.Issue, rejected []linear.Rejection) {
	_, _ = fmt.Fprintf(out, "Preflight passed: candidates=%d eligible=%d rejected=%d runner=%s linear_access=%s\n",
		len(candidates), len(eligible), len(rejected), cfg.AgentRunnerName(), cfg.LinearAccessMode())

	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].Identifier < eligible[j].Identifier
	})
	if len(eligible) == 0 {
		_, _ = fmt.Fprintln(out, "Eligible issues: none")
	} else {
		_, _ = fmt.Fprintln(out, "Eligible issues:")
		for _, issue := range eligible {
			_, _ = fmt.Fprintf(out, "- %s [%s] %s\n", issue.Identifier, issue.State, issue.Title)
		}
	}

	sort.Slice(rejected, func(i, j int) bool {
		return rejected[i].Issue.Identifier < rejected[j].Issue.Identifier
	})
	if len(rejected) > 0 {
		_, _ = fmt.Fprintln(out, "Rejected active candidates:")
		for _, rejection := range rejected {
			_, _ = fmt.Fprintf(out, "- %s: %s\n", rejection.Issue.Identifier, rejection.Reason)
		}
	}
}

func writeCodexCanaryChecklist(out io.Writer, issueIdentifier string) {
	if issueIdentifier == "" {
		issueIdentifier = "<SAM-N>"
	}
	writef(out, "Codex run evidence checklist for %s\n", issueIdentifier)
	writeln(out)
	writeln(out, "Pre-run gates:")
	writef(out, "[ ] Discovery preflight listed %s as eligible.\n", issueIdentifier)
	writeln(out, "[ ] Issue preflight passed with runner=codex and linear_access=host_proxy.")
	writeln(out, "[ ] Issue preflight printed a Codex app-server user agent, codex_home, platform, and thread_id.")
	writeln(out)
	writeln(out, "Run evidence:")
	writef(out, "[ ] `spur-orchestrator --once --issue %s --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/%s-codex.json` exited 0.\n", issueIdentifier, issueIdentifier)
	writef(out, "[ ] `spur-orchestrator --codex-canary-verify-status --issue %s --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/%s-codex.json` exited 0.\n", issueIdentifier, issueIdentifier)
	writeln(out, "[ ] Logs show `agent_runner=codex`, `linear_access=host_proxy`, `agent session started`, and `agent finished`.")
	writef(out, "[ ] Required status file `/tmp/spur-orchestrator/%s-codex.json` exists and contains `agent_runner=codex`, `linear_access=host_proxy`, `recent_runs[0].identifier=%s`, `recent_runs[0].status`, `recent_runs[0].session_id`, `recent_runs[0].thread_id`, `recent_runs[0].turn_id`, `recent_runs[0].token_info`, `recent_runs[0].rate_limits` if Codex emitted them, `codex_totals`, and latest `codex_rate_limits` if Codex emitted them.\n", issueIdentifier, issueIdentifier)
	writeln(out, "[ ] The issue did not enter `needs_human` due to a successful-continuation loop.")
	writeln(out)
	writeln(out, "Linear handoff:")
	writeln(out, "[ ] Pickup comment exists for the run attempt.")
	writeln(out, "[ ] Issue was moved to `In Progress` while work was active.")
	writeln(out, "[ ] Closeout comment exists with PR link, AC table, drift list, artifacts, and test evidence.")
	writeln(out, "[ ] Issue state is `In Review` after the PR is opened.")
	writeln(out)
	writeln(out, "GitHub handoff:")
	writef(out, "[ ] `scripts/spur-publish-preflight %s` passed before commit/push/PR creation.\n", issueIdentifier)
	writeln(out, "[ ] Branch was pushed.")
	writeln(out, "[ ] PR is ready for review, not draft, unless the PR body and Linear handoff explicitly explain a draft condition.")
	writeln(out, "[ ] PR title includes the issue identifier.")
	writeln(out, "[ ] PR body links the Linear issue and includes self-assessment.")
	writeln(out, "[ ] Required CI/checks are green or failures are explained in the Linear closeout.")
	writeln(out)
	writeln(out, "Credential boundary:")
	writeln(out, "[ ] No host-held Linear credential leaked into the agent environment.")
	writeln(out, "[ ] No unsupported Codex app-server request blocked the run.")
}

func verifyCodexCanaryStatusArgs(issueIdentifier, statusFile string) error {
	if issueIdentifier == "" {
		return errors.New("--codex-canary-verify-status requires --issue")
	}
	if statusFile == "" {
		return errors.New("--codex-canary-verify-status requires --status-file")
	}
	return nil
}

func verifyCodexCanaryStatusFile(path, issueIdentifier string, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read status file: %w", err)
	}

	var snapshot orchestrator.StatusSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("parse status file: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse status file fields: %w", err)
	}

	if snapshot.AgentRunner != "codex" {
		return fmt.Errorf("agent_runner=%q, want codex", snapshot.AgentRunner)
	}
	if snapshot.LinearAccess != "host_proxy" {
		return fmt.Errorf("linear_access=%q, want host_proxy", snapshot.LinearAccess)
	}
	if _, ok := raw["codex_totals"]; !ok {
		return errors.New("status file missing codex_totals")
	}
	if _, ok := raw["recent_runs"]; !ok {
		return errors.New("status file missing recent_runs")
	}

	run, runRaw, err := findStatusRun(data, issueIdentifier)
	if err != nil {
		return err
	}
	if run.Status != "succeeded" {
		return fmt.Errorf("recent run for %s has status=%q, want succeeded", issueIdentifier, run.Status)
	}
	if run.SessionID == "" {
		return fmt.Errorf("recent run for %s is missing session_id", issueIdentifier)
	}
	if run.ThreadID == "" {
		return fmt.Errorf("recent run for %s is missing thread_id", issueIdentifier)
	}
	if run.TurnID == "" {
		return fmt.Errorf("recent run for %s is missing turn_id", issueIdentifier)
	}
	if _, ok := runRaw["token_info"]; !ok {
		return fmt.Errorf("recent run for %s is missing token_info", issueIdentifier)
	}
	if run.TokenInfo.TotalTokens <= 0 {
		return fmt.Errorf("recent run for %s has total_tokens=%d, want > 0", issueIdentifier, run.TokenInfo.TotalTokens)
	}
	if snapshot.CodexTotals.TotalTokens < run.TokenInfo.TotalTokens {
		return fmt.Errorf("codex_totals.total_tokens=%d is less than run total_tokens=%d", snapshot.CodexTotals.TotalTokens, run.TokenInfo.TotalTokens)
	}

	writef(out, "Codex status verified: issue=%s status=%s session_id=%s thread_id=%s turn_id=%s tokens=%d runner=%s linear_access=%s\n",
		issueIdentifier, run.Status, run.SessionID, run.ThreadID, run.TurnID, run.TokenInfo.TotalTokens, snapshot.AgentRunner, snapshot.LinearAccess)
	if !hasNonNullJSONField(runRaw, "rate_limits") {
		writeln(out, "Warning: recent run has no rate_limits; this is acceptable only if Codex did not emit rate-limit telemetry.")
	}
	if !hasNonNullJSONField(raw, "codex_rate_limits") {
		writeln(out, "Warning: status file has no codex_rate_limits; this is acceptable only if Codex did not emit rate-limit telemetry.")
	}
	return nil
}

func findStatusRun(data []byte, issueIdentifier string) (orchestrator.StatusRunResult, map[string]json.RawMessage, error) {
	var raw struct {
		RecentRuns []json.RawMessage `json:"recent_runs"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return orchestrator.StatusRunResult{}, nil, fmt.Errorf("parse recent_runs: %w", err)
	}
	for _, rawRun := range raw.RecentRuns {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawRun, &fields); err != nil {
			return orchestrator.StatusRunResult{}, nil, fmt.Errorf("parse recent run fields: %w", err)
		}
		var identifier string
		if err := json.Unmarshal(fields["identifier"], &identifier); err != nil {
			return orchestrator.StatusRunResult{}, nil, fmt.Errorf("parse recent run identifier: %w", err)
		}
		if identifier != issueIdentifier {
			continue
		}
		var run orchestrator.StatusRunResult
		if err := json.Unmarshal(rawRun, &run); err != nil {
			return orchestrator.StatusRunResult{}, nil, fmt.Errorf("parse recent run for %s: %w", issueIdentifier, err)
		}
		return run, fields, nil
	}
	return orchestrator.StatusRunResult{}, nil, fmt.Errorf("status file has no recent run for %s", issueIdentifier)
}

func hasNonNullJSONField(fields map[string]json.RawMessage, name string) bool {
	v, ok := fields[name]
	if !ok {
		return false
	}
	return string(v) != "null"
}

func fatal(logger *slog.Logger, msg string, kv ...any) {
	logger.Error(msg, kv...)
	os.Exit(1)
}

func writef(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

func writeln(out io.Writer, args ...any) {
	_, _ = fmt.Fprintln(out, args...)
}
