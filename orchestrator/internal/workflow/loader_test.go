package workflow

import (
	"errors"
	"strings"
	"testing"
)

func TestParse_FrontMatterAndBody(t *testing.T) {
	t.Parallel()
	in := []byte(`---
tracker:
  kind: linear
  project_slug: spur
agent:
  max_concurrent_agents: 2
---

# Task: {{ issue.identifier }}

Body text here.
`)
	def, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if def.Config == nil {
		t.Fatal("Config is nil")
	}
	tracker, ok := def.Config["tracker"].(map[string]any)
	if !ok {
		t.Fatalf("tracker not a map: %T", def.Config["tracker"])
	}
	if got := tracker["kind"]; got != "linear" {
		t.Errorf("tracker.kind = %v, want linear", got)
	}
	if !strings.HasPrefix(def.PromptTemplate, "# Task:") {
		t.Errorf("PromptTemplate doesn't start with body header: %q", def.PromptTemplate[:min(40, len(def.PromptTemplate))])
	}
	if strings.Contains(def.PromptTemplate, "---") {
		t.Error("PromptTemplate should not contain the closing fence")
	}
}

func TestParse_NoFrontMatter(t *testing.T) {
	t.Parallel()
	in := []byte("# Hello\n\nNo front matter here.\n")
	def, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(def.Config) != 0 {
		t.Errorf("Config should be empty, got %v", def.Config)
	}
	if !strings.HasPrefix(def.PromptTemplate, "# Hello") {
		t.Errorf("PromptTemplate = %q", def.PromptTemplate)
	}
}

func TestParse_EmptyFrontMatter(t *testing.T) {
	t.Parallel()
	in := []byte("---\n---\nbody\n")
	def, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(def.Config) != 0 {
		t.Errorf("Config should be empty for empty front matter, got %v", def.Config)
	}
	if def.PromptTemplate != "body" {
		t.Errorf("PromptTemplate = %q, want %q", def.PromptTemplate, "body")
	}
}

func TestParse_FrontMatterMustBeMap(t *testing.T) {
	t.Parallel()
	in := []byte("---\n- a\n- b\n---\nbody\n")
	_, err := Parse(in)
	if !errors.Is(err, ErrFrontMatterNotMap) {
		t.Errorf("Parse() err = %v, want ErrFrontMatterNotMap", err)
	}
}

func TestParse_MalformedYAML(t *testing.T) {
	t.Parallel()
	in := []byte("---\nkey: : :\n---\nbody\n")
	_, err := Parse(in)
	if !errors.Is(err, ErrWorkflowParse) {
		t.Errorf("Parse() err = %v, want ErrWorkflowParse", err)
	}
}

func TestParse_TrimsBody(t *testing.T) {
	t.Parallel()
	in := []byte("---\nkey: value\n---\n\n\n  body  \n\n")
	def, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if def.PromptTemplate != "body" {
		t.Errorf("PromptTemplate = %q, want %q (trimmed)", def.PromptTemplate, "body")
	}
}

func TestLoad_RealWorkflowFile(t *testing.T) {
	t.Parallel()
	// Sanity-check that the repo's WORKFLOW.md parses cleanly.
	def, err := Load("../../../WORKFLOW.md")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tracker, ok := def.Config["tracker"].(map[string]any)
	if !ok {
		t.Fatalf("repo WORKFLOW.md: tracker section missing or wrong type: %T", def.Config["tracker"])
	}
	if got := tracker["kind"]; got != "linear" {
		t.Errorf("repo WORKFLOW.md: tracker.kind = %v, want linear", got)
	}
	if def.PromptTemplate == "" {
		t.Error("repo WORKFLOW.md: prompt template is empty")
	}
}
