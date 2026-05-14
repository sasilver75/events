// Package domain holds the normalized data model that the orchestrator,
// trackers, workspaces, and agent runner all agree on. Types follow
// OpenAI's Symphony specification §4.
//
// Reference: https://github.com/openai/symphony/blob/main/SPEC.md
package domain

import (
	"slices"
	"time"
)

// Issue is the normalized issue record used by orchestration, prompt
// rendering, and observability output. Symphony spec §4.1.1.
//
// Fields documented in the spec as "string or null" are represented as
// the zero value of their type (empty string, zero time.Time). Priority
// is a pointer because 0 is a valid Linear priority value distinguishable
// from "no priority set".
type Issue struct {
	// ID is the stable tracker-internal identifier (used for lookups
	// and map keys). For Linear this is the GraphQL node ID (UUID).
	ID string

	// Identifier is the human-readable ticket key (e.g. "SAM-12").
	// Use for logs and workspace naming.
	Identifier string

	Title       string
	Description string
	Priority    *int
	State       string
	BranchName  string
	URL         string
	AssigneeID  string

	// Labels are normalized to lowercase by the tracker adapter
	// (spec §11.3). The orchestrator and agent runner can compare
	// against lowercase strings directly without re-normalizing.
	Labels []string

	// BlockedBy lists issues that must reach a terminal state before
	// this one is dispatch-eligible (spec §8.2 blocker rule).
	BlockedBy []Blocker

	CreatedAt time.Time
	UpdatedAt time.Time
}

// HasLabel reports whether the issue carries the given label.
// Comparison is case-insensitive against the labels field (which the
// tracker adapter has already lowercased).
func (i Issue) HasLabel(name string) bool {
	return slices.Contains(i.Labels, name)
}

// Blocker is a reference to another issue that blocks this one.
// Symphony spec §4.1.1 (the "blocker refs" sub-record of blocked_by).
type Blocker struct {
	ID         string
	Identifier string
	State      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
