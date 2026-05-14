package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
)

func TestBuildCommand(t *testing.T) {
	t.Parallel()
	r := &Runner{
		Command:    "codex app-server",
		WorkingDir: "/Users/admin/events",
		Env: map[string]string{
			"LINEAR_API_KEY": "lin test",
			"GITHUB_TOKEN":   "don't",
		},
	}
	cmd := r.buildCommand()
	if !strings.Contains(cmd, "cd '/Users/admin/events'") {
		t.Fatalf("missing working directory: %s", cmd)
	}
	if !strings.Contains(cmd, "codex app-server") {
		t.Fatalf("missing codex command: %s", cmd)
	}
	if !strings.Contains(cmd, "export LINEAR_API_KEY='lin test'") {
		t.Fatalf("missing LINEAR_API_KEY export: %s", cmd)
	}
	if !strings.Contains(cmd, "export GITHUB_TOKEN='don'\\''t'") {
		t.Fatalf("missing quoted GITHUB_TOKEN export: %s", cmd)
	}
}

func TestSmokeCheck(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := fmt.Sprintf("CODEX_FAKE_APP_SERVER=1 %s -test.run=TestSmokeCheckFakeAppServer --", shellQuote(os.Args[0]))
	result, err := SmokeCheck(ctx, command, []agent.DynamicTool{SmokeLinearGraphQLTool()})
	if err != nil {
		t.Fatalf("SmokeCheck: %v", err)
	}
	if result.UserAgent != "fake-codex/0" || result.CodexHome != "/tmp/codex" || result.Platform != "macos" || result.ThreadID != "thread-smoke" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSmokeCheckFakeAppServer(t *testing.T) {
	if os.Getenv("CODEX_FAKE_APP_SERVER") != "1" {
		return
	}
	var req rpcMessage
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
		os.Exit(2)
	}
	if req.Method != "initialize" {
		_, _ = fmt.Fprintf(os.Stderr, "method = %s, want initialize\n", req.Method)
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"id": req.ID,
		"result": map[string]any{
			"userAgent":      "fake-codex/0",
			"codexHome":      "/tmp/codex",
			"platformFamily": "unix",
			"platformOs":     "macos",
		},
	}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode response: %v\n", err)
		os.Exit(2)
	}
	var threadReq rpcMessage
	if err := json.NewDecoder(os.Stdin).Decode(&threadReq); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "decode thread request: %v\n", err)
		os.Exit(2)
	}
	if threadReq.Method != "thread/start" {
		_, _ = fmt.Fprintf(os.Stderr, "method = %s, want thread/start\n", threadReq.Method)
		os.Exit(2)
	}
	if !requestHasDynamicTool(threadReq, "linear_graphql") {
		_, _ = fmt.Fprintln(os.Stderr, "thread/start missing linear_graphql")
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"id": threadReq.ID,
		"result": map[string]any{
			"thread": map[string]any{"id": "thread-smoke"},
		},
	}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode thread response: %v\n", err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	if got := shellQuote("don't"); got != "'don'\\''t'" {
		t.Fatalf("shellQuote = %q", got)
	}
}

func TestExtractThreadAndTurnIDs(t *testing.T) {
	t.Parallel()
	threadID, err := extractThreadID(json.RawMessage(`{"thread":{"id":"thread-123"}}`))
	if err != nil {
		t.Fatalf("extractThreadID: %v", err)
	}
	if threadID != "thread-123" {
		t.Fatalf("threadID = %q", threadID)
	}
	turnID, err := extractTurnID(json.RawMessage(`{"turn":{"id":"turn-456"}}`))
	if err != nil {
		t.Fatalf("extractTurnID: %v", err)
	}
	if turnID != "turn-456" {
		t.Fatalf("turnID = %q", turnID)
	}
}

func TestHandleTurnMessageMapsCompletionAndUsage(t *testing.T) {
	t.Parallel()
	client := &protocolClient{}
	result := agent.RunResult{SessionID: "thread-123"}

	usageMsg := rpcMessage{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{
			"threadId":"thread-123",
			"turnId":"turn-456",
			"tokenUsage":{"total":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}
		}`),
	}
	_, completed, err := client.handleTurnMessage(context.Background(), usageMsg, "thread-123", "turn-456", &result)
	if err != nil {
		t.Fatalf("handle usage: %v", err)
	}
	if completed {
		t.Fatalf("usage notification completed the turn")
	}
	if result.Usage.TotalTokens != 30 {
		t.Fatalf("usage = %+v", result.Usage)
	}

	rateLimitMsg := rpcMessage{
		Method: "account/rateLimits/updated",
		Params: json.RawMessage(`{
			"rateLimits":{
				"limitId":"codex",
				"limitName":"Codex",
				"planType":"pro",
				"primary":{"usedPercent":42,"resetsAt":1770000000,"windowDurationMins":300},
				"credits":{"balance":"10.50","hasCredits":true,"unlimited":false}
			}
		}`),
	}
	event, completed, err := client.handleTurnMessage(context.Background(), rateLimitMsg, "thread-123", "turn-456", &result)
	if err != nil {
		t.Fatalf("handle rate limits: %v", err)
	}
	if completed {
		t.Fatalf("rate-limit notification completed the turn")
	}
	if event.Type != agent.EventRateLimits {
		t.Fatalf("event type = %q", event.Type)
	}
	if result.RateLimits == nil || result.RateLimits.LimitID != "codex" {
		t.Fatalf("rate limits = %+v", result.RateLimits)
	}
	if result.RateLimits.Primary == nil || result.RateLimits.Primary.UsedPercent != 42 {
		t.Fatalf("primary rate limit = %+v", result.RateLimits.Primary)
	}

	doneMsg := rpcMessage{
		Method: "turn/completed",
		Params: json.RawMessage(`{
			"threadId":"thread-123",
			"turn":{"id":"turn-456","status":"completed","items":[]}
		}`),
	}
	event, completed, err = client.handleTurnMessage(context.Background(), doneMsg, "thread-123", "turn-456", &result)
	if err != nil {
		t.Fatalf("handle completion: %v", err)
	}
	if !completed {
		t.Fatalf("completion notification did not complete the turn")
	}
	if result.Type != agent.EventTurnCompleted || event.Type != agent.EventTurnCompleted {
		t.Fatalf("types = result %q event %q", result.Type, event.Type)
	}
	if result.ThreadID != "thread-123" || result.TurnID != "turn-456" {
		t.Fatalf("result thread/turn = %q/%q, want thread-123/turn-456", result.ThreadID, result.TurnID)
	}
}

func TestRunProtocolCompletesAgainstFakeAppServer(t *testing.T) {
	t.Parallel()
	serverIn, clientIn := io.Pipe()
	clientOut, serverOut := io.Pipe()
	defer func() { _ = serverIn.Close() }()
	defer func() { _ = clientIn.Close() }()
	defer func() { _ = clientOut.Close() }()
	defer func() { _ = serverOut.Close() }()

	errs := make(chan error, 1)
	go func() {
		dec := json.NewDecoder(serverIn)
		enc := json.NewEncoder(serverOut)
		errs <- runFakeAppServer(dec, enc)
	}()

	var events []agent.Event
	protocol := newProtocolClient(clientIn, clientOut, func(ev agent.Event) {
		events = append(events, ev)
	}).withDynamicTools([]agent.DynamicTool{
		{
			Name:        "linear_graphql",
			Description: "Run Linear GraphQL",
			InputSchema: json.RawMessage(`{"type":"object","required":["query"]}`),
			Handle: func(_ context.Context, call agent.DynamicToolCall) (agent.DynamicToolResult, error) {
				if call.ThreadID != "thread-123" || call.TurnID != "turn-456" || call.Tool != "linear_graphql" {
					t.Fatalf("dynamic tool call = %+v", call)
				}
				return agent.DynamicToolResult{Success: true, Text: `{"issue":{"identifier":"SAM-1"}}`}, nil
			},
		},
	})
	r := &Runner{WorkingDir: "/Users/admin/events"}
	threadID, result, err := r.runProtocol(context.Background(), protocol, "Do the work", "", time.Now(), func(ev agent.Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("runProtocol: %v", err)
	}
	if serverErr := <-errs; serverErr != nil {
		t.Fatalf("fake app-server: %v", serverErr)
	}
	if threadID != "thread-123" || result.SessionID != "thread-123" {
		t.Fatalf("thread/session = %q/%q", threadID, result.SessionID)
	}
	if result.ThreadID != "thread-123" || result.TurnID != "turn-456" {
		t.Fatalf("result thread/turn = %q/%q, want thread-123/turn-456", result.ThreadID, result.TurnID)
	}
	if result.Type != agent.EventTurnCompleted {
		t.Fatalf("result type = %q", result.Type)
	}
	if result.Usage.TotalTokens != 30 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if result.RateLimits == nil || result.RateLimits.Primary == nil || result.RateLimits.Primary.UsedPercent != 42 {
		t.Fatalf("rate limits = %+v", result.RateLimits)
	}
	if len(events) == 0 {
		t.Fatalf("expected normalized events")
	}
}

func TestRunProtocolResumeAdvertisesDynamicTools(t *testing.T) {
	t.Parallel()
	serverIn, clientIn := io.Pipe()
	clientOut, serverOut := io.Pipe()
	defer func() { _ = serverIn.Close() }()
	defer func() { _ = clientIn.Close() }()
	defer func() { _ = clientOut.Close() }()
	defer func() { _ = serverOut.Close() }()

	errs := make(chan error, 1)
	go func() {
		dec := json.NewDecoder(serverIn)
		enc := json.NewEncoder(serverOut)
		errs <- runFakeResumeAppServer(dec, enc)
	}()

	protocol := newProtocolClient(clientIn, clientOut, nil).withDynamicTools([]agent.DynamicTool{
		{
			Name:        "linear_graphql",
			Description: "Run Linear GraphQL",
			InputSchema: json.RawMessage(`{"type":"object","required":["query"]}`),
			Handle: func(context.Context, agent.DynamicToolCall) (agent.DynamicToolResult, error) {
				return agent.DynamicToolResult{Success: true, Text: `{}`}, nil
			},
		},
	})
	r := &Runner{WorkingDir: "/Users/admin/events"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	threadID, result, err := r.runProtocol(ctx, protocol, "Continue the work", "thread-123", time.Now(), nil)
	if err != nil {
		t.Fatalf("runProtocol: %v", err)
	}
	if serverErr := <-errs; serverErr != nil {
		t.Fatalf("fake app-server: %v", serverErr)
	}
	if threadID != "thread-123" || result.SessionID != "thread-123" {
		t.Fatalf("thread/session = %q/%q", threadID, result.SessionID)
	}
	if result.ThreadID != "thread-123" || result.TurnID != "turn-789" {
		t.Fatalf("result thread/turn = %q/%q, want thread-123/turn-789", result.ThreadID, result.TurnID)
	}
	if result.Type != agent.EventTurnCompleted {
		t.Fatalf("result type = %q", result.Type)
	}
}

func TestRunProtocolUsesConfiguredApprovalAndSandbox(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		resume       string
		threadMethod string
	}{
		{name: "start", threadMethod: "thread/start"},
		{name: "resume", resume: "thread-123", threadMethod: "thread/resume"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			serverIn, clientIn := io.Pipe()
			clientOut, serverOut := io.Pipe()
			defer func() { _ = serverIn.Close() }()
			defer func() { _ = clientIn.Close() }()
			defer func() { _ = clientOut.Close() }()
			defer func() { _ = serverOut.Close() }()

			errs := make(chan error, 1)
			go func() {
				dec := json.NewDecoder(serverIn)
				enc := json.NewEncoder(serverOut)
				errs <- runFakeRuntimeConfigAppServer(dec, enc, tc.threadMethod)
			}()

			protocol := newProtocolClient(clientIn, clientOut, nil)
			r := &Runner{
				WorkingDir:        "/Users/admin/events",
				ApprovalPolicy:    "on-request",
				ThreadSandbox:     "workspace-write",
				TurnSandboxPolicy: map[string]any{"type": "workspaceWrite", "writableRoots": []any{"/Users/admin/events"}},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			threadID, result, err := r.runProtocol(ctx, protocol, "Do the work", tc.resume, time.Now(), nil)
			if err != nil {
				t.Fatalf("runProtocol: %v", err)
			}
			if serverErr := <-errs; serverErr != nil {
				t.Fatalf("fake app-server: %v", serverErr)
			}
			if threadID != "thread-123" || result.Type != agent.EventTurnCompleted {
				t.Fatalf("threadID/result = %q/%q", threadID, result.Type)
			}
		})
	}
}

func runFakeAppServer(dec *json.Decoder, enc *json.Encoder) error {
	initReq, err := readFakeRequest(dec, "initialize")
	if err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id":     initReq.ID,
		"result": map[string]any{"userAgent": "fake-codex"},
	}); err != nil {
		return err
	}

	threadReq, err := readFakeRequest(dec, "thread/start")
	if err != nil {
		return err
	}
	if !requestHasDynamicTool(threadReq, "linear_graphql") {
		return &fakeServerError{"thread/start missing linear_graphql dynamic tool"}
	}
	if err := assertThreadRuntimeParams(threadReq, "never", "danger-full-access"); err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id": threadReq.ID,
		"result": map[string]any{
			"thread": map[string]any{"id": "thread-123"},
		},
	}); err != nil {
		return err
	}

	turnReq, err := readFakeRequest(dec, "turn/start")
	if err != nil {
		return err
	}
	if got := gjsonString(turnReq.Params, "threadId"); got != "thread-123" {
		return &fakeServerError{"turn/start threadId = " + got}
	}
	if err := assertTurnRuntimeParams(turnReq, "never", "dangerFullAccess"); err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id": turnReq.ID,
		"result": map[string]any{
			"turn": map[string]any{"id": "turn-456"},
		},
	}); err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id":     "tool-request-1",
		"method": "item/tool/call",
		"params": map[string]any{
			"threadId":  "thread-123",
			"turnId":    "turn-456",
			"callId":    "call-1",
			"tool":      "linear_graphql",
			"arguments": map[string]any{"query": "query { issue(id: \"uuid-1\") { identifier } }"},
		},
	}); err != nil {
		return err
	}
	toolResp, err := readFakeResponse(dec, json.RawMessage(`"tool-request-1"`))
	if err != nil {
		return err
	}
	if !gjsonBool(toolResp.Result, "success") {
		return &fakeServerError{"dynamic tool response success=false"}
	}
	if err := enc.Encode(map[string]any{
		"method": "account/rateLimits/updated",
		"params": map[string]any{
			"rateLimits": map[string]any{
				"limitId":   "codex",
				"limitName": "Codex",
				"primary": map[string]any{
					"usedPercent": 42,
				},
			},
		},
	}); err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"method": "thread/tokenUsage/updated",
		"params": map[string]any{
			"threadId": "thread-123",
			"turnId":   "turn-456",
			"tokenUsage": map[string]any{
				"total": map[string]any{
					"inputTokens":  10,
					"outputTokens": 20,
					"totalTokens":  30,
				},
			},
		},
	}); err != nil {
		return err
	}
	return enc.Encode(map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": "thread-123",
			"turn": map[string]any{
				"id":     "turn-456",
				"status": "completed",
				"items":  []any{},
			},
		},
	})
}

func runFakeResumeAppServer(dec *json.Decoder, enc *json.Encoder) error {
	initReq, err := readFakeRequest(dec, "initialize")
	if err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id":     initReq.ID,
		"result": map[string]any{"userAgent": "fake-codex"},
	}); err != nil {
		return err
	}

	threadReq, err := readFakeRequest(dec, "thread/resume")
	if err != nil {
		return err
	}
	if got := gjsonString(threadReq.Params, "threadId"); got != "thread-123" {
		return &fakeServerError{"thread/resume threadId = " + got}
	}
	if !requestHasDynamicTool(threadReq, "linear_graphql") {
		return &fakeServerError{"thread/resume missing linear_graphql dynamic tool"}
	}
	if err := assertThreadRuntimeParams(threadReq, "never", "danger-full-access"); err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id": threadReq.ID,
		"result": map[string]any{
			"thread": map[string]any{"id": "thread-123"},
		},
	}); err != nil {
		return err
	}

	turnReq, err := readFakeRequest(dec, "turn/start")
	if err != nil {
		return err
	}
	if got := gjsonString(turnReq.Params, "threadId"); got != "thread-123" {
		return &fakeServerError{"turn/start threadId = " + got}
	}
	if err := assertTurnRuntimeParams(turnReq, "never", "dangerFullAccess"); err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id": turnReq.ID,
		"result": map[string]any{
			"turn": map[string]any{"id": "turn-789"},
		},
	}); err != nil {
		return err
	}
	return enc.Encode(map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": "thread-123",
			"turn": map[string]any{
				"id":     "turn-789",
				"status": "completed",
				"items":  []any{},
			},
		},
	})
}

func runFakeRuntimeConfigAppServer(dec *json.Decoder, enc *json.Encoder, threadMethod string) error {
	initReq, err := readFakeRequest(dec, "initialize")
	if err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id":     initReq.ID,
		"result": map[string]any{"userAgent": "fake-codex"},
	}); err != nil {
		return err
	}

	threadReq, err := readFakeRequest(dec, threadMethod)
	if err != nil {
		return err
	}
	if threadMethod == "thread/resume" {
		if got := gjsonString(threadReq.Params, "threadId"); got != "thread-123" {
			return &fakeServerError{"thread/resume threadId = " + got}
		}
	}
	if err := assertThreadRuntimeParams(threadReq, "on-request", "workspace-write"); err != nil {
		return err
	}
	if err := enc.Encode(map[string]any{
		"id": threadReq.ID,
		"result": map[string]any{
			"thread": map[string]any{"id": "thread-123"},
		},
	}); err != nil {
		return err
	}

	turnReq, err := readFakeRequest(dec, "turn/start")
	if err != nil {
		return err
	}
	if got := gjsonString(turnReq.Params, "threadId"); got != "thread-123" {
		return &fakeServerError{"turn/start threadId = " + got}
	}
	if err := assertTurnRuntimeParams(turnReq, "on-request", "workspaceWrite"); err != nil {
		return err
	}
	if got := sandboxPolicyWritableRoot(turnReq.Params); got != "/Users/admin/events" {
		return &fakeServerError{"turn/start sandboxPolicy.writableRoots[0] = " + got}
	}
	if err := enc.Encode(map[string]any{
		"id": turnReq.ID,
		"result": map[string]any{
			"turn": map[string]any{"id": "turn-custom"},
		},
	}); err != nil {
		return err
	}
	return enc.Encode(map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": "thread-123",
			"turn": map[string]any{
				"id":     "turn-custom",
				"status": "completed",
				"items":  []any{},
			},
		},
	})
}

func assertThreadRuntimeParams(req rpcMessage, approvalPolicy, sandbox string) error {
	if got := gjsonString(req.Params, "approvalPolicy"); got != approvalPolicy {
		return &fakeServerError{req.Method + " approvalPolicy = " + got}
	}
	if got := gjsonString(req.Params, "sandbox"); got != sandbox {
		return &fakeServerError{req.Method + " sandbox = " + got}
	}
	return nil
}

func assertTurnRuntimeParams(req rpcMessage, approvalPolicy, sandboxPolicyType string) error {
	if got := gjsonString(req.Params, "approvalPolicy"); got != approvalPolicy {
		return &fakeServerError{req.Method + " approvalPolicy = " + got}
	}
	if got := gjsonString(req.Params, "sandboxPolicy.type"); got != sandboxPolicyType {
		return &fakeServerError{req.Method + " sandboxPolicy.type = " + got}
	}
	return nil
}

func sandboxPolicyWritableRoot(raw json.RawMessage) string {
	var params struct {
		SandboxPolicy struct {
			WritableRoots []string `json:"writableRoots"`
		} `json:"sandboxPolicy"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || len(params.SandboxPolicy.WritableRoots) == 0 {
		return ""
	}
	return params.SandboxPolicy.WritableRoots[0]
}

func readFakeRequest(dec *json.Decoder, method string) (rpcMessage, error) {
	var req rpcMessage
	if err := dec.Decode(&req); err != nil {
		return rpcMessage{}, err
	}
	if req.Method != method {
		return rpcMessage{}, &fakeServerError{"method = " + req.Method + ", want " + method}
	}
	return req, nil
}

func readFakeResponse(dec *json.Decoder, id json.RawMessage) (rpcMessage, error) {
	var resp rpcMessage
	if err := dec.Decode(&resp); err != nil {
		return rpcMessage{}, err
	}
	if !bytes.Equal(resp.ID, id) {
		return rpcMessage{}, &fakeServerError{"response id = " + string(resp.ID) + ", want " + string(id)}
	}
	return resp, nil
}

func requestHasDynamicTool(req rpcMessage, name string) bool {
	var params struct {
		DynamicTools []struct {
			Name string `json:"name"`
		} `json:"dynamicTools"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return false
	}
	for _, tool := range params.DynamicTools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func gjsonBool(raw json.RawMessage, path string) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	for _, part := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return false
		}
		v = obj[part]
	}
	b, _ := v.(bool)
	return b
}

type fakeServerError struct {
	msg string
}

func (e *fakeServerError) Error() string {
	return e.msg
}

func TestRespondUnsupportedDynamicToolCall(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	client := &protocolClient{enc: json.NewEncoder(&buf)}
	msg := rpcMessage{
		ID:     json.RawMessage(`7`),
		Method: "item/tool/call",
	}
	if err := client.respondUnsupported(context.Background(), msg); err != nil {
		t.Fatalf("respondUnsupported: %v", err)
	}
	var got struct {
		ID     int `json:"id"`
		Result struct {
			Success      bool `json:"success"`
			ContentItems []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"contentItems"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if got.ID != 7 || got.Result.Success {
		t.Fatalf("response = %+v", got)
	}
	if len(got.Result.ContentItems) != 1 || got.Result.ContentItems[0].Text != ErrProtocolUnsupported.Error() {
		t.Fatalf("contentItems = %+v", got.Result.ContentItems)
	}
}

func TestRespondDynamicToolCall(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	client := (&protocolClient{enc: json.NewEncoder(&buf)}).withDynamicTools([]agent.DynamicTool{
		{
			Name: "linear_graphql",
			Handle: func(_ context.Context, call agent.DynamicToolCall) (agent.DynamicToolResult, error) {
				if call.ThreadID != "thread-123" || call.TurnID != "turn-456" || call.Tool != "linear_graphql" {
					t.Fatalf("call = %+v", call)
				}
				if gjsonString(call.Arguments, "query") != "query Test { viewer { id } }" {
					t.Fatalf("arguments = %s", string(call.Arguments))
				}
				return agent.DynamicToolResult{Success: true, Text: `{"viewer":{"id":"me"}}`}, nil
			},
		},
	})
	msg := rpcMessage{
		ID:     json.RawMessage(`9`),
		Method: "item/tool/call",
		Params: json.RawMessage(`{
			"threadId":"thread-123",
			"turnId":"turn-456",
			"callId":"call-1",
			"tool":"linear_graphql",
			"arguments":{"query":"query Test { viewer { id } }"}
		}`),
	}
	if err := client.respondUnsupported(context.Background(), msg); err != nil {
		t.Fatalf("respondUnsupported: %v", err)
	}
	var got struct {
		ID     int `json:"id"`
		Result struct {
			Success      bool `json:"success"`
			ContentItems []struct {
				Text string `json:"text"`
			} `json:"contentItems"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if got.ID != 9 || !got.Result.Success {
		t.Fatalf("response = %+v", got)
	}
	if len(got.Result.ContentItems) != 1 || got.Result.ContentItems[0].Text != `{"viewer":{"id":"me"}}` {
		t.Fatalf("contentItems = %+v", got.Result.ContentItems)
	}
}
