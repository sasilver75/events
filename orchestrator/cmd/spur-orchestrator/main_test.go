package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sasilver75/events/orchestrator/internal/domain"
	"github.com/sasilver75/events/orchestrator/internal/tracker/linear"
	"github.com/sasilver75/events/orchestrator/internal/workflow"
)

func TestApplyCLIConfigOverridesCodexCanary(t *testing.T) {
	cfg := workflow.ServiceConfig{
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}

	if err := applyCLIConfigOverrides(&cfg, true, true, false, "SAM-12"); err != nil {
		t.Fatalf("applyCLIConfigOverrides returned error: %v", err)
	}
	if cfg.Agent.Runner != "codex" {
		t.Fatalf("runner = %q, want codex", cfg.Agent.Runner)
	}
	if cfg.Credentials.LinearAccess != "host_proxy" {
		t.Fatalf("linear access = %q, want host_proxy", cfg.Credentials.LinearAccess)
	}
}

func TestRequireEnvMessageOmitsDuplicateFallback(t *testing.T) {
	t.Setenv("SPUR_TEST_REQUIRED_ENV", "")

	_, err := requireEnv("SPUR_TEST_REQUIRED_ENV", "SPUR_TEST_REQUIRED_ENV")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "env var SPUR_TEST_REQUIRED_ENV is not set" {
		t.Fatalf("error = %q", got)
	}
}

func TestRequireEnvMessageIncludesFallbackWhenDifferent(t *testing.T) {
	t.Setenv("SPUR_TEST_PRIMARY_ENV", "")
	t.Setenv("SPUR_TEST_FALLBACK_ENV", "")

	_, err := requireEnv("SPUR_TEST_PRIMARY_ENV", "SPUR_TEST_FALLBACK_ENV")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "env var SPUR_TEST_PRIMARY_ENV (or SPUR_TEST_FALLBACK_ENV) is not set" {
		t.Fatalf("error = %q", got)
	}
}

func TestApplyCLIConfigOverridesCodexCanaryRequiresOneIssue(t *testing.T) {
	tests := []struct {
		name      string
		once      bool
		preflight bool
		issue     string
	}{
		{name: "daemon", once: false, preflight: false, issue: "SAM-12"},
		{name: "missing issue outside preflight", once: true, preflight: false, issue: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := workflow.ServiceConfig{
				Agent:       workflow.AgentConfig{Runner: "codex"},
				Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
			}
			if err := applyCLIConfigOverrides(&cfg, true, tt.once, tt.preflight, tt.issue); err == nil {
				t.Fatal("expected error")
			}
			if cfg.Agent.Runner != "codex" {
				t.Fatalf("runner changed to %q", cfg.Agent.Runner)
			}
			if cfg.Credentials.LinearAccess != "host_proxy" {
				t.Fatalf("linear access changed to %q", cfg.Credentials.LinearAccess)
			}
		})
	}
}

func TestApplyCLIConfigOverridesCodexCanaryAllowsPreflightWithoutIssue(t *testing.T) {
	cfg := workflow.ServiceConfig{
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}

	if err := applyCLIConfigOverrides(&cfg, true, true, true, ""); err != nil {
		t.Fatalf("applyCLIConfigOverrides returned error: %v", err)
	}
	if cfg.Agent.Runner != "codex" {
		t.Fatalf("runner = %q, want codex", cfg.Agent.Runner)
	}
	if cfg.Credentials.LinearAccess != "host_proxy" {
		t.Fatalf("linear access = %q, want host_proxy", cfg.Credentials.LinearAccess)
	}
}

func TestApplyCLIConfigOverridesNoop(t *testing.T) {
	cfg := workflow.ServiceConfig{
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}

	if err := applyCLIConfigOverrides(&cfg, false, false, false, ""); err != nil {
		t.Fatalf("applyCLIConfigOverrides returned error: %v", err)
	}
	if cfg.Agent.Runner != "codex" {
		t.Fatalf("runner = %q, want codex", cfg.Agent.Runner)
	}
	if cfg.Credentials.LinearAccess != "host_proxy" {
		t.Fatalf("linear access = %q, want host_proxy", cfg.Credentials.LinearAccess)
	}
}

func TestValidatePreflightCredentialsRequiresGitHubToken(t *testing.T) {
	if err := validatePreflightCredentials("gh_test"); err != nil {
		t.Fatalf("validatePreflightCredentials returned error: %v", err)
	}
	err := validatePreflightCredentials("")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "SPUR_HARNESS_GITHUB_TOKEN") {
		t.Fatalf("error = %q, want GitHub token", err)
	}
}

func TestValidateWorkflowConfigForModeAllowsOfflineStatusVerificationWithoutLinearKey(t *testing.T) {
	t.Setenv("SPUR_TEST_LINEAR_KEY", "")
	t.Setenv("LINEAR_API_KEY", "")
	cfg := workflow.ServiceConfig{
		Tracker: workflow.TrackerConfig{
			Kind:        "linear",
			ProjectSlug: "spur",
			APIKeyEnv:   "SPUR_TEST_LINEAR_KEY",
		},
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Codex:       workflow.CodexConfig{Command: "codex app-server"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}

	if err := validateWorkflowConfigForMode(cfg, true); err != nil {
		t.Fatalf("offline verify validation returned error: %v", err)
	}
	err := validateWorkflowConfigForMode(cfg, false)
	if err == nil || !strings.Contains(err.Error(), "missing_tracker_api_key") {
		t.Fatalf("normal validation error = %v, want missing tracker API key", err)
	}
}

func TestValidatePreflightLocalReadinessForIssueReportsRunInputs(t *testing.T) {
	t.Setenv("SPUR_TEST_LINEAR_KEY", "")
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("SPUR_HARNESS_GITHUB_TOKEN", "")
	t.Setenv("SPUR_HARNESS_SSH_KEY", filepath.Join(t.TempDir(), "missing-key"))
	t.Setenv("SPUR_HARNESS_CODEX_DIR", filepath.Join(t.TempDir(), "missing-codex"))
	cfg := workflow.ServiceConfig{Tracker: workflow.TrackerConfig{APIKeyEnv: "SPUR_TEST_LINEAR_KEY"}}

	err := validatePreflightLocalReadiness(cfg, true)
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	for _, want := range []string{
		"tracker.api_key references env var SPUR_TEST_LINEAR_KEY, but it is not set",
		"SPUR_HARNESS_GITHUB_TOKEN is not set",
		"SPUR_HARNESS_CODEX_DIR not found",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("readiness error missing %q:\n%s", want, got)
		}
	}
}

func TestValidatePreflightLocalReadinessForDiscoveryOnlyRequiresLinear(t *testing.T) {
	t.Setenv("SPUR_TEST_LINEAR_KEY", "")
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("SPUR_HARNESS_GITHUB_TOKEN", "")
	t.Setenv("SPUR_HARNESS_SSH_KEY", filepath.Join(t.TempDir(), "missing-key"))
	cfg := workflow.ServiceConfig{Tracker: workflow.TrackerConfig{APIKeyEnv: "SPUR_TEST_LINEAR_KEY"}}

	err := validatePreflightLocalReadiness(cfg, false)
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if !strings.Contains(got, "tracker.api_key references env var SPUR_TEST_LINEAR_KEY, but it is not set") {
		t.Fatalf("readiness error missing Linear key:\n%s", got)
	}
	for _, notWant := range []string{"SPUR_HARNESS_GITHUB_TOKEN", "harness SSH key"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("discovery readiness should not require %q:\n%s", notWant, got)
		}
	}
}

func TestValidatePreflightLocalReadinessPasses(t *testing.T) {
	sshKey := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(sshKey, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("write ssh key: %v", err)
	}
	t.Setenv("SPUR_TEST_LINEAR_KEY", "lin_test")
	t.Setenv("SPUR_HARNESS_GITHUB_TOKEN", "gh_test")
	t.Setenv("SPUR_HARNESS_SSH_KEY", sshKey)
	t.Setenv("SPUR_HARNESS_CODEX_DIR", t.TempDir())
	cfg := workflow.ServiceConfig{Tracker: workflow.TrackerConfig{APIKeyEnv: "SPUR_TEST_LINEAR_KEY"}}

	if err := validatePreflightLocalReadiness(cfg, true); err != nil {
		t.Fatalf("validatePreflightLocalReadiness returned error: %v", err)
	}
}

func TestRunPreflightFindsEligibleIssue(t *testing.T) {
	tracker := fakePreflightTracker{
		issues: []domain.Issue{
			{ID: "uuid-1", Identifier: "SAM-12", State: "Ready", Labels: []string{"afk"}},
		},
	}
	cfg := workflow.ServiceConfig{
		Tracker:     workflow.TrackerConfig{ActiveStates: []string{"Ready"}},
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}

	if err := runPreflight(context.Background(), cfg, tracker, linear.SpurDefault, "SAM-12"); err != nil {
		t.Fatalf("runPreflight returned error: %v", err)
	}
}

func TestRunPreflightListsCandidateIssues(t *testing.T) {
	tracker := fakePreflightTracker{
		issues: []domain.Issue{
			{ID: "uuid-2", Identifier: "SAM-2", Title: "Needs AFK", State: "Ready"},
			{ID: "uuid-12", Identifier: "SAM-12", Title: "Codex run", State: "Ready", Labels: []string{"afk"}},
		},
	}
	cfg := workflow.ServiceConfig{
		Tracker:     workflow.TrackerConfig{ActiveStates: []string{"Ready"}},
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}
	var out bytes.Buffer

	if err := runPreflightWithOutput(context.Background(), cfg, tracker, linear.SpurDefault, "", &out); err != nil {
		t.Fatalf("runPreflight returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"candidates=2 eligible=1 rejected=1",
		"runner=codex linear_access=host_proxy",
		"Eligible issues:",
		"- SAM-12 [Ready] Codex run",
		"Rejected active candidates:",
		"- SAM-2: missing_required_label:afk",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRunPreflightRejectsIneligibleIssue(t *testing.T) {
	tracker := fakePreflightTracker{
		issues: []domain.Issue{
			{ID: "uuid-1", Identifier: "SAM-12", State: "Ready"},
		},
	}
	cfg := workflow.ServiceConfig{
		Tracker:     workflow.TrackerConfig{ActiveStates: []string{"Ready"}},
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}

	err := runPreflight(context.Background(), cfg, tracker, linear.SpurDefault, "SAM-12")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing_required_label:afk") {
		t.Fatalf("error = %q, want missing afk label", err)
	}
}

func TestRunPreflightRejectsMissingIssue(t *testing.T) {
	tracker := fakePreflightTracker{
		issues: []domain.Issue{
			{ID: "uuid-1", Identifier: "SAM-12", State: "Ready", Labels: []string{"afk"}},
		},
	}
	cfg := workflow.ServiceConfig{
		Tracker:     workflow.TrackerConfig{ActiveStates: []string{"Ready"}},
		Agent:       workflow.AgentConfig{Runner: "codex"},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
	}

	err := runPreflight(context.Background(), cfg, tracker, linear.SpurDefault, "SAM-99")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "issue not found") {
		t.Fatalf("error = %q, want issue not found", err)
	}
}

func TestWriteCodexCanaryChecklistUsesIssueIdentifier(t *testing.T) {
	var out bytes.Buffer

	writeCodexCanaryChecklist(&out, "SAM-12")

	got := out.String()
	for _, want := range []string{
		"Codex run evidence checklist for SAM-12",
		"Discovery preflight listed SAM-12 as eligible.",
		"`spur-orchestrator --once --issue SAM-12 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-12-codex.json` exited 0.",
		"`spur-orchestrator --codex-canary-verify-status --issue SAM-12 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-12-codex.json` exited 0.",
		"Required status file `/tmp/spur-orchestrator/SAM-12-codex.json` exists and contains `agent_runner=codex`, `linear_access=host_proxy`, `recent_runs[0].identifier=SAM-12`, `recent_runs[0].status`, `recent_runs[0].session_id`, `recent_runs[0].thread_id`, `recent_runs[0].turn_id`, `recent_runs[0].token_info`, `recent_runs[0].rate_limits`",
		"Issue state is `In Review`",
		"`scripts/spur-publish-preflight SAM-12` passed before commit/push/PR creation.",
		"PR is ready for review, not draft",
		"No host-held Linear credential leaked",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("checklist missing %q:\n%s", want, got)
		}
	}
}

func TestWriteCodexCanaryChecklistUsesPlaceholder(t *testing.T) {
	var out bytes.Buffer

	writeCodexCanaryChecklist(&out, "")

	if got := out.String(); !strings.Contains(got, "Codex run evidence checklist for <SAM-N>") {
		t.Fatalf("checklist missing placeholder:\n%s", got)
	}
}

func TestPublishPreflightScriptAllowsMatchingIssueBranch(t *testing.T) {
	repo := newPublishPreflightRepo(t)
	git(t, repo, "switch", "-c", "sam-64-publish-preflight")

	got, err := runPublishPreflightScript(repo, "SAM-64")
	if err != nil {
		t.Fatalf("publish preflight failed: %v\n%s", err, got)
	}
	if !strings.Contains(got, "publish preflight passed") {
		t.Fatalf("preflight output missing success:\n%s", got)
	}
}

func TestPublishPreflightScriptRejectsDifferentIssueBranch(t *testing.T) {
	repo := newPublishPreflightRepo(t)
	git(t, repo, "switch", "-c", "sam-60-fleet-dashboard")

	got, err := runPublishPreflightScript(repo, "SAM-64")
	if err == nil {
		t.Fatalf("publish preflight passed unexpectedly:\n%s", got)
	}
	for _, want := range []string{
		"branch/issue mismatch",
		"appears to belong to SAM-60",
		"active Linear issue is SAM-64",
		"Risk: accidentally stacking one Linear issue on another issue's branch.",
		"Create a clean branch from origin/main",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preflight output missing %q:\n%s", want, got)
		}
	}
}

func TestPublishPreflightScriptRejectsOtherIssueCommitRefs(t *testing.T) {
	repo := newPublishPreflightRepo(t)
	git(t, repo, "switch", "-c", "sam-64-publish-preflight")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("stacked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "work.txt")
	git(t, repo, "commit", "-m", "feat: previous ticket work (SAM-60)")

	got, err := runPublishPreflightScript(repo, "SAM-64")
	if err == nil {
		t.Fatalf("publish preflight passed unexpectedly:\n%s", got)
	}
	for _, want := range []string{
		"branch contains commits for another Linear issue",
		"SAM-60",
		"Risk: accidentally stacking one Linear issue on another issue's branch.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preflight output missing %q:\n%s", want, got)
		}
	}
}

func newPublishPreflightRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "initial commit")
	git(t, repo, "branch", "-M", "main")
	git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	return repo
}

func runPublishPreflightScript(repo, issue string) (string, error) {
	script := filepath.Clean(filepath.Join("..", "..", "..", "scripts", "spur-publish-preflight"))
	cmd := exec.Command("bash", script, issue, "--repo", repo)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestVerifyCodexCanaryStatusArgs(t *testing.T) {
	if err := verifyCodexCanaryStatusArgs("SAM-12", "/tmp/status.json"); err != nil {
		t.Fatalf("verifyCodexCanaryStatusArgs returned error: %v", err)
	}
	if err := verifyCodexCanaryStatusArgs("", "/tmp/status.json"); err == nil || !strings.Contains(err.Error(), "--issue") {
		t.Fatalf("missing issue error = %v, want --issue", err)
	}
	if err := verifyCodexCanaryStatusArgs("SAM-12", ""); err == nil || !strings.Contains(err.Error(), "--status-file") {
		t.Fatalf("missing status file error = %v, want --status-file", err)
	}
}

func TestVerifyCodexCanaryStatusFilePasses(t *testing.T) {
	path := writeCanaryStatusFile(t, validCanaryStatus())
	var out bytes.Buffer

	if err := verifyCodexCanaryStatusFile(path, "SAM-12", &out); err != nil {
		t.Fatalf("verifyCodexCanaryStatusFile returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Codex status verified",
		"issue=SAM-12",
		"session_id=thread-123",
		"thread_id=thread-123",
		"turn_id=turn-456",
		"tokens=18",
		"runner=codex",
		"linear_access=host_proxy",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestVerifyCodexCanaryStatusFileRejectsWrongRunnerAndAccess(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name: "wrong runner",
			mutate: func(status map[string]any) {
				status["agent_runner"] = "other"
			},
			wantErr: "agent_runner",
		},
		{
			name: "wrong linear access",
			mutate: func(status map[string]any) {
				status["linear_access"] = "vm_env"
			},
			wantErr: "linear_access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := validCanaryStatus()
			tt.mutate(status)
			err := verifyCodexCanaryStatusFile(writeCanaryStatusFile(t, status), "SAM-12", io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyCodexCanaryStatusFileRejectsBadRecentRun(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name: "missing issue",
			mutate: func(status map[string]any) {
				status["recent_runs"] = []map[string]any{}
			},
			wantErr: "no recent run",
		},
		{
			name: "failed status",
			mutate: func(status map[string]any) {
				firstCanaryRun(status)["status"] = "failed"
			},
			wantErr: "want succeeded",
		},
		{
			name: "missing session",
			mutate: func(status map[string]any) {
				firstCanaryRun(status)["session_id"] = ""
			},
			wantErr: "missing session_id",
		},
		{
			name: "missing thread",
			mutate: func(status map[string]any) {
				firstCanaryRun(status)["thread_id"] = ""
			},
			wantErr: "missing thread_id",
		},
		{
			name: "missing turn",
			mutate: func(status map[string]any) {
				firstCanaryRun(status)["turn_id"] = ""
			},
			wantErr: "missing turn_id",
		},
		{
			name: "missing token info",
			mutate: func(status map[string]any) {
				delete(firstCanaryRun(status), "token_info")
			},
			wantErr: "missing token_info",
		},
		{
			name: "zero token total",
			mutate: func(status map[string]any) {
				firstCanaryRun(status)["token_info"] = map[string]any{"total_tokens": 0}
			},
			wantErr: "want > 0",
		},
		{
			name: "codex totals lower than run",
			mutate: func(status map[string]any) {
				status["codex_totals"] = map[string]any{"total_tokens": 1}
			},
			wantErr: "less than run total_tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := validCanaryStatus()
			tt.mutate(status)
			err := verifyCodexCanaryStatusFile(writeCanaryStatusFile(t, status), "SAM-12", io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func validCanaryStatus() map[string]any {
	return map[string]any{
		"agent_runner":  "codex",
		"linear_access": "host_proxy",
		"recent_runs": []map[string]any{
			{
				"issue_id":    "uuid-12",
				"identifier":  "SAM-12",
				"session_id":  "thread-123",
				"thread_id":   "thread-123",
				"turn_id":     "turn-456",
				"status":      "succeeded",
				"token_info":  map[string]any{"input_tokens": 12, "output_tokens": 6, "total_tokens": 18},
				"rate_limits": map[string]any{"primary": map[string]any{"remaining": 42}},
			},
		},
		"codex_totals":      map[string]any{"input_tokens": 12, "output_tokens": 6, "total_tokens": 18},
		"codex_rate_limits": map[string]any{"primary": map[string]any{"remaining": 42}},
	}
}

func firstCanaryRun(status map[string]any) map[string]any {
	return status["recent_runs"].([]map[string]any)[0]
}

func writeCanaryStatusFile(t *testing.T, status map[string]any) string {
	t.Helper()
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	path := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}
	return path
}

type fakePreflightTracker struct {
	issues []domain.Issue
	err    error
}

func (f fakePreflightTracker) FetchCandidateIssues(context.Context, []string) ([]domain.Issue, error) {
	return f.issues, f.err
}
