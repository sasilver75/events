package workflow

import (
	"strings"
	"testing"

	"github.com/sasilver75/events/orchestrator/internal/domain"
)

func TestRender_BasicSubstitution(t *testing.T) {
	t.Parallel()
	tmpl := "Hello {{ issue.identifier }}: {{ issue.title }}"
	out, err := Render(tmpl, domain.Issue{
		Identifier: "SAM-12",
		Title:      "Test ticket",
	}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "Hello SAM-12: Test ticket" {
		t.Errorf("Render = %q", out)
	}
}

func TestRender_AttemptConditional(t *testing.T) {
	t.Parallel()
	tmpl := `{% if attempt %}retry {{ attempt }}{% else %}first run{% endif %}`

	first, _ := Render(tmpl, domain.Issue{}, nil)
	if first != "first run" {
		t.Errorf("first run = %q", first)
	}

	n := 2
	retry, _ := Render(tmpl, domain.Issue{}, &n)
	if retry != "retry 2" {
		t.Errorf("retry = %q", retry)
	}
}

func TestRender_LabelsAndBlockers(t *testing.T) {
	t.Parallel()
	tmpl := `Labels: {{ issue.labels | join: ", " }}; Blocked by: {{ issue.blocked_by | map: "identifier" | join: ", " }}`
	out, err := Render(tmpl, domain.Issue{
		Labels: []string{"afk", "feature", "area-ios"},
		BlockedBy: []domain.Blocker{
			{Identifier: "SAM-7"},
			{Identifier: "SAM-9"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "Labels: afk, feature, area-ios; Blocked by: SAM-7, SAM-9"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRender_RealWorkflowTemplate(t *testing.T) {
	t.Parallel()
	def, err := Load("../../../WORKFLOW.md")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	priority := 2
	out, err := Render(def.PromptTemplate, domain.Issue{
		ID:          "abc-xyz-uuid",
		Identifier:  "SAM-12",
		Title:       "Add post-event feedback flow",
		Description: "Implement the post-event feedback flow end-to-end.",
		Priority:    &priority,
		State:       "Ready",
		URL:         "https://linear.app/samcorp/issue/SAM-12",
		Labels:      []string{"afk", "feature", "area-server"},
		BlockedBy:   []domain.Blocker{{Identifier: "SAM-7", State: "Done"}},
	}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "SAM-12") {
		t.Errorf("rendered output missing SAM-12: %s", firstN(out, 200))
	}
	if !strings.Contains(out, "Add post-event feedback flow") {
		t.Error("rendered output missing title")
	}
}

func firstN(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
