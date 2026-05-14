package workflow

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ServiceConfig is the typed view of Definition.Config. Symphony spec §4.1.3
// and §6.4. Built once at load time and then read by every component that
// needs config (orchestrator, tracker adapter, workspace manager, agent runner).
//
// The cheat-sheet of all fields lives in spec §6.4. Anything not listed here
// is either deferred (we don't need it for v0) or an extension key the spec
// permits but doesn't require.
type ServiceConfig struct {
	Tracker     TrackerConfig
	Polling     PollingConfig
	Workspace   WorkspaceConfig
	Hooks       HooksConfig
	Agent       AgentConfig
	Credentials CredentialsConfig
	Codex       CodexConfig
}

// TrackerConfig is the typed view of `tracker.*`. Symphony spec §5.3.1.
type TrackerConfig struct {
	Kind           string // currently "linear"
	Endpoint       string
	APIKeyEnv      string // env var name for "$VAR_NAME" references
	APIKeyLiteral  string // literal api_key value when WORKFLOW.md uses one
	ProjectSlug    string
	ActiveStates   []string
	TerminalStates []string
}

// PollingConfig is the typed view of `polling.*`. Symphony spec §5.3.2.
type PollingConfig struct {
	IntervalMs int
}

// WorkspaceConfig is the typed view of `workspace.*`. Symphony spec §5.3.3,
// plus the Spur-specific `base_image` extension (see docs/agents/harness.md).
type WorkspaceConfig struct {
	Root      string
	BaseImage string // Spur extension: the Tart VM to clone per issue
}

// HooksConfig holds the four lifecycle hook scripts as raw strings.
// Symphony spec §5.3.4.
type HooksConfig struct {
	AfterCreate  string
	BeforeRun    string
	AfterRun     string
	BeforeRemove string
	TimeoutMs    int
}

// AgentConfig is the typed view of `agent.*`. Symphony spec §5.3.5.
type AgentConfig struct {
	MaxConcurrentAgents    int
	MaxTurns               int
	MaxRetryBackoffMs      int
	MaxUnproductiveSuccess int
	Runner                 string // currently only "codex" is supported
}

// CredentialsConfig is a Spur extension that makes the current secret boundary
// explicit. `vm_env` injects tracker credentials into the VM; `host_proxy`
// keeps the Linear token on the host and is only valid for the Codex runner.
type CredentialsConfig struct {
	LinearAccess string
}

// CodexConfig is the typed view of Symphony's reference `codex` section.
type CodexConfig struct {
	Command        string
	TurnTimeoutMs  int
	ReadTimeoutMs  int
	StallTimeoutMs int
}

// Validation error categories. Symphony spec §6.3.
var (
	ErrUnsupportedTracker      = errors.New("unsupported_tracker_kind")
	ErrUnsupportedRunner       = errors.New("unsupported_agent_runner")
	ErrMissingTrackerKind      = errors.New("missing_tracker_kind")
	ErrMissingProjectSlug      = errors.New("missing_tracker_project_slug")
	ErrMissingCodexCommand     = errors.New("missing_codex_command")
	ErrMissingTrackerAPIKey    = errors.New("missing_tracker_api_key")
	ErrUnsupportedLinearAccess = errors.New("unsupported_linear_access_mode")
)

// NewServiceConfig builds a ServiceConfig from a parsed Definition's raw
// front-matter map. Symphony spec §6 (Source Precedence and Resolution).
// Applies defaults from §6.4.
func NewServiceConfig(raw map[string]any) ServiceConfig {
	cfg := ServiceConfig{
		Tracker: TrackerConfig{
			Endpoint:       "https://api.linear.app/graphql",
			ActiveStates:   []string{"Todo", "In Progress"},
			TerminalStates: []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"},
		},
		Polling:   PollingConfig{IntervalMs: 30000},
		Workspace: WorkspaceConfig{Root: defaultWorkspaceRoot()},
		Hooks:     HooksConfig{TimeoutMs: 60000},
		Agent: AgentConfig{
			MaxConcurrentAgents:    10,
			MaxTurns:               20,
			MaxRetryBackoffMs:      300000,
			MaxUnproductiveSuccess: 3,
			Runner:                 "codex",
		},
		Credentials: CredentialsConfig{LinearAccess: "host_proxy"},
		Codex: CodexConfig{
			Command:        "codex app-server",
			TurnTimeoutMs:  3600000,
			ReadTimeoutMs:  5000,
			StallTimeoutMs: 300000,
		},
	}

	if t, ok := raw["tracker"].(map[string]any); ok {
		cfg.Tracker.Kind = stringField(t, "kind")
		if v := stringField(t, "endpoint"); v != "" {
			cfg.Tracker.Endpoint = v
		}
		cfg.Tracker.APIKeyLiteral, cfg.Tracker.APIKeyEnv = parseAPIKeyRef(stringField(t, "api_key"))
		cfg.Tracker.ProjectSlug = stringField(t, "project_slug")
		if states := stringSliceField(t, "active_states"); states != nil {
			cfg.Tracker.ActiveStates = states
		}
		if states := stringSliceField(t, "terminal_states"); states != nil {
			cfg.Tracker.TerminalStates = states
		}
	}

	if p, ok := raw["polling"].(map[string]any); ok {
		if v := intField(p, "interval_ms"); v > 0 {
			cfg.Polling.IntervalMs = v
		}
	}

	if w, ok := raw["workspace"].(map[string]any); ok {
		if v := stringField(w, "root"); v != "" {
			cfg.Workspace.Root = v
		}
		cfg.Workspace.BaseImage = stringField(w, "base_image")
	}

	if h, ok := raw["hooks"].(map[string]any); ok {
		cfg.Hooks.AfterCreate = stringField(h, "after_create")
		cfg.Hooks.BeforeRun = stringField(h, "before_run")
		cfg.Hooks.AfterRun = stringField(h, "after_run")
		cfg.Hooks.BeforeRemove = stringField(h, "before_remove")
		if v := intField(h, "timeout_ms"); v > 0 {
			cfg.Hooks.TimeoutMs = v
		}
	}

	if a, ok := raw["agent"].(map[string]any); ok {
		if v := intField(a, "max_concurrent_agents"); v > 0 {
			cfg.Agent.MaxConcurrentAgents = v
		}
		if v := intField(a, "max_turns"); v > 0 {
			cfg.Agent.MaxTurns = v
		}
		if v := intField(a, "max_retry_backoff_ms"); v > 0 {
			cfg.Agent.MaxRetryBackoffMs = v
		}
		if v := intField(a, "max_unproductive_successes"); v > 0 {
			cfg.Agent.MaxUnproductiveSuccess = v
		}
		if v := stringField(a, "runner"); v != "" {
			cfg.Agent.Runner = v
		}
	}

	if c, ok := raw["credentials"].(map[string]any); ok {
		if v := stringField(c, "linear_access"); v != "" {
			cfg.Credentials.LinearAccess = v
		}
	}

	if c, ok := raw["codex"].(map[string]any); ok {
		if v := stringField(c, "command"); v != "" {
			cfg.Codex.Command = v
		}
		if v := intField(c, "turn_timeout_ms"); v > 0 {
			cfg.Codex.TurnTimeoutMs = v
		}
		if v := intField(c, "read_timeout_ms"); v > 0 {
			cfg.Codex.ReadTimeoutMs = v
		}
		if v := intField(c, "stall_timeout_ms"); v > 0 {
			cfg.Codex.StallTimeoutMs = v
		}
	}

	return cfg
}

// Validate runs the dispatch preflight checks from spec §6.3.
func (c ServiceConfig) Validate() error {
	if c.Tracker.Kind == "" {
		return ErrMissingTrackerKind
	}
	if c.Tracker.Kind != "linear" {
		return fmt.Errorf("%w: %q", ErrUnsupportedTracker, c.Tracker.Kind)
	}
	if c.Tracker.Kind == "linear" && c.Tracker.ProjectSlug == "" {
		return ErrMissingProjectSlug
	}
	switch c.LinearAccessMode() {
	case "vm_env":
	case "host_proxy":
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedLinearAccess, c.Credentials.LinearAccess)
	}
	switch c.AgentRunnerName() {
	case "codex":
		if c.Codex.Command == "" {
			return ErrMissingCodexCommand
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedRunner, c.Agent.Runner)
	}
	if _, err := c.ResolveTrackerAPIKey(); err != nil {
		return err
	}
	return nil
}

// ResolveTrackerAPIKey applies the same env-aware resolution rule used by the
// CLI and validation. It supports Symphony's literal api_key form and "$VAR"
// references, with LINEAR_API_KEY as Spur's historical fallback.
func (c ServiceConfig) ResolveTrackerAPIKey() (string, error) {
	if c.Tracker.APIKeyLiteral != "" {
		return c.Tracker.APIKeyLiteral, nil
	}
	return resolveEnv(c.Tracker.APIKeyEnv, "LINEAR_API_KEY")
}

func resolveEnv(preferredVar, fallbackVar string) (string, error) {
	if preferredVar != "" {
		if v := os.Getenv(preferredVar); v != "" {
			return v, nil
		}
	}
	if v := os.Getenv(fallbackVar); v != "" {
		return v, nil
	}
	if preferredVar == "" || preferredVar == fallbackVar {
		return "", fmt.Errorf("%w: env var %s is not set", ErrMissingTrackerAPIKey, fallbackVar)
	}
	return "", fmt.Errorf("%w: env var %s (or %s) is not set", ErrMissingTrackerAPIKey, preferredVar, fallbackVar)
}

func (c ServiceConfig) AgentCommand() string {
	return c.Codex.Command
}

func (c ServiceConfig) AgentTurnTimeoutMs() int {
	return c.Codex.TurnTimeoutMs
}

func (c ServiceConfig) AgentStallTimeoutMs() int {
	return c.Codex.StallTimeoutMs
}

func (c ServiceConfig) AgentRunnerName() string {
	if c.Agent.Runner == "" {
		return "codex"
	}
	return c.Agent.Runner
}

func (c ServiceConfig) LinearAccessMode() string {
	if c.Credentials.LinearAccess == "" {
		return "host_proxy"
	}
	return c.Credentials.LinearAccess
}

// stringField extracts a string field from a YAML-decoded map, returning ""
// if absent or wrong type.
func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// intField extracts an integer field. YAML decodes numbers as int by default,
// but supports string-int per spec §5.3.5 ("integer or string integer").
func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

// stringSliceField extracts a list of strings. Returns nil if absent so
// callers can distinguish "use default" from "explicitly empty".
func stringSliceField(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// parseAPIKeyRef splits the tracker api_key into its literal or env-reference
// representation. Spec §5.3.1: api_key may be a literal token or "$VAR_NAME".
func parseAPIKeyRef(s string) (literal, env string) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '$' {
		return "", s[1:]
	}
	return s, ""
}

func defaultWorkspaceRoot() string {
	// Spec §5.3.3 default is <system-temp>/symphony_workspaces. For Spur,
	// the workspace root is conceptual (Tart manages its own registry),
	// so we use ~/.tart/vms as a documentation pointer.
	return "~/.tart/vms"
}
