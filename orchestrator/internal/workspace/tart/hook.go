package tart

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// HookEnv carries the variables WORKFLOW.md hooks reference. The Hook
// scripts run on the host (not inside the VM), with these env vars set
// so they can SSH into the VM, scp files, etc.
//
// See WORKFLOW.md `hooks.*` for how these are consumed.
type HookEnv struct {
	VMName           string
	VMIP             string
	IssueID          string
	IssueIdentifier  string
	IssueJSON        string // serialized current issue snapshot
	GitHubToken      string
	LinearToken      string
	RunLogDir        string
	SSHKey           string // host path to harness private SSH key
	HarnessClaudeDir string // host path to ~/.spur/claude-harness/ snapshot
	HarnessCodexDir  string // optional host path to ~/.spur/codex-harness/ snapshot
}

// Env converts HookEnv to a slice of "KEY=value" env entries for exec.Cmd.
func (h HookEnv) Env() []string {
	return []string{
		"SPUR_VM_NAME=" + h.VMName,
		"SPUR_VM_IP=" + h.VMIP,
		"SPUR_ISSUE_ID=" + h.IssueID,
		"SPUR_ISSUE_IDENTIFIER=" + h.IssueIdentifier,
		"SPUR_ISSUE_JSON=" + h.IssueJSON,
		"SPUR_GITHUB_TOKEN=" + h.GitHubToken,
		"SPUR_LINEAR_TOKEN=" + h.LinearToken,
		"SPUR_RUN_LOG_DIR=" + h.RunLogDir,
		"SPUR_SSH_KEY=" + h.SSHKey,
		"SPUR_HARNESS_CLAUDE_DIR=" + h.HarnessClaudeDir,
		"SPUR_HARNESS_CODEX_DIR=" + h.HarnessCodexDir,
	}
}

// RunHook executes a hook script body with the given env, bounded by
// timeout. Symphony spec §9.4 — hook execution contract.
//
// `script` is the raw shell body. We invoke `bash -lc "$script"` so the
// host's PATH and tools (tart, ssh, scp) are available.
//
// Returns the captured stdout+stderr alongside any error so the caller
// (orchestrator) can log it. Empty script body is a no-op (returns nil).
func RunHook(ctx context.Context, hookName, script string, env HookEnv, timeout time.Duration) ([]byte, error) {
	if script == "" {
		return nil, nil
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "bash", "-lc", script)
	cmd.Env = append(currentEnv(), env.Env()...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return buf.Bytes(), fmt.Errorf("hook %s: timeout after %s", hookName, timeout)
	}
	if err != nil {
		return buf.Bytes(), fmt.Errorf("hook %s: %w", hookName, err)
	}
	return buf.Bytes(), nil
}

func currentEnv() []string {
	// We pass through the host environment so the hook scripts have access
	// to PATH, HOME, etc. The HookEnv fields override anything with the
	// same name because they're appended last.
	return append([]string{}, processEnviron()...)
}
