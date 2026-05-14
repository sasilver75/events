package tart

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestVMNameFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		identifier, want string
	}{
		{"SAM-12", "spur-ticket-SAM-12"},
		{"SAM-12.3", "spur-ticket-SAM-12.3"},
		{"a/b 12", "spur-ticket-a_b_12"},
	}
	for _, tc := range cases {
		if got := VMNameFor(tc.identifier); got != tc.want {
			t.Errorf("VMNameFor(%q) = %q, want %q", tc.identifier, got, tc.want)
		}
	}
}

func TestNew_Defaults(t *testing.T) {
	t.Parallel()
	m := New("spur-base", "", "/path/to/key")
	if m.SSHUser != "admin" {
		t.Errorf("default SSHUser = %q, want admin", m.SSHUser)
	}
	if m.BaseImage != "spur-base" {
		t.Errorf("BaseImage = %q", m.BaseImage)
	}
	if m.BootTimeout < time.Minute {
		t.Errorf("BootTimeout = %v, want >= 1m", m.BootTimeout)
	}
}

func TestHookEnv_Env(t *testing.T) {
	t.Parallel()
	env := HookEnv{
		VMName:           "spur-ticket-SAM-12",
		VMIP:             "192.168.64.5",
		IssueID:          "uuid-1",
		IssueIdentifier:  "SAM-12",
		IssueJSON:        `{"id":"uuid-1"}`,
		GitHubToken:      "ghp_xxx",
		LinearToken:      "lin_xxx",
		RunLogDir:        "/var/log/spur/SAM-12-1",
		HarnessClaudeDir: "/tmp/claude",
		HarnessCodexDir:  "/tmp/codex",
	}
	got := env.Env()
	wantKeys := []string{
		"SPUR_VM_NAME=spur-ticket-SAM-12",
		"SPUR_VM_IP=192.168.64.5",
		"SPUR_ISSUE_ID=uuid-1",
		"SPUR_ISSUE_IDENTIFIER=SAM-12",
		`SPUR_ISSUE_JSON={"id":"uuid-1"}`,
		"SPUR_GITHUB_TOKEN=ghp_xxx",
		"SPUR_LINEAR_TOKEN=lin_xxx",
		"SPUR_RUN_LOG_DIR=/var/log/spur/SAM-12-1",
		"SPUR_HARNESS_CLAUDE_DIR=/tmp/claude",
		"SPUR_HARNESS_CODEX_DIR=/tmp/codex",
	}
	for _, want := range wantKeys {
		found := false
		for _, e := range got {
			if e == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("env missing %q (got %v)", want, got)
		}
	}
}

// TestRunHook_EmptyScript verifies that an empty hook body is a no-op.
func TestRunHook_EmptyScript(t *testing.T) {
	t.Parallel()
	out, err := RunHook(context.Background(), "after_create", "", HookEnv{}, time.Second)
	if err != nil {
		t.Errorf("empty hook err = %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty hook out = %q", string(out))
	}
}

// TestRunHook_EnvVarVisible verifies that env vars are passed through.
func TestRunHook_EnvVarVisible(t *testing.T) {
	t.Parallel()
	out, err := RunHook(context.Background(),
		"test", `echo "name=$SPUR_VM_NAME id=$SPUR_ISSUE_ID"`,
		HookEnv{VMName: "spur-ticket-SAM-1", IssueID: "uuid-1"},
		3*time.Second)
	if err != nil {
		t.Fatalf("hook err = %v out=%s", err, out)
	}
	if !strings.Contains(string(out), "name=spur-ticket-SAM-1 id=uuid-1") {
		t.Errorf("hook output missing env vars: %s", out)
	}
}
