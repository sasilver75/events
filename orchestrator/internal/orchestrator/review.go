package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/domain"
	"github.com/sasilver75/events/orchestrator/internal/workflow"
	"github.com/sasilver75/events/orchestrator/internal/workspace/tart"
)

const reviewerResultPath = ".spur-agent/reviewer-result.json"

// ReviewLoopRequest describes one bounded reviewer-agent pass for a
// harness-created PR.
type ReviewLoopRequest struct {
	Issue                    domain.Issue
	PullRequest              string
	AllowImplementerResponse bool
	ImplementerTurnTimeoutMs int
}

// ReviewLoopResult is a compact, serializable status surface for reviewer mode.
type ReviewLoopResult struct {
	IssueIdentifier          string             `json:"issue_identifier"`
	PullRequest              string             `json:"pull_request"`
	State                    domain.ReviewState `json:"state"`
	ActionableComments       bool               `json:"actionable_comments"`
	NeedsHuman               bool               `json:"needs_human"`
	Summary                  string             `json:"summary,omitempty"`
	ReviewerStatus           domain.RunStatus   `json:"reviewer_status"`
	ReviewerError            string             `json:"reviewer_error,omitempty"`
	ImplementerStatus        domain.RunStatus   `json:"implementer_status,omitempty"`
	ImplementerError         string             `json:"implementer_error,omitempty"`
	ImplementerResponseTried bool               `json:"implementer_response_tried"`
}

type reviewerAgentResult struct {
	State              domain.ReviewState `json:"state"`
	ActionableComments bool               `json:"actionable_comments"`
	NeedsHuman         bool               `json:"needs_human"`
	Summary            string             `json:"summary"`
}

// RunReviewLoop runs one reviewer-agent pass and, when requested and
// actionable comments exist, at most one implementer-agent response turn.
func (w *SpurWorker) RunReviewLoop(ctx context.Context, req ReviewLoopRequest, onEvent func(agent.Event)) ReviewLoopResult {
	_, cfg := w.workflowSnapshot()
	logger := w.Logger.With("issue", req.Issue.Identifier, "pr", req.PullRequest)
	logger.Info("review loop starting")

	base := ReviewLoopResult{
		IssueIdentifier: req.Issue.Identifier,
		PullRequest:     req.PullRequest,
		State:           domain.ReviewStateReviewerPassRequested,
	}
	if strings.TrimSpace(req.PullRequest) == "" {
		base.State = domain.ReviewStateNeedsHuman
		base.NeedsHuman = true
		base.ReviewerError = "missing pull request reference"
		return base
	}

	_, vmIP, hookEnv, err := w.prepareReviewWorkspace(ctx, req.Issue, cfg)
	if err != nil {
		base.State = domain.ReviewStateNeedsHuman
		base.NeedsHuman = true
		base.ReviewerError = err.Error()
		return base
	}
	defer w.runReviewAfterRunHook(ctx, cfg, hookEnv)

	reviewerPrompt := renderReviewerAgentPrompt(req, reviewerResultPath)
	agentRunner := w.AgentRunner.WithTurnConfig(w.agentTurnConfig(cfg))
	reviewerResult, runErr := agentRunner.Run(ctx, vmIP, reviewerPrompt, "", onEvent)
	base.ReviewerStatus = mapAgentEventToStatus(reviewerResult.Type)
	base.ReviewerError = reviewerResult.Error
	if runErr != nil {
		base.ReviewerError = joinErrorStrings(base.ReviewerError, runErr.Error())
	}
	if base.ReviewerStatus != domain.RunStatusSucceeded {
		base.State = domain.ReviewStateNeedsHuman
		base.NeedsHuman = true
		if base.ReviewerError == "" {
			base.ReviewerError = "reviewer agent did not complete successfully"
		}
		return base
	}

	summary, err := w.readReviewerAgentResult(ctx, vmIP, reviewerResultPath)
	if err != nil {
		base.State = domain.ReviewStateNeedsHuman
		base.NeedsHuman = true
		base.ReviewerError = joinErrorStrings(base.ReviewerError, err.Error())
		return base
	}
	base.State = summary.State
	base.ActionableComments = summary.ActionableComments
	base.NeedsHuman = summary.NeedsHuman
	base.Summary = summary.Summary
	if base.State == "" {
		base.State = domain.ReviewStateReviewPosted
	}
	if base.NeedsHuman || base.State == domain.ReviewStateNeedsHuman {
		base.State = domain.ReviewStateNeedsHuman
		base.NeedsHuman = true
		return base
	}
	if !req.AllowImplementerResponse || !summary.ActionableComments {
		base.State = domain.ReviewStateFinalHumanMergeGate
		return base
	}

	implementerCfg := cfg
	if req.ImplementerTurnTimeoutMs > 0 {
		implementerCfg.Codex.TurnTimeoutMs = req.ImplementerTurnTimeoutMs
	}
	implementerRunner := w.AgentRunner.WithTurnConfig(w.agentTurnConfig(implementerCfg))
	implementerPrompt := renderImplementerReviewResponsePrompt(req)
	implResult, implErr := implementerRunner.Run(ctx, vmIP, implementerPrompt, "", onEvent)
	base.ImplementerResponseTried = true
	base.ImplementerStatus = mapAgentEventToStatus(implResult.Type)
	base.ImplementerError = implResult.Error
	if implErr != nil {
		base.ImplementerError = joinErrorStrings(base.ImplementerError, implErr.Error())
	}
	base.State = domain.ReviewStateImplementerResponseAttempt
	if base.ImplementerStatus != domain.RunStatusSucceeded {
		base.State = domain.ReviewStateNeedsHuman
		base.NeedsHuman = true
		if base.ImplementerError == "" {
			base.ImplementerError = "implementer response turn did not complete successfully"
		}
		return base
	}
	base.State = domain.ReviewStateFinalHumanMergeGate
	return base
}

func (w *SpurWorker) prepareReviewWorkspace(ctx context.Context, issue domain.Issue, cfg workflow.ServiceConfig) (domain.Workspace, string, tart.HookEnv, error) {
	workspace, vmIP, err := w.WorkspaceMgr.EnsureWorkspace(ctx, issue.Identifier)
	if err != nil {
		return domain.Workspace{}, "", tart.HookEnv{}, fmt.Errorf("ensure_workspace: %w", err)
	}

	runLogDir := w.runLogDirFor(issue, nil)
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
		RunLogDir:       runLogDir,
		SSHKey:          w.WorkspaceMgr.SSHKey,
		HarnessCodexDir: w.HarnessCreds.CodexHarnessDir,
	}

	hookTimeout := time.Duration(cfg.Hooks.TimeoutMs) * time.Millisecond
	if workspace.CreatedNow && cfg.Hooks.AfterCreate != "" {
		if out, err := tart.RunHook(ctx, "after_create", cfg.Hooks.AfterCreate, hookEnv, hookTimeout); err != nil {
			return domain.Workspace{}, "", tart.HookEnv{}, fmt.Errorf("after_create: %w: %s", err, trim(string(out), 400))
		}
	}
	if cfg.Hooks.BeforeRun != "" {
		if out, err := tart.RunHook(ctx, "before_run", cfg.Hooks.BeforeRun, hookEnv, hookTimeout); err != nil {
			return domain.Workspace{}, "", tart.HookEnv{}, fmt.Errorf("before_run: %w: %s", err, trim(string(out), 400))
		}
	}
	return workspace, vmIP, hookEnv, nil
}

func (w *SpurWorker) runReviewAfterRunHook(ctx context.Context, cfg workflow.ServiceConfig, hookEnv tart.HookEnv) {
	if cfg.Hooks.AfterRun == "" {
		return
	}
	hookTimeout := time.Duration(cfg.Hooks.TimeoutMs) * time.Millisecond
	_, _ = tart.RunHook(ctx, "after_run", cfg.Hooks.AfterRun, hookEnv, hookTimeout)
}

func (w *SpurWorker) readReviewerAgentResult(ctx context.Context, vmIP, path string) (reviewerAgentResult, error) {
	out, err := w.WorkspaceMgr.SSHRun(ctx, vmIP, "cd /Users/admin/events && cat "+shellQuoteForReview(path))
	if err != nil {
		return reviewerAgentResult{}, fmt.Errorf("read reviewer result %s: %w", path, err)
	}
	var result reviewerAgentResult
	if err := json.Unmarshal(out, &result); err != nil {
		return reviewerAgentResult{}, fmt.Errorf("parse reviewer result %s: %w", path, err)
	}
	if result.State != "" && !validReviewState(result.State) {
		return reviewerAgentResult{}, fmt.Errorf("invalid reviewer state %q", result.State)
	}
	return result, nil
}

func validReviewState(state domain.ReviewState) bool {
	switch state {
	case domain.ReviewStateReviewPosted, domain.ReviewStateNeedsHuman, domain.ReviewStateFinalHumanMergeGate:
		return true
	default:
		return false
	}
}

func joinErrorStrings(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "; ")
}

func renderReviewerAgentPrompt(req ReviewLoopRequest, resultPath string) string {
	return fmt.Sprintf(`# Spur reviewer-agent task: %[1]s

You are the reviewer-agent for harness-created PR %[2]s. Review only; do not edit files, push commits, approve, or merge.

Review state model:
- implementation_complete: the implementer opened the PR and moved Linear to In Review.
- reviewer_pass_requested: this turn is the reviewer pass.
- review_posted: you posted structured GitHub review feedback.
- implementer_response_attempted: reserved for one bounded follow-up turn by the implementer-agent.
- final_human_merge_gate: humans own approval and merge.
- needs_human: failure, timeout, ambiguity, missing PR data, or judgment-heavy decisions require an operator.

Inputs to inspect:
- PR diff: gh pr diff %[2]s
- PR metadata, CI status, and merge state: gh pr view %[2]s --json title,url,body,author,headRefName,baseRefName,statusCheckRollup,mergeStateStatus,reviewDecision,files,commits,comments,reviews
- Linear issue %[1]s and comments, including the implementer closeout, through the linear_graphql tool.
- Repo docs: CLAUDE.md, CONTEXT.md, PRD-v0.md, docs/adr/, docs/agents/.

Post a structured GitHub review with gh pr review %[2]s --comment --body-file <file>. The body must start with:

> _Reviewer-agent feedback for %[1]s._

Focus on correctness, regressions, missing tests, and unmet acceptance criteria. Clearly separate actionable findings from observations. Do not use GitHub approval or request-changes states; final merge remains human-owned.

Also post a Linear comment that starts with:

> _Reviewer-agent feedback posted for %[1]s._

Write %[3]s before exiting. It must be JSON:
{
  "state": "review_posted" | "final_human_merge_gate" | "needs_human",
  "actionable_comments": true | false,
  "needs_human": true | false,
  "summary": "one-sentence operator summary"
}

Set actionable_comments=true only when the implementer-agent could address concrete review comments in one bounded continuation turn. Set needs_human=true for ambiguous CI, missing PR/Linear context, product or architecture decisions, or anything requiring human judgment.
`, req.Issue.Identifier, req.PullRequest, resultPath)
}

func renderImplementerReviewResponsePrompt(req ReviewLoopRequest) string {
	return fmt.Sprintf(`# Spur implementer-agent bounded review response: %[1]s

You are the implementer-agent responding to reviewer-agent feedback on PR %[2]s. You have exactly one bounded continuation turn.

Inspect:
- gh pr view %[2]s --json comments,reviews,reviewThreads,statusCheckRollup,url
- gh pr diff %[2]s
- Linear issue %[1]s and comments through linear_graphql
- Relevant repo docs and tests.

Address only concrete reviewer-agent findings that can be completed safely in this one turn. Do not broaden scope, do not run another review cycle, do not approve, and never merge.

After any changes, run the relevant verification, push the branch, and post both:
- a GitHub PR comment starting with > _Implementer-agent response for %[1]s._
- a Linear comment starting with > _Implementer-agent response attempted for %[1]s._

If a finding needs human judgment or cannot be completed in this bounded turn, leave it explicitly unresolved in both comments. Final merge remains human-owned.
`, req.Issue.Identifier, req.PullRequest)
}

func shellQuoteForReview(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
