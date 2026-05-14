package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
)

type SmokeResult struct {
	UserAgent string
	CodexHome string
	Platform  string
	ThreadID  string
}

func SmokeCheck(ctx context.Context, command string, tools []agent.DynamicTool) (SmokeResult, error) {
	if command == "" {
		command = "codex app-server"
	}
	smokeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(smokeCtx, "sh", "-lc", command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return SmokeResult{}, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return SmokeResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return SmokeResult{}, fmt.Errorf("stderr pipe: %w", err)
	}
	var stderrBuf strings.Builder
	go func() { _, _ = io.Copy(&stderrBuf, stderr) }()

	if err := cmd.Start(); err != nil {
		return SmokeResult{}, fmt.Errorf("start codex app-server: %w", err)
	}
	protocol := newProtocolClient(stdin, stdout, nil).withDynamicTools(tools)
	defer protocol.close()

	result, err := protocol.requestRaw(smokeCtx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "spur-orchestrator-smoke",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	})
	if err != nil {
		if stderrBuf.Len() > 0 {
			return SmokeResult{}, fmt.Errorf("%w (stderr: %s)", err, truncate(stderrBuf.String(), 200))
		}
		return SmokeResult{}, err
	}
	var decoded struct {
		UserAgent      string `json:"userAgent"`
		CodexHome      string `json:"codexHome"`
		PlatformFamily string `json:"platformFamily"`
		PlatformOS     string `json:"platformOs"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return SmokeResult{}, fmt.Errorf("decode initialize result: %w", err)
	}
	threadID, err := protocol.smokeThreadStart(smokeCtx)
	if err != nil {
		if stderrBuf.Len() > 0 {
			return SmokeResult{}, fmt.Errorf("%w (stderr: %s)", err, truncate(stderrBuf.String(), 200))
		}
		return SmokeResult{}, err
	}
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if waitErr != nil && smokeCtx.Err() != nil {
		return SmokeResult{}, smokeCtx.Err()
	}
	platform := decoded.PlatformOS
	if platform == "" {
		platform = decoded.PlatformFamily
	}
	return SmokeResult{
		UserAgent: decoded.UserAgent,
		CodexHome: decoded.CodexHome,
		Platform:  platform,
		ThreadID:  threadID,
	}, nil
}

func SmokeLinearGraphQLTool() agent.DynamicTool {
	return agent.DynamicTool{
		Name:        "linear_graphql",
		Description: "Smoke-test placeholder for the host-side Linear GraphQL dynamic tool.",
		InputSchema: json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"variables":{"type":"object"}},"additionalProperties":false}`),
		Handle: func(context.Context, agent.DynamicToolCall) (agent.DynamicToolResult, error) {
			return agent.DynamicToolResult{Success: false, Text: "smoke check placeholder tool"}, nil
		},
	}
}

func (c *protocolClient) smokeThreadStart(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	params := map[string]any{
		"approvalPolicy": "never",
		"cwd":            cwd,
		"ephemeral":      true,
		"sandbox":        "danger-full-access",
	}
	if len(c.dynamicTools) > 0 {
		params["dynamicTools"] = dynamicToolSpecs(c.dynamicTools)
	}
	result, err := c.requestRaw(ctx, "thread/start", params)
	if err != nil {
		return "", err
	}
	return extractThreadID(result)
}
