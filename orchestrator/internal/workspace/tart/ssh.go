package tart

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// SSHCmd returns an *exec.Cmd that runs `command` inside the VM at `ip`
// over key-based SSH. Callers attach stdin/stdout/stderr as needed.
//
// The flags suppress known_hosts churn (per-issue clones all start with
// the same host key) and silence routine warnings. This matches the
// shell-script bootstrap's SSH_OPTS.
func (m *Manager) SSHCmd(ctx context.Context, ip, command string) *exec.Cmd {
	args := []string{
		"-i", m.SSHKey,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=30",
		fmt.Sprintf("%s@%s", m.SSHUser, ip),
		command,
	}
	return exec.CommandContext(ctx, "ssh", args...)
}

// SSHRun executes `command` inside the VM and returns combined output.
// Useful for one-shot probes ("is the repo cloned yet?", "what's the
// branch?"). For long-running commands (the agent runner) use SSHCmd
// directly and stream.
func (m *Manager) SSHRun(ctx context.Context, ip, command string) ([]byte, error) {
	cmd := m.SSHCmd(ctx, ip, command)
	return cmd.CombinedOutput()
}

// SSHScript pipes a multi-line bash script into `bash -s` running inside
// the VM. Returns combined output. This is the safest way to ship a
// non-trivial script over SSH — no shell-quoting hazards, heredocs work
// (as long as they don't conflict with each other within the script body).
func (m *Manager) SSHScript(ctx context.Context, ip, script string) ([]byte, error) {
	cmd := m.SSHCmd(ctx, ip, "bash -s")
	cmd.Stdin = stringReader(script)
	return cmd.CombinedOutput()
}

func stringReader(s string) io.Reader {
	return &readerFromString{s: s}
}

type readerFromString struct {
	s   string
	pos int
}

func (r *readerFromString) Read(p []byte) (int, error) {
	if r.pos >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}
