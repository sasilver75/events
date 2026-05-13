// Package tart implements the Workspace Manager from Symphony spec §9,
// adapted for Spur: each workspace is a Tart VM clone of spur-base rather
// than a filesystem directory.
//
// VM name convention: "spur-ticket-<WorkspaceKey>", where WorkspaceKey is
// derived from the issue identifier per spec §4.2 / §9.5 Invariant 3.
//
// Safety invariants from spec §9.5 (adapted):
//
//  1. The agent runs inside the per-issue VM, never on the host. Enforced
//     by the agent runner always invoking commands via SSH against the
//     VM IP, never executing on the host filesystem.
//
//  2. The VM name stays inside the "workspace root" (Tart's VM registry
//     at ~/.tart/vms/). Enforced by always going through `tart` for
//     VM lifecycle — we never touch ~/.tart/ directly.
//
//  3. The workspace key is sanitized. Enforced by domain.WorkspaceKey.
package tart

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/domain"
)

const (
	// VMNamePrefix is prepended to the sanitized workspace key.
	VMNamePrefix = "spur-ticket-"
)

// Manager owns the lifecycle of per-issue Tart VMs.
type Manager struct {
	// BaseImage is the Tart VM cloned per ticket (e.g. "spur-base").
	BaseImage string

	// SSHUser is the admin account inside cirruslabs images. Default: "admin".
	SSHUser string

	// SSHKey is the path to the harness private key (e.g. ~/.ssh/spur-agent-vm).
	SSHKey string

	// BootTimeout is how long we wait for `tart run` to produce an
	// SSH-reachable VM. Default: 6 minutes.
	BootTimeout time.Duration

	// runningPIDs maps VM name → *exec.Cmd of the `tart run` process so
	// the manager can graceful-stop on shutdown.
	runningPIDs map[string]*exec.Cmd
}

// New constructs a Manager with sensible defaults.
func New(baseImage, sshUser, sshKey string) *Manager {
	if sshUser == "" {
		sshUser = "admin"
	}
	return &Manager{
		BaseImage:   baseImage,
		SSHUser:     sshUser,
		SSHKey:      sshKey,
		BootTimeout: 6 * time.Minute,
		runningPIDs: map[string]*exec.Cmd{},
	}
}

// VMNameFor maps an issue identifier to its Tart VM name.
// Symphony spec §4.2 + spec §9.5 Invariant 3.
func VMNameFor(issueIdentifier string) string {
	return VMNamePrefix + domain.WorkspaceKey(issueIdentifier)
}

// EnsureWorkspace clones-from-base if needed, boots the VM, and waits for
// SSH to be reachable. Returns the populated Workspace.
//
// The CreatedNow flag tells the caller whether to fire the `after_create`
// hook (spec §5.3.4).
func (m *Manager) EnsureWorkspace(ctx context.Context, issueIdentifier string) (domain.Workspace, string, error) {
	if m.BaseImage == "" {
		return domain.Workspace{}, "", errors.New("tart: BaseImage not configured")
	}
	vmName := VMNameFor(issueIdentifier)

	exists, err := m.exists(ctx, vmName)
	if err != nil {
		return domain.Workspace{}, "", err
	}

	createdNow := false
	if !exists {
		if err := m.run(ctx, "tart", "clone", m.BaseImage, vmName); err != nil {
			return domain.Workspace{}, "", fmt.Errorf("tart clone %s %s: %w", m.BaseImage, vmName, err)
		}
		createdNow = true
	}

	// Boot if not already running.
	state, err := m.vmState(ctx, vmName)
	if err != nil {
		return domain.Workspace{}, "", err
	}
	if state != "running" {
		if err := m.startVM(ctx, vmName); err != nil {
			return domain.Workspace{}, "", err
		}
	}

	ip, err := m.waitForSSH(ctx, vmName)
	if err != nil {
		return domain.Workspace{}, "", fmt.Errorf("waitForSSH %s: %w", vmName, err)
	}

	return domain.Workspace{
		Path:         vmName,
		WorkspaceKey: domain.WorkspaceKey(issueIdentifier),
		CreatedNow:   createdNow,
	}, ip, nil
}

// RemoveWorkspace stops and deletes the VM. Symphony spec §9.4 (before_remove
// hook should fire before this; the orchestrator owns hook ordering).
func (m *Manager) RemoveWorkspace(ctx context.Context, vmName string) error {
	// Best-effort stop; ignore "not running" errors.
	_ = m.run(ctx, "tart", "stop", vmName)
	if cmd, ok := m.runningPIDs[vmName]; ok {
		_ = cmd.Wait()
		delete(m.runningPIDs, vmName)
	}
	if err := m.run(ctx, "tart", "delete", vmName); err != nil {
		return fmt.Errorf("tart delete %s: %w", vmName, err)
	}
	return nil
}

// Exists reports whether a VM by that name is registered with Tart.
func (m *Manager) Exists(ctx context.Context, vmName string) (bool, error) {
	return m.exists(ctx, vmName)
}

// Shutdown stops all VMs the manager booted in this process lifetime.
// Use from orchestrator graceful shutdown / signal handler.
func (m *Manager) Shutdown(ctx context.Context) {
	for name, cmd := range m.runningPIDs {
		_ = m.run(ctx, "tart", "stop", name)
		_ = cmd.Wait()
	}
	m.runningPIDs = map[string]*exec.Cmd{}
}

func (m *Manager) exists(ctx context.Context, vmName string) (bool, error) {
	out, err := m.output(ctx, "tart", "list", "--format", "json")
	if err != nil {
		// Fall back to text parsing if --format json isn't supported on
		// this tart version.
		text, terr := m.output(ctx, "tart", "list")
		if terr != nil {
			return false, fmt.Errorf("tart list: %w", err)
		}
		for _, line := range strings.Split(string(text), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == vmName {
				return true, nil
			}
		}
		return false, nil
	}
	// json variant: a list of {"Source","Name","Disk","Size","State"} rows.
	// We just look for the name substring — quick and avoids pulling in a
	// schema we don't otherwise need.
	return strings.Contains(string(out), `"Name":"`+vmName+`"`), nil
}

func (m *Manager) vmState(ctx context.Context, vmName string) (string, error) {
	out, err := m.output(ctx, "tart", "list")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == vmName {
			// Last field is the state column. Lines look like:
			//   local  spur-base  140  86  ...  stopped
			return fields[len(fields)-1], nil
		}
	}
	return "absent", nil
}

func (m *Manager) startVM(ctx context.Context, vmName string) error {
	// Bind the tart run process to the parent context. When the
	// orchestrator process exits (signal, --once mode completion, etc.)
	// the tart child stops cleanly, which gracefully halts the VM and
	// flushes its disk state — no orphan accumulation between runs.
	// VM persistence across runs is provided by Tart's stored image on
	// disk, not by the tart run process staying alive.
	cmd := exec.CommandContext(ctx, "tart", "run", vmName, "--no-graphics")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tart run %s: %w", vmName, err)
	}
	m.runningPIDs[vmName] = cmd
	return nil
}

func (m *Manager) waitForSSH(ctx context.Context, vmName string) (string, error) {
	deadline := time.Now().Add(m.BootTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		ip, err := m.output(ctx, "tart", "ip", vmName)
		if err == nil && len(ip) > 0 {
			ipStr := strings.TrimSpace(string(ip))
			if isSSHOpen(ipStr) {
				return ipStr, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("VM %s never opened SSH within %s", vmName, m.BootTimeout)
}

func isSSHOpen(ip string) bool {
	if ip == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, "22"), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// run executes a command and discards stdout/stderr. Used for fire-and-check
// invocations like `tart clone`.
func (m *Manager) run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// output runs a command and returns its stdout.
func (m *Manager) output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}
