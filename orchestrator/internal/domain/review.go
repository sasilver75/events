package domain

// ReviewState is the operator-facing lifecycle for reviewer-agent work on a
// harness-created PR. It is intentionally separate from Linear workflow state:
// Linear remains the source of truth for issue ownership, while this model
// describes the bounded PR review loop.
type ReviewState string

const (
	ReviewStateImplementationComplete     ReviewState = "implementation_complete"
	ReviewStateReviewerPassRequested      ReviewState = "reviewer_pass_requested"
	ReviewStateReviewPosted               ReviewState = "review_posted"
	ReviewStateImplementerResponseAttempt ReviewState = "implementer_response_attempted"
	ReviewStateFinalHumanMergeGate        ReviewState = "final_human_merge_gate"
	ReviewStateNeedsHuman                 ReviewState = "needs_human"
)
