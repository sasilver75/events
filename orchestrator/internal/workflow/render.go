package workflow

import (
	"fmt"

	"github.com/osteele/liquid"

	"github.com/sasilver75/events/orchestrator/internal/domain"
)

// Render produces the per-issue prompt by interpolating the WORKFLOW.md
// prompt template with the given issue and attempt count. Symphony spec §5.4.
//
// Template input variables (per spec §5.4):
//   - issue (object) — all normalized issue fields.
//   - attempt (integer or null) — nil/absent on first attempt.
//
// Rendering uses strict variable and filter checking — unknown variables
// or filters cause render errors (per spec §5.4 "Unknown variables must
// fail rendering").
func Render(template string, issue domain.Issue, attempt *int) (string, error) {
	engine := liquid.NewEngine()

	bindings := map[string]any{
		"issue":   issueToBindings(issue),
		"attempt": attemptToBinding(attempt),
	}

	out, err := engine.ParseAndRenderString(template, bindings)
	if err != nil {
		return "", fmt.Errorf("template_render_error: %w", err)
	}
	return out, nil
}

// RenderAgentPrompt returns the prompt that should be sent for a worker turn.
// First turns use the full WORKFLOW.md prompt. Continuations on an existing
// Codex session get a concise overlay that points the agent at the remaining
// handoff work instead of replaying the whole task contract.
func RenderAgentPrompt(template string, issue domain.Issue, attempt *int, resumeSessionID string) (string, error) {
	if resumeSessionID == "" {
		return Render(template, issue, attempt)
	}
	return RenderContinuationPrompt(issue, attempt, resumeSessionID), nil
}

func RenderContinuationPrompt(issue domain.Issue, attempt *int, resumeSessionID string) string {
	attemptNumber := 1
	if attempt != nil {
		attemptNumber = *attempt
	}

	out := "# Spur continuation: " + issue.Identifier + "\n\n"
	out += "You are resuming an existing Codex thread for Linear issue " + issue.Identifier + ": " + issue.Title + ".\n"
	if issue.URL != "" {
		out += "Issue URL: " + issue.URL + "\n"
	}
	if issue.BranchName != "" {
		out += "Expected branch: " + issue.BranchName + "\n"
	}
	if issue.State != "" {
		out += "Tracker state at dispatch: " + issue.State + "\n"
	}
	out += "Resume session: " + resumeSessionID + "\n"
	out += "Continuation attempt: " + fmt.Sprint(attemptNumber) + "\n\n"
	out += "Use the prior thread context and current repository state as authoritative. Do not restart the task or re-run the full original workflow unless current evidence shows it is necessary.\n\n"
	out += "Finish only the missing work needed to hand off this issue:\n"
	out += "1. Confirm the implementation and verification are complete for " + issue.Identifier + ".\n"
	out += "2. Ensure there is a ready-for-review PR against main and that the PR title includes " + issue.Identifier + ".\n"
	out += "3. Post the Linear closeout comment with the PR link, acceptance-criteria evidence, drift from spec if any, and artifacts if any.\n"
	out += "4. Move the Linear issue to In Review.\n\n"
	out += "If a required artifact already exists, do not duplicate it. Draft PRs are only for explicitly requested drafts, incomplete work, blockers, partial handoffs, or known human-decision needs; explain any draft condition in the Linear handoff. If you discover a genuine blocker, post the blocker details on Linear and transition the issue to Needs Human.\n"
	return out
}

func issueToBindings(i domain.Issue) map[string]any {
	bindings := map[string]any{
		"id":          i.ID,
		"identifier":  i.Identifier,
		"title":       i.Title,
		"description": i.Description,
		"state":       i.State,
		"branch_name": i.BranchName,
		"url":         i.URL,
		"labels":      i.Labels,
		"blocked_by":  blockersToBindings(i.BlockedBy),
		"created_at":  i.CreatedAt,
		"updated_at":  i.UpdatedAt,
	}
	if i.Priority != nil {
		bindings["priority"] = *i.Priority
	} else {
		bindings["priority"] = nil
	}
	return bindings
}

func blockersToBindings(bs []domain.Blocker) []map[string]any {
	out := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		out = append(out, map[string]any{
			"id":         b.ID,
			"identifier": b.Identifier,
			"state":      b.State,
			"created_at": b.CreatedAt,
			"updated_at": b.UpdatedAt,
		})
	}
	return out
}

func attemptToBinding(attempt *int) any {
	if attempt == nil {
		return nil
	}
	return *attempt
}
