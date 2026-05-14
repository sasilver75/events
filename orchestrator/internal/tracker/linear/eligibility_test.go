package linear

import (
	"testing"

	"github.com/sasilver75/events/orchestrator/internal/domain"
)

func TestEligibilityFilter_SpurDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		issue  domain.Issue
		want   bool
		reason string
	}{
		{
			name:  "AFK label, no HITL, no blockers",
			issue: domain.Issue{Identifier: "SAM-1", Labels: []string{"afk", "feature"}},
			want:  true,
		},
		{
			name:   "missing AFK label",
			issue:  domain.Issue{Identifier: "SAM-2", Labels: []string{"feature"}},
			want:   false,
			reason: "missing_required_label:afk",
		},
		{
			name:   "has HITL label",
			issue:  domain.Issue{Identifier: "SAM-3", Labels: []string{"afk", "hitl"}},
			want:   false,
			reason: "has_excluded_label:hitl",
		},
		{
			name: "AFK with all blockers Done",
			issue: domain.Issue{
				Identifier: "SAM-4",
				Labels:     []string{"afk"},
				BlockedBy: []domain.Blocker{
					{Identifier: "SAM-1", State: "Done"},
					{Identifier: "SAM-2", State: "Done"},
				},
			},
			want: true,
		},
		{
			name: "AFK with open blocker",
			issue: domain.Issue{
				Identifier: "SAM-5",
				Labels:     []string{"afk"},
				BlockedBy: []domain.Blocker{
					{Identifier: "SAM-1", State: "Done"},
					{Identifier: "SAM-2", State: "In Progress"},
				},
			},
			want:   false,
			reason: "blocked_by_open:SAM-2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eligible, rejected := SpurDefault.Apply([]domain.Issue{tc.issue})
			gotEligible := len(eligible) == 1
			if gotEligible != tc.want {
				t.Errorf("eligible = %v, want %v (rejections: %v)", gotEligible, tc.want, rejected)
			}
			if !tc.want && len(rejected) == 1 && rejected[0].Reason != tc.reason {
				t.Errorf("reason = %q, want %q", rejected[0].Reason, tc.reason)
			}
		})
	}
}

func TestEligibilityFilter_AssigneeCriterion(t *testing.T) {
	t.Parallel()
	filter := SpurDefault
	filter.CurrentUserID = "user-current"

	cases := []struct {
		name       string
		assigneeID string
		want       bool
		reason     string
	}{
		{
			name: "unassigned",
			want: true,
		},
		{
			name:       "assigned to current user",
			assigneeID: "user-current",
			want:       true,
		},
		{
			name:       "assigned to another user",
			assigneeID: "user-other",
			want:       false,
			reason:     "assigned_to_other:user-other",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issue := domain.Issue{Identifier: "SAM-1", Labels: []string{"afk"}, AssigneeID: tc.assigneeID}
			eligible, rejected := filter.Apply([]domain.Issue{issue})
			gotEligible := len(eligible) == 1
			if gotEligible != tc.want {
				t.Fatalf("eligible = %v, want %v (rejections: %v)", gotEligible, tc.want, rejected)
			}
			if !tc.want && (len(rejected) != 1 || rejected[0].Reason != tc.reason) {
				t.Fatalf("rejected = %+v, want reason %q", rejected, tc.reason)
			}
		})
	}
}

func TestEligibilityFilter_RejectsAssignedIssueWhenCurrentUserUnknown(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{Identifier: "SAM-1", Labels: []string{"afk"}, AssigneeID: "user-current"}

	eligible, rejected := SpurDefault.Apply([]domain.Issue{issue})
	if len(eligible) != 0 {
		t.Fatalf("eligible = %+v, want none", eligible)
	}
	if len(rejected) != 1 || rejected[0].Reason != "assignee_current_user_unknown" {
		t.Fatalf("rejected = %+v, want assignee_current_user_unknown", rejected)
	}
}
