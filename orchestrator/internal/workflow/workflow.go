// Package workflow loads and validates the repository-owned WORKFLOW.md file
// that defines the per-issue agent prompt and runtime configuration.
// Symphony spec §5.
package workflow

// Definition is the parsed contents of WORKFLOW.md. Symphony spec §4.1.2.
//
// Config holds the raw YAML front matter root object — typed access goes
// through a ServiceConfig view (see package config). PromptTemplate holds
// the Markdown body, trimmed.
type Definition struct {
	Config         map[string]any
	PromptTemplate string
}
