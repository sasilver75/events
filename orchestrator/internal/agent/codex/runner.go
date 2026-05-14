// Package codex launches Symphony's reference Codex app-server runner.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/workspace/tart"
)

var (
	ErrProtocolClosed      = errors.New("codex_app_server_protocol_closed")
	ErrProtocolUnsupported = errors.New("codex_app_server_unsupported_request")
)

type Runner struct {
	Workspace *tart.Manager

	Command      string
	TurnTimeout  time.Duration
	StallTimeout time.Duration
	WorkingDir   string
	Env          map[string]string
	DynamicTools []agent.DynamicTool
}

func (r *Runner) WithTurnConfig(cfg agent.TurnConfig) agent.Runner {
	copy := *r
	if cfg.Command != "" {
		copy.Command = cfg.Command
	}
	copy.TurnTimeout = cfg.TurnTimeout
	copy.StallTimeout = cfg.StallTimeout
	copy.Env = map[string]string{}
	for k, v := range cfg.Env {
		copy.Env[k] = v
	}
	copy.DynamicTools = append([]agent.DynamicTool(nil), cfg.DynamicTools...)
	return &copy
}

func (r *Runner) Run(ctx context.Context, vmIP, prompt, resume string, onEvent func(agent.Event)) (agent.RunResult, error) {
	if r.TurnTimeout <= 0 {
		r.TurnTimeout = 60 * time.Minute
	}
	turnCtx, cancel := context.WithTimeout(ctx, r.TurnTimeout)
	defer cancel()

	cmd := r.Workspace.SSHCmd(turnCtx, vmIP, r.buildCommand())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agent.RunResult{Type: agent.EventStartupFailed}, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.RunResult{Type: agent.EventStartupFailed}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.RunResult{Type: agent.EventStartupFailed}, fmt.Errorf("stderr pipe: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return agent.RunResult{Type: agent.EventStartupFailed, Duration: time.Since(start)}, fmt.Errorf("start: %w", err)
	}

	var stderrBuf strings.Builder
	go func() { _, _ = io.Copy(&stderrBuf, stderr) }()

	protocol := newProtocolClient(stdin, stdout, onEvent).withDynamicTools(r.DynamicTools)
	defer protocol.close()

	threadID, result, err := r.runProtocol(turnCtx, protocol, prompt, resume, start, onEvent)
	_ = stdin.Close()
	if err != nil {
		_ = cmd.Wait()
		result.Duration = time.Since(start)
		if result.Type == "" {
			result.Type = agent.EventStartupFailed
		}
		if result.Error == "" {
			result.Error = err.Error()
		}
		if threadID != "" {
			result.SessionID = threadID
			result.ThreadID = threadID
		}
		if stderrBuf.Len() > 0 {
			return result, fmt.Errorf("%w (stderr: %s)", err, truncate(stderrBuf.String(), 200))
		}
		return result, err
	}

	waitErr := cmd.Wait()
	result.Duration = time.Since(start)
	if result.SessionID == "" {
		result.SessionID = threadID
	}
	if result.ThreadID == "" {
		result.ThreadID = threadID
	}
	if waitErr != nil {
		if stderrBuf.Len() > 0 {
			return result, fmt.Errorf("codex app-server wait: %w (stderr: %s)", waitErr, truncate(stderrBuf.String(), 200))
		}
		return result, fmt.Errorf("codex app-server wait: %w", waitErr)
	}
	return result, nil
}

func (r *Runner) runProtocol(ctx context.Context, protocol *protocolClient, prompt, resume string, start time.Time, onEvent func(agent.Event)) (string, agent.RunResult, error) {
	if err := protocol.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "spur-orchestrator",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		return "", agent.RunResult{Type: agent.EventStartupFailed}, err
	}

	var threadID string
	var err error
	if resume != "" {
		threadID, err = protocol.threadResume(ctx, resume, r.WorkingDir)
	} else {
		threadID, err = protocol.threadStart(ctx, r.WorkingDir)
	}
	if err != nil {
		return threadID, agent.RunResult{Type: agent.EventStartupFailed, SessionID: threadID}, err
	}
	if onEvent != nil {
		onEvent(agent.Event{
			Type:      agent.EventSessionStarted,
			Timestamp: time.Now().UTC(),
			SessionID: threadID,
			ThreadID:  threadID,
		})
	}

	turnID, err := protocol.turnStart(ctx, threadID, prompt, r.WorkingDir)
	if err != nil {
		return threadID, agent.RunResult{Type: agent.EventTurnFailed, SessionID: threadID}, err
	}

	threadID, result, err := protocol.waitForTurn(ctx, threadID, turnID, start)
	if result.RateLimits == nil {
		result.RateLimits = protocol.latestRateLimits
	}
	return threadID, result, err
}

func (r *Runner) buildCommand() string {
	envPrefix := ""
	for k, v := range r.Env {
		envPrefix += "export " + k + "=" + shellQuote(v) + " && "
	}
	return fmt.Sprintf(`eval "$(/opt/homebrew/bin/brew shellenv)" && %scd %s && %s`,
		envPrefix, shellQuote(r.WorkingDir), r.Command)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type protocolClient struct {
	enc          *json.Encoder
	stdin        io.Closer
	msgs         <-chan rpcRead
	onEvent      func(agent.Event)
	dynamicTools map[string]agent.DynamicTool

	mu               sync.Mutex
	nextID           int64
	latestRateLimits *agent.RateLimitSnapshot
}

type rpcRead struct {
	raw []byte
	msg rpcMessage
	err error
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func newProtocolClient(stdin io.WriteCloser, stdout io.Reader, onEvent func(agent.Event)) *protocolClient {
	msgs := make(chan rpcRead, 32)
	go func() {
		defer close(msgs)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			raw := append([]byte(nil), scanner.Bytes()...)
			var msg rpcMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				msgs <- rpcRead{raw: raw, err: err}
				continue
			}
			msgs <- rpcRead{raw: raw, msg: msg}
		}
		if err := scanner.Err(); err != nil {
			msgs <- rpcRead{err: err}
		}
	}()
	return &protocolClient{
		enc:     json.NewEncoder(stdin),
		stdin:   stdin,
		msgs:    msgs,
		onEvent: onEvent,
	}
}

func (c *protocolClient) withDynamicTools(tools []agent.DynamicTool) *protocolClient {
	if len(tools) == 0 {
		return c
	}
	c.dynamicTools = make(map[string]agent.DynamicTool, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || tool.Handle == nil {
			continue
		}
		c.dynamicTools[dynamicToolKey(tool.Namespace, tool.Name)] = tool
	}
	return c
}

func (c *protocolClient) close() {
	_ = c.stdin.Close()
}

func (c *protocolClient) request(ctx context.Context, method string, params any) error {
	_, err := c.requestRaw(ctx, method, params)
	return err
}

func (c *protocolClient) requestRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextRequestID()
	if err := c.write(map[string]any{
		"id":     json.RawMessage(id),
		"method": method,
		"params": params,
	}); err != nil {
		return nil, err
	}
	for {
		msg, err := c.nextMessage(ctx)
		if err != nil {
			return nil, err
		}
		if len(msg.ID) > 0 && bytes.Equal(msg.ID, id) {
			if msg.Error != nil {
				return nil, fmt.Errorf("codex rpc %s: %s", method, msg.Error.Message)
			}
			return msg.Result, nil
		}
		if err := c.handleAsync(ctx, msg); err != nil {
			return nil, err
		}
	}
}

func (c *protocolClient) threadStart(ctx context.Context, cwd string) (string, error) {
	params := map[string]any{
		"approvalPolicy": "never",
		"cwd":            cwd,
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

func (c *protocolClient) threadResume(ctx context.Context, threadID, cwd string) (string, error) {
	params := map[string]any{
		"approvalPolicy": "never",
		"cwd":            cwd,
		"sandbox":        "danger-full-access",
		"threadId":       threadID,
	}
	if len(c.dynamicTools) > 0 {
		params["dynamicTools"] = dynamicToolSpecs(c.dynamicTools)
	}
	result, err := c.requestRaw(ctx, "thread/resume", params)
	if err != nil {
		return "", err
	}
	if resumed, err := extractThreadID(result); err == nil && resumed != "" {
		return resumed, nil
	}
	return threadID, nil
}

func (c *protocolClient) turnStart(ctx context.Context, threadID, prompt, cwd string) (string, error) {
	result, err := c.requestRaw(ctx, "turn/start", map[string]any{
		"threadId":       threadID,
		"cwd":            cwd,
		"approvalPolicy": "never",
		"sandboxPolicy": map[string]any{
			"type": "dangerFullAccess",
		},
		"input": []map[string]string{
			{
				"type": "text",
				"text": prompt,
			},
		},
	})
	if err != nil {
		return "", err
	}
	return extractTurnID(result)
}

func (c *protocolClient) waitForTurn(ctx context.Context, threadID, turnID string, start time.Time) (string, agent.RunResult, error) {
	result := agent.RunResult{
		Type:      agent.EventTurnFailed,
		SessionID: threadID,
		ThreadID:  threadID,
		TurnID:    turnID,
		Duration:  time.Since(start),
	}
	for {
		msg, err := c.nextMessage(ctx)
		if err != nil {
			result.Error = err.Error()
			return threadID, result, err
		}
		event, completed, err := c.handleTurnMessage(ctx, msg, threadID, turnID, &result)
		if err != nil {
			result.Error = err.Error()
			return threadID, result, err
		}
		if c.onEvent != nil && event.Type != "" {
			c.onEvent(event)
		}
		if completed {
			result.Duration = time.Since(start)
			return threadID, result, nil
		}
	}
}

func (c *protocolClient) handleTurnMessage(ctx context.Context, msg rpcMessage, threadID, turnID string, result *agent.RunResult) (agent.Event, bool, error) {
	if len(msg.ID) > 0 && msg.Method != "" {
		return agent.Event{}, false, c.respondUnsupported(ctx, msg)
	}
	if msg.Method == "" {
		return agent.Event{}, false, nil
	}
	ev := agent.Event{
		Type:      agent.EventOtherMessage,
		Timestamp: time.Now().UTC(),
		SessionID: threadID,
		ThreadID:  threadID,
		TurnID:    turnID,
		Raw:       append(json.RawMessage(nil), msg.Params...),
	}
	switch msg.Method {
	case "turn/started":
		if gotThreadID := gjsonString(msg.Params, "threadId"); gotThreadID != "" {
			ev.ThreadID = gotThreadID
			ev.SessionID = gotThreadID
			result.SessionID = gotThreadID
			result.ThreadID = gotThreadID
		}
		if id := gjsonString(msg.Params, "turn.id"); id != "" {
			ev.TurnID = id
			result.TurnID = id
		}
		ev.Type = agent.EventOtherMessage
	case "thread/tokenUsage/updated":
		if gotThreadID := gjsonString(msg.Params, "threadId"); gotThreadID != "" {
			ev.ThreadID = gotThreadID
			ev.SessionID = gotThreadID
			result.SessionID = gotThreadID
			result.ThreadID = gotThreadID
		}
		if id := gjsonString(msg.Params, "turnId"); id != "" {
			ev.TurnID = id
			result.TurnID = id
		}
		usage := agent.Usage{
			InputTokens:  gjsonInt(msg.Params, "tokenUsage.total.inputTokens"),
			OutputTokens: gjsonInt(msg.Params, "tokenUsage.total.outputTokens"),
			TotalTokens:  gjsonInt(msg.Params, "tokenUsage.total.totalTokens"),
		}
		result.Usage = usage
		ev.Usage = usage
	case "account/rateLimits/updated":
		rateLimits := parseRateLimitSnapshot(msg.Params)
		c.latestRateLimits = rateLimits
		result.RateLimits = rateLimits
		ev.Type = agent.EventRateLimits
		ev.RateLimits = rateLimits
	case "turn/completed":
		if gotThreadID := gjsonString(msg.Params, "threadId"); gotThreadID != "" {
			ev.ThreadID = gotThreadID
			ev.SessionID = gotThreadID
			result.SessionID = gotThreadID
			result.ThreadID = gotThreadID
		}
		status := gjsonString(msg.Params, "turn.status")
		if id := gjsonString(msg.Params, "turn.id"); id != "" {
			ev.TurnID = id
			result.TurnID = id
		}
		result.Error = gjsonString(msg.Params, "turn.error.message")
		switch status {
		case "completed":
			result.Type = agent.EventTurnCompleted
			ev.Type = agent.EventTurnCompleted
		case "interrupted":
			result.Type = agent.EventTurnCancelled
			ev.Type = agent.EventTurnCancelled
		case "failed":
			result.Type = agent.EventTurnFailed
			ev.Type = agent.EventTurnFailed
			if result.Error == "" {
				result.Error = "codex turn failed"
			}
		default:
			result.Type = agent.EventTurnFailed
			ev.Type = agent.EventTurnFailed
			result.Error = "codex turn completed with unknown status: " + status
		}
		ev.Error = result.Error
		return ev, true, nil
	case "error":
		errMsg := gjsonString(msg.Params, "message")
		if errMsg == "" {
			errMsg = "codex app-server error"
		}
		result.Type = agent.EventTurnFailed
		result.Error = errMsg
		ev.Type = agent.EventTurnFailed
		ev.Error = errMsg
		return ev, true, nil
	}
	return ev, false, nil
}

func (c *protocolClient) handleAsync(ctx context.Context, msg rpcMessage) error {
	if len(msg.ID) > 0 && msg.Method != "" {
		return c.respondUnsupported(ctx, msg)
	}
	if c.onEvent == nil || msg.Method == "" {
		return nil
	}
	switch msg.Method {
	case "thread/started":
		threadID := gjsonString(msg.Params, "thread.id")
		c.onEvent(agent.Event{
			Type:      agent.EventSessionStarted,
			Timestamp: time.Now().UTC(),
			SessionID: threadID,
			ThreadID:  threadID,
			Raw:       append(json.RawMessage(nil), msg.Params...),
		})
	case "account/rateLimits/updated":
		rateLimits := parseRateLimitSnapshot(msg.Params)
		c.latestRateLimits = rateLimits
		c.onEvent(agent.Event{
			Type:       agent.EventRateLimits,
			Timestamp:  time.Now().UTC(),
			Raw:        append(json.RawMessage(nil), msg.Params...),
			RateLimits: rateLimits,
		})
	}
	return nil
}

func (c *protocolClient) respondUnsupported(ctx context.Context, msg rpcMessage) error {
	switch msg.Method {
	case "item/commandExecution/requestApproval", "execCommandApproval":
		return c.writeResponse(ctx, msg.ID, map[string]any{"decision": "cancel"})
	case "item/fileChange/requestApproval", "applyPatchApproval":
		return c.writeResponse(ctx, msg.ID, map[string]any{"decision": "cancel"})
	case "item/tool/call":
		return c.respondDynamicToolCall(ctx, msg)
	default:
		return c.writeError(ctx, msg.ID, -32601, ErrProtocolUnsupported.Error()+": "+msg.Method)
	}
}

func (c *protocolClient) respondDynamicToolCall(ctx context.Context, msg rpcMessage) error {
	var params struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		CallID    string          `json:"callId"`
		Tool      string          `json:"tool"`
		Namespace string          `json:"namespace"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(msg.Params) == 0 {
		return c.writeDynamicToolResult(ctx, msg.ID, false, ErrProtocolUnsupported.Error())
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return c.writeDynamicToolResult(ctx, msg.ID, false, "invalid dynamic tool call: "+err.Error())
	}
	tool, ok := c.dynamicTools[dynamicToolKey(params.Namespace, params.Tool)]
	if !ok || tool.Handle == nil {
		return c.writeDynamicToolResult(ctx, msg.ID, false, ErrProtocolUnsupported.Error()+": "+params.Tool)
	}
	result, err := tool.Handle(ctx, agent.DynamicToolCall{
		ThreadID:  params.ThreadID,
		TurnID:    params.TurnID,
		CallID:    params.CallID,
		Tool:      params.Tool,
		Namespace: params.Namespace,
		Arguments: params.Arguments,
	})
	if err != nil {
		return c.writeDynamicToolResult(ctx, msg.ID, false, err.Error())
	}
	return c.writeDynamicToolResult(ctx, msg.ID, result.Success, result.Text)
}

func (c *protocolClient) writeDynamicToolResult(ctx context.Context, id json.RawMessage, success bool, text string) error {
	return c.writeResponse(ctx, id, map[string]any{
		"success": success,
		"contentItems": []map[string]string{
			{"type": "inputText", "text": text},
		},
	})
}

func dynamicToolSpecs(tools map[string]agent.DynamicTool) []map[string]any {
	specs := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		spec := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": json.RawMessage(tool.InputSchema),
		}
		if len(tool.InputSchema) == 0 {
			spec["inputSchema"] = map[string]any{"type": "object"}
		}
		if tool.Namespace != "" {
			spec["namespace"] = tool.Namespace
		}
		specs = append(specs, spec)
	}
	return specs
}

func dynamicToolKey(namespace, name string) string {
	return namespace + "/" + name
}

func (c *protocolClient) writeResponse(ctx context.Context, id json.RawMessage, result any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return c.write(map[string]any{
		"id":     id,
		"result": result,
	})
}

func (c *protocolClient) writeError(ctx context.Context, id json.RawMessage, code int, message string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return c.write(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (c *protocolClient) write(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(v)
}

func (c *protocolClient) nextMessage(ctx context.Context) (rpcMessage, error) {
	select {
	case <-ctx.Done():
		return rpcMessage{}, ctx.Err()
	case read, ok := <-c.msgs:
		if !ok {
			return rpcMessage{}, ErrProtocolClosed
		}
		if read.err != nil {
			if c.onEvent != nil {
				c.onEvent(agent.Event{
					Type:      agent.EventMalformed,
					Timestamp: time.Now().UTC(),
					Raw:       append(json.RawMessage(nil), read.raw...),
					Error:     read.err.Error(),
				})
			}
			return rpcMessage{}, read.err
		}
		return read.msg, nil
	}
}

func (c *protocolClient) nextRequestID() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return json.RawMessage(strconv.FormatInt(c.nextID, 10))
}

func extractThreadID(raw json.RawMessage) (string, error) {
	threadID := gjsonString(raw, "thread.id")
	if threadID == "" {
		return "", errors.New("codex response missing thread.id")
	}
	return threadID, nil
}

func extractTurnID(raw json.RawMessage) (string, error) {
	turnID := gjsonString(raw, "turn.id")
	if turnID == "" {
		return "", errors.New("codex response missing turn.id")
	}
	return turnID, nil
}

type rateLimitParams struct {
	RateLimits rateLimitSnapshot `json:"rateLimits"`
}

type rateLimitSnapshot struct {
	LimitID              *string          `json:"limitId"`
	LimitName            *string          `json:"limitName"`
	PlanType             *string          `json:"planType"`
	RateLimitReachedType *string          `json:"rateLimitReachedType"`
	Primary              *rateLimitWindow `json:"primary"`
	Secondary            *rateLimitWindow `json:"secondary"`
	Credits              *creditsSnapshot `json:"credits"`
}

type rateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	ResetsAt           *int64 `json:"resetsAt"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
}

type creditsSnapshot struct {
	Balance    *string `json:"balance"`
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
}

func parseRateLimitSnapshot(raw json.RawMessage) *agent.RateLimitSnapshot {
	var params rateLimitParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil
	}
	snapshot := params.RateLimits
	out := &agent.RateLimitSnapshot{
		LimitID:              stringValue(snapshot.LimitID),
		LimitName:            stringValue(snapshot.LimitName),
		PlanType:             stringValue(snapshot.PlanType),
		RateLimitReachedType: stringValue(snapshot.RateLimitReachedType),
		Primary:              convertRateLimitWindow(snapshot.Primary),
		Secondary:            convertRateLimitWindow(snapshot.Secondary),
		Credits:              convertCredits(snapshot.Credits),
	}
	if out.LimitID == "" && out.LimitName == "" && out.PlanType == "" &&
		out.RateLimitReachedType == "" && out.Primary == nil &&
		out.Secondary == nil && out.Credits == nil {
		return nil
	}
	return out
}

func convertRateLimitWindow(w *rateLimitWindow) *agent.RateLimitWindow {
	if w == nil {
		return nil
	}
	return &agent.RateLimitWindow{
		UsedPercent:        w.UsedPercent,
		ResetsAt:           w.ResetsAt,
		WindowDurationMins: w.WindowDurationMins,
	}
}

func convertCredits(c *creditsSnapshot) *agent.CreditsSnapshot {
	if c == nil {
		return nil
	}
	return &agent.CreditsSnapshot{
		Balance:    stringValue(c.Balance),
		HasCredits: c.HasCredits,
		Unlimited:  c.Unlimited,
	}
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func gjsonString(raw json.RawMessage, path string) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	for _, part := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = obj[part]
	}
	s, _ := v.(string)
	return s
}

func gjsonInt(raw json.RawMessage, path string) int {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0
	}
	for _, part := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return 0
		}
		v = obj[part]
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
