package linear

import "github.com/sasilver75/events/orchestrator/internal/domain"

// EligibilityFilter encodes Spur's pickup criteria as an overlay on top of
// Symphony's base candidate filter (active state + not running/claimed).
//
// Per docs/agents/triage-labels.md, the agent harness only picks up issues
// that are:
//
//   - state ∈ active_states (already filtered server-side by FetchCandidateIssues)
//   - label "afk" present
//   - label "hitl" absent
//   - all blockers in state "Done"
//   - assignee empty or assigned to the current Linear API user
//
// This is applied client-side after FetchCandidateIssues because Linear's
// GraphQL filter API can't express "all blockers in Done" in one query —
// it can only filter by issue properties, not by joined data.
type EligibilityFilter struct {
	RequireLabel   string   // typically "afk"
	ExcludeLabel   string   // typically "hitl"
	TerminalStates []string // states that count as "blocker resolved" (typically "Done")
	CurrentUserID  string   // Linear viewer ID for the harness API key
}

// SpurDefault is the eligibility filter applied to every spur-agent run.
// Centralized so future changes (e.g. adding additional excluded labels)
// happen in one place.
var SpurDefault = EligibilityFilter{
	RequireLabel:   "afk",
	ExcludeLabel:   "hitl",
	TerminalStates: []string{"Done"},
}

// Apply returns the subset of input that passes the filter, plus a slice
// of reasons (one per rejected issue, in input order, for those that were
// rejected). Reasons are useful for debug logging.
func (f EligibilityFilter) Apply(issues []domain.Issue) (eligible []domain.Issue, rejected []Rejection) {
	terminalSet := make(map[string]struct{}, len(f.TerminalStates))
	for _, s := range f.TerminalStates {
		terminalSet[normalizeStateKey(s)] = struct{}{}
	}

	for _, issue := range issues {
		if f.RequireLabel != "" && !issue.HasLabel(f.RequireLabel) {
			rejected = append(rejected, Rejection{Issue: issue, Reason: "missing_required_label:" + f.RequireLabel})
			continue
		}
		if f.ExcludeLabel != "" && issue.HasLabel(f.ExcludeLabel) {
			rejected = append(rejected, Rejection{Issue: issue, Reason: "has_excluded_label:" + f.ExcludeLabel})
			continue
		}
		if blockerOpen := firstOpenBlocker(issue, terminalSet); blockerOpen != "" {
			rejected = append(rejected, Rejection{Issue: issue, Reason: "blocked_by_open:" + blockerOpen})
			continue
		}
		if issue.AssigneeID != "" {
			if f.CurrentUserID == "" {
				rejected = append(rejected, Rejection{Issue: issue, Reason: "assignee_current_user_unknown"})
				continue
			}
			if issue.AssigneeID != f.CurrentUserID {
				rejected = append(rejected, Rejection{Issue: issue, Reason: "assigned_to_other:" + issue.AssigneeID})
				continue
			}
		}
		eligible = append(eligible, issue)
	}
	return eligible, rejected
}

// Rejection records why an issue didn't pass eligibility, for logs.
type Rejection struct {
	Issue  domain.Issue
	Reason string
}

func firstOpenBlocker(issue domain.Issue, terminalSet map[string]struct{}) string {
	for _, b := range issue.BlockedBy {
		if _, terminal := terminalSet[normalizeStateKey(b.State)]; !terminal {
			return b.Identifier
		}
	}
	return ""
}
