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
