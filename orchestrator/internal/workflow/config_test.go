package workflow

import (
	"errors"
	"testing"
)

func TestNewServiceConfig_AppliesDefaults(t *testing.T) {
	t.Parallel()
	cfg := NewServiceConfig(map[string]any{})

	if cfg.Tracker.Endpoint != "https://api.linear.app/graphql" {
		t.Errorf("Tracker.Endpoint default = %q", cfg.Tracker.Endpoint)
	}
	if cfg.Polling.IntervalMs != 30000 {
		t.Errorf("Polling.IntervalMs default = %d", cfg.Polling.IntervalMs)
	}
	if cfg.Agent.MaxConcurrentAgents != 10 {
		t.Errorf("Agent.MaxConcurrentAgents default = %d", cfg.Agent.MaxConcurrentAgents)
	}
	if cfg.Hooks.TimeoutMs != 60000 {
		t.Errorf("Hooks.TimeoutMs default = %d", cfg.Hooks.TimeoutMs)
	}
}

func TestNewServiceConfig_OverridesFromFrontMatter(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"tracker": map[string]any{
			"kind":         "linear",
			"api_key":      "$LINEAR_API_KEY",
			"project_slug": "spur-c956b9432c2f",
			"active_states": []any{
				"Ready",
				"In Progress",
			},
			"terminal_states": []any{
				"Done",
				"Canceled",
				"Duplicate",
			},
		},
		"polling": map[string]any{"interval_ms": 15000},
		"workspace": map[string]any{
			"root":       "~/.tart/vms",
			"base_image": "spur-base",
		},
		"agent": map[string]any{
			"max_concurrent_agents": 2,
		},
	}
	cfg := NewServiceConfig(raw)

	if cfg.Tracker.Kind != "linear" {
		t.Errorf("Tracker.Kind = %q", cfg.Tracker.Kind)
	}
	if cfg.Tracker.APIKeyEnv != "LINEAR_API_KEY" {
		t.Errorf("Tracker.APIKeyEnv = %q, want LINEAR_API_KEY", cfg.Tracker.APIKeyEnv)
	}
	if cfg.Tracker.ProjectSlug != "spur-c956b9432c2f" {
		t.Errorf("Tracker.ProjectSlug = %q", cfg.Tracker.ProjectSlug)
	}
	if len(cfg.Tracker.ActiveStates) != 2 || cfg.Tracker.ActiveStates[0] != "Ready" {
		t.Errorf("Tracker.ActiveStates = %v", cfg.Tracker.ActiveStates)
	}
	if cfg.Polling.IntervalMs != 15000 {
		t.Errorf("Polling.IntervalMs = %d", cfg.Polling.IntervalMs)
	}
	if cfg.Workspace.BaseImage != "spur-base" {
		t.Errorf("Workspace.BaseImage = %q", cfg.Workspace.BaseImage)
	}
	if cfg.Agent.MaxConcurrentAgents != 2 {
		t.Errorf("Agent.MaxConcurrentAgents = %d", cfg.Agent.MaxConcurrentAgents)
	}
}

func TestServiceConfig_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  ServiceConfig
		want error
	}{
		{
			name: "missing tracker kind",
			cfg:  ServiceConfig{},
			want: ErrMissingTrackerKind,
		},
		{
			name: "unsupported tracker",
			cfg:  ServiceConfig{Tracker: TrackerConfig{Kind: "github"}},
			want: ErrUnsupportedTracker,
		},
		{
			name: "missing project slug",
			cfg:  ServiceConfig{Tracker: TrackerConfig{Kind: "linear"}},
			want: ErrMissingProjectSlug,
		},
		{
			name: "missing claudecode command",
			cfg: ServiceConfig{
				Tracker: TrackerConfig{Kind: "linear", ProjectSlug: "spur"},
			},
			want: ErrMissingCodexCommand,
		},
		{
			name: "valid",
			cfg: ServiceConfig{
				Tracker:    TrackerConfig{Kind: "linear", ProjectSlug: "spur"},
				ClaudeCode: ClaudeCodeConfig{Command: "claude --print"},
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if !errors.Is(err, tc.want) {
				t.Errorf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestNewServiceConfig_RealWorkflowFile validates the repo's WORKFLOW.md
// loads to a config that passes Validate.
func TestNewServiceConfig_RealWorkflowFile(t *testing.T) {
	t.Parallel()
	def, err := Load("../../../WORKFLOW.md")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := NewServiceConfig(def.Config)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Tracker.Kind != "linear" {
		t.Errorf("Tracker.Kind = %q", cfg.Tracker.Kind)
	}
	if cfg.Workspace.BaseImage != "spur-base" {
		t.Errorf("Workspace.BaseImage = %q (expected spur-base)", cfg.Workspace.BaseImage)
	}
}
