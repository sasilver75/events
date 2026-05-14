package linear

import (
	"strings"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/domain"
)

// rawIssue is the wire shape returned by Linear's GraphQL. We unmarshal
// into this and then call normalize() to produce a domain.Issue. Keeping
// the wire shape distinct from the domain shape isolates schema drift.
type rawIssue struct {
	ID          string  `json:"id"`
	Identifier  string  `json:"identifier"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Priority    float64 `json:"priority"`
	BranchName  string  `json:"branchName"`
	URL         string  `json:"url"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`

	Assignee *struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"assignee"`

	State *struct {
		Name string `json:"name"`
	} `json:"state"`

	Labels *struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`

	InverseRelations *struct {
		Nodes []struct {
			Type  string `json:"type"`
			Issue *struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
				State      *struct {
					Name string `json:"name"`
				} `json:"state"`
				CreatedAt string `json:"createdAt"`
				UpdatedAt string `json:"updatedAt"`
			} `json:"issue"`
		} `json:"nodes"`
	} `json:"inverseRelations"`
}

// normalize converts a rawIssue into a domain.Issue per spec §11.3.
//
// Normalization rules (verbatim from spec):
//   - labels → lowercase strings
//   - blocked_by → derived from inverse relations where relation type is "blocks"
//   - priority → integer only (non-integers become null) — Linear sends 0..4
//   - createdAt / updatedAt → parse ISO-8601 timestamps
func (r rawIssue) normalize() domain.Issue {
	issue := domain.Issue{
		ID:          r.ID,
		Identifier:  r.Identifier,
		Title:       r.Title,
		Description: r.Description,
		BranchName:  r.BranchName,
		URL:         r.URL,
		CreatedAt:   parseTime(r.CreatedAt),
		UpdatedAt:   parseTime(r.UpdatedAt),
	}
	if r.State != nil {
		issue.State = r.State.Name
	}
	if r.Assignee != nil {
		issue.AssigneeID = r.Assignee.ID
	}

	// Priority: Linear's API returns it as a Float in GraphQL even when the
	// underlying value is integer 0..4. Per spec §11.3 only integer values
	// pass through; anything fractional becomes nil.
	if r.Priority == float64(int(r.Priority)) {
		p := int(r.Priority)
		issue.Priority = &p
	}

	if r.Labels != nil {
		issue.Labels = make([]string, 0, len(r.Labels.Nodes))
		for _, n := range r.Labels.Nodes {
			issue.Labels = append(issue.Labels, strings.ToLower(n.Name))
		}
	}

	if r.InverseRelations != nil {
		for _, rel := range r.InverseRelations.Nodes {
			if rel.Type != "blocks" || rel.Issue == nil {
				continue
			}
			b := domain.Blocker{
				ID:         rel.Issue.ID,
				Identifier: rel.Issue.Identifier,
				CreatedAt:  parseTime(rel.Issue.CreatedAt),
				UpdatedAt:  parseTime(rel.Issue.UpdatedAt),
			}
			if rel.Issue.State != nil {
				b.State = rel.Issue.State.Name
			}
			issue.BlockedBy = append(issue.BlockedBy, b)
		}
	}

	return issue
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
