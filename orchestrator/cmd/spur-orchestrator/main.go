// Command spur-orchestrator implements OpenAI's Symphony spec adapted for
// Spur. Polls Linear, dispatches per-issue Tart VM workspaces, runs Claude
// Code agents inside them, reconciles state.
//
// Reference: https://github.com/openai/symphony/blob/main/SPEC.md
// Adaptations: docs/agents/harness.md
//
// Two modes:
//
//	spur-orchestrator              → daemon (poll loop on the interval
//	                                 from WORKFLOW.md polling.interval_ms)
//	spur-orchestrator --once       → single tick; exits after current
//	                                 work completes
//	spur-orchestrator --once --issue SAM-12
//	                               → dispatch one specific issue and exit
//
// Required env vars at startup:
//
//	LINEAR_API_KEY                 → tracker reads
//	SPUR_HARNESS_GITHUB_TOKEN      → injected into per-VM credentials
//	SPUR_HARNESS_LINEAR_BOT_TOKEN  → injected into per-VM credentials
//	                                 (may equal LINEAR_API_KEY if not
//	                                 using a separate bot account)
//	SPUR_HARNESS_SSH_KEY           → path to harness private key
//	                                 (default: ~/.ssh/spur-agent-vm)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent/claudecode"
	"github.com/sasilver75/events/orchestrator/internal/orchestrator"
	"github.com/sasilver75/events/orchestrator/internal/tracker/linear"
	"github.com/sasilver75/events/orchestrator/internal/workflow"
	"github.com/sasilver75/events/orchestrator/internal/workspace/tart"
)

func main() {
	var (
		workflowPath = flag.String("workflow", "WORKFLOW.md", "Path to WORKFLOW.md (Symphony spec §5.1)")
		once         = flag.Bool("once", false, "Dispatch one tick and exit (used by scripts/spur-agent)")
		issue        = flag.String("issue", "", "When --once is set, dispatch only this specific Linear issue identifier (e.g. SAM-12)")
		validate     = flag.Bool("validate", false, "Validate WORKFLOW.md and exit without running")
		verbose      = flag.Bool("verbose", false, "Log at debug level")
	)
	flag.Parse()

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// 1. Load + validate WORKFLOW.md.
	def, err := workflow.Load(*workflowPath)
	if err != nil {
		fatal(logger, "workflow load failed", "path", *workflowPath, "err", err)
	}
	cfg := workflow.NewServiceConfig(def.Config)
	if err := cfg.Validate(); err != nil {
		fatal(logger, "workflow validation failed", "err", err)
	}
	logger.Info("workflow loaded",
		"tracker", cfg.Tracker.Kind,
		"project_slug", cfg.Tracker.ProjectSlug,
		"active_states", cfg.Tracker.ActiveStates,
		"max_concurrent_agents", cfg.Agent.MaxConcurrentAgents,
		"workspace_base_image", cfg.Workspace.BaseImage,
	)
	if *validate {
		fmt.Println("WORKFLOW.md is valid.")
		return
	}

	// 2. Resolve credentials from env.
	linearAPIKey, err := requireEnv(cfg.Tracker.APIKeyEnv, "LINEAR_API_KEY")
	if err != nil {
		fatal(logger, "missing Linear API key", "err", err)
	}
	githubToken := os.Getenv("SPUR_HARNESS_GITHUB_TOKEN")
	linearBotToken := os.Getenv("SPUR_HARNESS_LINEAR_BOT_TOKEN")
	if linearBotToken == "" {
		linearBotToken = linearAPIKey // fall back to the read key if no bot configured
	}
	sshKey := os.Getenv("SPUR_HARNESS_SSH_KEY")
	if sshKey == "" {
		home, _ := os.UserHomeDir()
		sshKey = filepath.Join(home, ".ssh", "spur-agent-vm")
	}
	if _, err := os.Stat(sshKey); err != nil {
		fatal(logger, "harness SSH key not found", "path", sshKey, "err", err)
	}

	// 3. Build the dependency graph.
	tracker, err := linear.New(linear.Config{
		Endpoint:    cfg.Tracker.Endpoint,
		APIKey:      linearAPIKey,
		ProjectSlug: cfg.Tracker.ProjectSlug,
	})
	if err != nil {
		fatal(logger, "linear client init failed", "err", err)
	}
	wsManager := tart.New(cfg.Workspace.BaseImage, "admin", sshKey)
	agentRunner := &claudecode.Runner{
		Workspace:    wsManager,
		Command:      cfg.ClaudeCode.Command,
		TurnTimeout:  time.Duration(cfg.ClaudeCode.TurnTimeoutMs) * time.Millisecond,
		StallTimeout: time.Duration(cfg.ClaudeCode.StallTimeoutMs) * time.Millisecond,
		WorkingDir:   "/Users/admin/events",
	}
	claudeHarnessDir := os.Getenv("SPUR_HARNESS_CLAUDE_DIR")
	if claudeHarnessDir == "" {
		home, _ := os.UserHomeDir()
		claudeHarnessDir = filepath.Join(home, ".spur", "claude-harness")
	}
	worker := &orchestrator.SpurWorker{
		Workflow:     def,
		Config:       cfg,
		WorkspaceMgr: wsManager,
		AgentRunner:  agentRunner,
		HarnessCreds: orchestrator.Credentials{
			GitHubToken:      githubToken,
			LinearToken:      linearBotToken,
			ClaudeHarnessDir: claudeHarnessDir,
		},
		Logger: logger,
	}
	orch := orchestrator.New(def, cfg, tracker, worker, logger)

	// 4. Run.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *once {
		if err := orch.RunOnce(ctx, *issue); err != nil {
			logger.Error("once mode failed", "err", err)
			os.Exit(2)
		}
		return
	}
	if err := orch.RunDaemon(ctx); err != nil {
		logger.Error("daemon failed", "err", err)
		os.Exit(2)
	}
}

func requireEnv(preferredVar, fallbackVar string) (string, error) {
	if preferredVar != "" {
		if v := os.Getenv(preferredVar); v != "" {
			return v, nil
		}
	}
	if v := os.Getenv(fallbackVar); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("env var %s (or %s) is not set", preferredVar, fallbackVar)
}

func fatal(logger *slog.Logger, msg string, kv ...any) {
	logger.Error(msg, kv...)
	os.Exit(1)
}
