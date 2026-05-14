package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/domain"
	"github.com/sasilver75/events/orchestrator/internal/tracker/linear"
	"github.com/sasilver75/events/orchestrator/internal/workflow"
)

func TestBackoffDelay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		attempt, max int
		want         time.Duration
	}{
		{1, 300000, 10 * time.Second},
		{2, 300000, 20 * time.Second},
		{3, 300000, 40 * time.Second},
		{4, 300000, 80 * time.Second},
		{5, 300000, 160 * time.Second},
		{6, 300000, 300 * time.Second}, // capped
		{1, 0, 10 * time.Second},       // 0 max → default
	}
	for _, tc := range cases {
		got := backoffDelay(tc.attempt, tc.max)
		if got != tc.want {
			t.Errorf("backoffDelay(%d, %d) = %v, want %v", tc.attempt, tc.max, got, tc.want)
		}
	}
}

func TestSortDispatch(t *testing.T) {
	t.Parallel()
	p1, p2, p3 := 1, 2, 3
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issues := []domain.Issue{
		{Identifier: "SAM-3", Priority: &p2, CreatedAt: t0.Add(2 * time.Hour)},
		{Identifier: "SAM-1", Priority: &p1, CreatedAt: t0},
		{Identifier: "SAM-5", Priority: nil, CreatedAt: t0},
		{Identifier: "SAM-2", Priority: &p2, CreatedAt: t0},
		{Identifier: "SAM-4", Priority: &p3, CreatedAt: t0},
	}
	sortDispatch(issues)

	got := []string{}
	for _, i := range issues {
		got = append(got, i.Identifier)
	}
	want := []string{"SAM-1", "SAM-2", "SAM-3", "SAM-4", "SAM-5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sort order = %v, want %v", got, want)
	}
}

// fakeTracker is a Tracker that returns canned data.
type fakeTracker struct {
	candidates []domain.Issue
	states     map[string]string
	terminal   []domain.Issue
}

func (f *fakeTracker) FetchCandidateIssues(ctx context.Context, _ []string) ([]domain.Issue, error) {
	return f.candidates, nil
}

func (f *fakeTracker) FetchIssueStatesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if s, ok := f.states[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (f *fakeTracker) FetchIssuesByStates(ctx context.Context, _ []string) ([]domain.Issue, error) {
	return f.terminal, nil
}

type escalatingFakeTracker struct {
	*fakeTracker
	mu              sync.Mutex
	escalationCalls []needsHumanEscalation
	err             error
}

func (f *escalatingFakeTracker) EscalateNeedsHuman(ctx context.Context, issue domain.Issue, reason string, attempts int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.escalationCalls = append(f.escalationCalls, needsHumanEscalation{issue: issue, reason: reason, attempts: attempts})
	return f.err
}

// recordingWorker captures every issue it was asked to run, then reports
// the canned result.
type recordingWorker struct {
	mu             sync.Mutex
	calls          []string
	attempts       []*int
	resumes        []string
	cleanupCalls   []string
	workflowCfg    workflow.ServiceConfig
	workflowPrompt string
	canceled       bool
	waitForCancel  bool
	result         WorkerResult
	delay          time.Duration
	eventToEmit    *agent.Event
}

func (w *recordingWorker) Run(ctx context.Context, issue domain.Issue, attempt *int, resumeSessionID string, onEvent func(agent.Event)) WorkerResult {
	w.mu.Lock()
	w.calls = append(w.calls, issue.Identifier)
	w.attempts = append(w.attempts, attempt)
	w.resumes = append(w.resumes, resumeSessionID)
	eventToEmit := w.eventToEmit
	w.mu.Unlock()
	if eventToEmit != nil && onEvent != nil {
		onEvent(*eventToEmit)
	}
	if w.waitForCancel {
		<-ctx.Done()
		w.mu.Lock()
		w.canceled = true
		w.mu.Unlock()
		return WorkerResult{Issue: issue, Status: domain.RunStatusCanceledByReconciliation}
	}
	if w.delay > 0 {
		select {
		case <-time.After(w.delay):
		case <-ctx.Done():
			w.mu.Lock()
			w.canceled = true
			w.mu.Unlock()
			return WorkerResult{Issue: issue, Status: domain.RunStatusCanceledByReconciliation}
		}
	}
	res := w.result
	res.Issue = issue
	return res
}

func (w *recordingWorker) Cleanup(ctx context.Context, issue domain.Issue) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cleanupCalls = append(w.cleanupCalls, issue.Identifier)
	return nil
}

func (w *recordingWorker) UpdateWorkflow(def *workflow.Definition, cfg workflow.ServiceConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.workflowCfg = cfg
	w.workflowPrompt = def.PromptTemplate
}

func TestSpurWorkerAgentTurnConfigCredentialModes(t *testing.T) {
	t.Parallel()
	tool := agent.DynamicTool{Name: "linear_graphql"}
	w := &SpurWorker{
		HarnessCreds: Credentials{
			GitHubToken: "gh_test",
			LinearToken: "lin_test",
		},
		DynamicTools: []agent.DynamicTool{tool},
	}

	vmEnvCfg := validConfig()
	vmEnvCfg.Credentials.LinearAccess = "vm_env"
	vmEnvTurn := w.agentTurnConfig(vmEnvCfg)
	if vmEnvTurn.Env["GITHUB_TOKEN"] != "gh_test" {
		t.Fatalf("GITHUB_TOKEN = %q", vmEnvTurn.Env["GITHUB_TOKEN"])
	}
	if vmEnvTurn.Env["LINEAR_API_KEY"] != "lin_test" {
		t.Fatalf("LINEAR_API_KEY = %q", vmEnvTurn.Env["LINEAR_API_KEY"])
	}
	if len(vmEnvTurn.DynamicTools) != 0 {
		t.Fatalf("vm_env dynamic tools = %+v", vmEnvTurn.DynamicTools)
	}

	hostProxyCfg := validConfig()
	hostProxyCfg.Agent.Runner = "codex"
	hostProxyCfg.Credentials.LinearAccess = "host_proxy"
	hostProxyTurn := w.agentTurnConfig(hostProxyCfg)
	if hostProxyTurn.Env["GITHUB_TOKEN"] != "gh_test" {
		t.Fatalf("GITHUB_TOKEN = %q", hostProxyTurn.Env["GITHUB_TOKEN"])
	}
	if _, ok := hostProxyTurn.Env["LINEAR_API_KEY"]; ok {
		t.Fatalf("host_proxy should not pass LINEAR_API_KEY: %+v", hostProxyTurn.Env)
	}
	if len(hostProxyTurn.DynamicTools) != 1 || hostProxyTurn.DynamicTools[0].Name != "linear_graphql" {
		t.Fatalf("host_proxy dynamic tools = %+v", hostProxyTurn.DynamicTools)
	}

	customCfg := validConfig()
	customCfg.Codex.ApprovalPolicy = "on-request"
	customCfg.Codex.ThreadSandbox = "workspace-write"
	customCfg.Codex.TurnSandboxPolicy = map[string]any{"type": "workspaceWrite"}
	customTurn := w.agentTurnConfig(customCfg)
	if customTurn.ApprovalPolicy != "on-request" {
		t.Fatalf("ApprovalPolicy = %q", customTurn.ApprovalPolicy)
	}
	if customTurn.ThreadSandbox != "workspace-write" {
		t.Fatalf("ThreadSandbox = %q", customTurn.ThreadSandbox)
	}
	if got := customTurn.TurnSandboxPolicy.(map[string]any)["type"]; got != "workspaceWrite" {
		t.Fatalf("TurnSandboxPolicy type = %v", got)
	}
}

func TestRunOnce_DispatchesSingleIssue(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{
		ID:         "uuid-1",
		Identifier: "SAM-12",
		Title:      "Test",
		State:      "Ready",
		Labels:     []string{"afk"},
	}
	tracker := &fakeTracker{candidates: []domain.Issue{issue}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusSucceeded}}

	o := New(nil, validConfig(), tracker, worker, silentLogger())
	if err := o.RunOnce(context.Background(), "SAM-12"); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.calls) != 1 || worker.calls[0] != "SAM-12" {
		t.Errorf("worker calls = %v", worker.calls)
	}
}

func TestRunOnce_NoEligibleIssues(t *testing.T) {
	t.Parallel()
	// Has issue but missing AFK label — fails eligibility.
	issue := domain.Issue{
		ID:         "uuid-1",
		Identifier: "SAM-12",
		Title:      "Test",
		State:      "Ready",
		Labels:     []string{"feature"},
	}
	tracker := &fakeTracker{candidates: []domain.Issue{issue}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusSucceeded}}

	o := New(nil, validConfig(), tracker, worker, silentLogger())
	if err := o.RunOnce(context.Background(), ""); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.calls) != 0 {
		t.Errorf("worker should not have been called: %v", worker.calls)
	}
}

func TestRunOnce_ExplicitIssueMustBeEligible(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{
		ID:         "uuid-1",
		Identifier: "SAM-12",
		Title:      "Test",
		State:      "Ready",
		Labels:     []string{"feature"},
	}
	tracker := &fakeTracker{candidates: []domain.Issue{issue}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusSucceeded}}

	o := New(nil, validConfig(), tracker, worker, silentLogger())
	err := o.RunOnce(context.Background(), "SAM-12")
	if err == nil {
		t.Fatal("expected eligibility error")
	}
	if !strings.Contains(err.Error(), "missing_required_label:afk") {
		t.Fatalf("error = %q, want missing afk reason", err)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.calls) != 0 {
		t.Fatalf("worker should not have been called: %v", worker.calls)
	}
}

func TestRunOnce_ExplicitIssueRejectsOtherAssignee(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{
		ID:         "uuid-1",
		Identifier: "SAM-12",
		Title:      "Test",
		State:      "Ready",
		Labels:     []string{"afk"},
		AssigneeID: "user-other",
	}
	tracker := &fakeTracker{candidates: []domain.Issue{issue}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusSucceeded}}

	o := New(nil, validConfig(), tracker, worker, silentLogger())
	o.Eligibility.CurrentUserID = "user-current"
	err := o.RunOnce(context.Background(), "SAM-12")
	if err == nil {
		t.Fatal("expected eligibility error")
	}
	if !strings.Contains(err.Error(), "assigned_to_other:user-other") {
		t.Fatalf("error = %q, want assigned_to_other reason", err)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.calls) != 0 {
		t.Fatalf("worker should not have been called: %v", worker.calls)
	}
}

func TestRunOnce_IssueNotFound(t *testing.T) {
	t.Parallel()
	tracker := &fakeTracker{candidates: nil}
	worker := &recordingWorker{}
	o := New(nil, validConfig(), tracker, worker, silentLogger())
	err := o.RunOnce(context.Background(), "SAM-99")
	if err == nil {
		t.Error("expected error for unknown issue")
	}
}

func TestRunOnce_PropagatesWorkerFailure(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{
		ID:         "uuid-1",
		Identifier: "SAM-12",
		State:      "Ready",
		Labels:     []string{"afk"},
	}
	tracker := &fakeTracker{candidates: []domain.Issue{issue}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusFailed, Error: "boom"}}
	o := New(nil, validConfig(), tracker, worker, silentLogger())
	err := o.RunOnce(context.Background(), "SAM-12")
	if err == nil {
		t.Error("expected error from failed worker")
	}
}

func TestRunOnceWritesStatusSnapshotWithTelemetry(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{
		ID:         "uuid-1",
		Identifier: "SAM-12",
		State:      "Ready",
		Labels:     []string{"afk"},
	}
	tracker := &fakeTracker{candidates: []domain.Issue{issue}}
	worker := &recordingWorker{result: WorkerResult{
		Status:       domain.RunStatusSucceeded,
		InputTokens:  11,
		OutputTokens: 7,
		TotalTokens:  18,
		SessionID:    "thread-123",
		ThreadID:     "thread-123",
		TurnID:       "turn-456",
		RateLimits: &agent.RateLimitSnapshot{
			LimitID: "codex",
		},
	}}
	statusPath := filepath.Join(t.TempDir(), "status", "once.json")
	cfg := validConfig()
	cfg.Agent.Runner = "codex"
	cfg.Credentials.LinearAccess = "host_proxy"
	cfg.Codex.Command = "codex app-server"
	o := New(nil, cfg, tracker, worker, silentLogger())
	o.StatusFile = statusPath

	if err := o.RunOnce(context.Background(), "SAM-12"); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var snapshot StatusSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, data)
	}
	if snapshot.CodexTotals.InputTokens != 11 || snapshot.CodexTotals.OutputTokens != 7 || snapshot.CodexTotals.TotalTokens != 18 {
		t.Fatalf("codex_totals = %+v", snapshot.CodexTotals)
	}
	if snapshot.AgentRunner != "codex" || snapshot.LinearAccess != "host_proxy" {
		t.Fatalf("runner/access = %q/%q, want codex/host_proxy", snapshot.AgentRunner, snapshot.LinearAccess)
	}
	if snapshot.CompletedCount != 1 {
		t.Fatalf("completed_count = %d, want 1", snapshot.CompletedCount)
	}
	if len(snapshot.RecentRuns) != 1 {
		t.Fatalf("recent_runs = %+v, want one run", snapshot.RecentRuns)
	}
	recentRun := snapshot.RecentRuns[0]
	if recentRun.Identifier != "SAM-12" || recentRun.Status != string(domain.RunStatusSucceeded) {
		t.Fatalf("recent run = %+v", recentRun)
	}
	if recentRun.SessionID != "thread-123" {
		t.Fatalf("recent run session = %q, want thread-123", recentRun.SessionID)
	}
	if recentRun.ThreadID != "thread-123" || recentRun.TurnID != "turn-456" {
		t.Fatalf("recent run thread/turn = %q/%q, want thread-123/turn-456", recentRun.ThreadID, recentRun.TurnID)
	}
	if recentRun.TokenInfo.InputTokens != 11 || recentRun.TokenInfo.OutputTokens != 7 || recentRun.TokenInfo.TotalTokens != 18 {
		t.Fatalf("recent token_info = %+v", recentRun.TokenInfo)
	}
	if recentRun.RateLimits == nil {
		t.Fatal("recent rate_limits missing")
	}
	if recentRun.DurationMs < 0 {
		t.Fatalf("duration_ms = %d, want non-negative", recentRun.DurationMs)
	}
	if snapshot.CodexRateLimits == nil {
		t.Fatal("codex_rate_limits missing")
	}
}

func TestRunOnceFailsWhenStatusSnapshotCannotBeWritten(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{
		ID:         "uuid-1",
		Identifier: "SAM-12",
		State:      "Ready",
		Labels:     []string{"afk"},
	}
	tracker := &fakeTracker{candidates: []domain.Issue{issue}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusSucceeded}}
	statusDir := t.TempDir()
	o := New(nil, validConfig(), tracker, worker, silentLogger())
	o.StatusFile = statusDir

	err := o.RunOnce(context.Background(), "SAM-12")
	if err == nil {
		t.Fatal("expected status snapshot error")
	}
	if !strings.Contains(err.Error(), "status snapshot:") {
		t.Fatalf("error = %q, want status snapshot context", err)
	}
}

// TestTick_RespectsMaxConcurrentAgents verifies that with 2 candidates
// and max=1, only one dispatch happens per tick.
func TestTick_RespectsMaxConcurrentAgents(t *testing.T) {
	t.Parallel()
	p1 := 1
	issues := []domain.Issue{
		{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}, Priority: &p1},
		{ID: "uuid-2", Identifier: "SAM-2", State: "Ready", Labels: []string{"afk"}, Priority: &p1},
	}
	tracker := &fakeTracker{candidates: issues}
	worker := &recordingWorker{
		result: WorkerResult{Status: domain.RunStatusSucceeded},
		delay:  100 * time.Millisecond,
	}
	cfg := validConfig()
	cfg.Agent.MaxConcurrentAgents = 1

	o := New(nil, cfg, tracker, worker, silentLogger())
	_ = o.tick(context.Background())

	// Give the dispatched worker a chance to start (but not finish).
	time.Sleep(20 * time.Millisecond)
	o.mu.Lock()
	running := len(o.state.Running)
	o.mu.Unlock()
	if running != 1 {
		t.Errorf("running = %d, want 1", running)
	}
}

func TestDispatchRespectsPerStateCap(t *testing.T) {
	t.Parallel()
	issues := []domain.Issue{
		{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}},
		{ID: "uuid-2", Identifier: "SAM-2", State: "READY", Labels: []string{"afk"}},
		{ID: "uuid-3", Identifier: "SAM-3", State: "In Progress", Labels: []string{"afk"}},
	}
	worker := &recordingWorker{waitForCancel: true}
	cfg := validConfig()
	cfg.Agent.MaxConcurrentAgents = 3
	cfg.Agent.MaxConcurrentAgentsByState = map[string]int{"ready": 1}
	o := New(nil, cfg, &fakeTracker{}, worker, silentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o.dispatchUpToCapacity(ctx, issues)

	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.state.Running) != 2 {
		t.Fatalf("running = %d, want 2", len(o.state.Running))
	}
	if _, running := o.state.Running["uuid-1"]; !running {
		t.Fatal("SAM-1 should be running")
	}
	if _, running := o.state.Running["uuid-2"]; running {
		t.Fatal("SAM-2 should be skipped by normalized ready cap")
	}
	if _, running := o.state.Running["uuid-3"]; !running {
		t.Fatal("SAM-3 should still dispatch because in progress has no state cap")
	}
}

func TestDispatchWithoutPerStateCapsFallsBackToGlobalLimit(t *testing.T) {
	t.Parallel()
	issues := []domain.Issue{
		{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}},
		{ID: "uuid-2", Identifier: "SAM-2", State: "READY", Labels: []string{"afk"}},
	}
	worker := &recordingWorker{waitForCancel: true}
	cfg := validConfig()
	cfg.Agent.MaxConcurrentAgents = 2
	cfg.Agent.MaxConcurrentAgentsByState = nil
	o := New(nil, cfg, &fakeTracker{}, worker, silentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o.dispatchUpToCapacity(ctx, issues)

	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.state.Running) != 2 {
		t.Fatalf("running = %d, want 2", len(o.state.Running))
	}
}

func TestHandleWorkerResult_StopsAtMaxTurns(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1"}
	cfg := validConfig()
	cfg.Agent.MaxTurns = 1
	o := New(nil, cfg, &fakeTracker{}, &recordingWorker{}, silentLogger())
	o.state.Claimed[issue.ID] = struct{}{}

	o.handleWorkerResult(WorkerResult{Issue: issue, Status: domain.RunStatusSucceeded})

	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.state.RetryAttempts) != 0 {
		t.Fatalf("retry attempts = %d, want 0", len(o.state.RetryAttempts))
	}
	if _, claimed := o.state.Claimed[issue.ID]; !claimed {
		t.Fatal("issue claim was released after max_turns; want retained for operator review")
	}
}

func TestHandleWorkerResult_StopsSuccessfulContinuationLoop(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1"}
	cfg := validConfig()
	cfg.Agent.MaxUnproductiveSuccess = 2
	o := New(nil, cfg, &fakeTracker{}, &recordingWorker{}, silentLogger())
	o.state.Claimed[issue.ID] = struct{}{}

	o.handleWorkerResult(WorkerResult{Issue: issue, Status: domain.RunStatusSucceeded})
	firstRetry := o.retryTimers[issue.ID]
	if firstRetry == nil {
		t.Fatal("first successful turn did not schedule continuation")
	}

	attempt := 1
	o.handleWorkerResult(WorkerResult{Issue: issue, Attempt: &attempt, Status: domain.RunStatusSucceeded})

	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.state.RetryAttempts) != 0 {
		t.Fatalf("retry attempts = %d, want 0 after loop detection", len(o.state.RetryAttempts))
	}
	if _, claimed := o.state.Claimed[issue.ID]; !claimed {
		t.Fatal("issue claim was released after loop detection; want retained for operator review")
	}
	entry, ok := o.state.NeedsHuman[issue.ID]
	if !ok {
		t.Fatal("issue was not marked as needing human review")
	}
	if entry.Attempts != 2 || entry.Reason != "successful_continuation_loop" {
		t.Fatalf("needs-human entry = %+v", entry)
	}
}

func TestHandleWorkerResult_EscalatesSuccessfulContinuationLoop(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1"}
	cfg := validConfig()
	cfg.Agent.MaxUnproductiveSuccess = 2
	tracker := &escalatingFakeTracker{fakeTracker: &fakeTracker{}}
	o := New(nil, cfg, tracker, &recordingWorker{}, silentLogger())
	o.state.Claimed[issue.ID] = struct{}{}

	o.handleWorkerResult(WorkerResult{Issue: issue, Status: domain.RunStatusSucceeded})
	attempt := 1
	o.handleWorkerResult(WorkerResult{Issue: issue, Attempt: &attempt, Status: domain.RunStatusSucceeded})

	tracker.mu.Lock()
	if len(tracker.escalationCalls) != 1 {
		t.Fatalf("escalation calls = %d, want 1", len(tracker.escalationCalls))
	}
	call := tracker.escalationCalls[0]
	tracker.mu.Unlock()
	if call.issue.Identifier != "SAM-1" || call.reason != "successful_continuation_loop" || call.attempts != 2 {
		t.Fatalf("escalation call = %+v", call)
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.state.NeedsHuman[issue.ID]
	if entry.EscalatedAt.IsZero() {
		t.Fatalf("needs-human entry was not marked escalated: %+v", entry)
	}
	if entry.EscalationError != "" {
		t.Fatalf("escalation error = %q, want empty", entry.EscalationError)
	}
}

func TestHandleWorkerResult_AccumulatesTelemetry(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1"}
	cfg := validConfig()
	cfg.Agent.MaxUnproductiveSuccess = 0
	o := New(nil, cfg, &fakeTracker{}, &recordingWorker{}, silentLogger())
	o.state.Running[issue.ID] = domain.RunningEntry{
		Issue:     issue,
		StartedAt: time.Now().Add(-2 * time.Second),
	}
	o.state.Claimed[issue.ID] = struct{}{}

	o.handleWorkerResult(WorkerResult{
		Issue:        issue,
		Status:       domain.RunStatusSucceeded,
		InputTokens:  100,
		OutputTokens: 25,
		TotalTokens:  125,
		RateLimits: &agent.RateLimitSnapshot{
			LimitID: "codex",
			Primary: &agent.RateLimitWindow{
				UsedPercent: 42,
			},
		},
	})

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state.CodexTotals.InputTokens != 100 {
		t.Fatalf("input tokens = %d, want 100", o.state.CodexTotals.InputTokens)
	}
	if o.state.CodexTotals.OutputTokens != 25 {
		t.Fatalf("output tokens = %d, want 25", o.state.CodexTotals.OutputTokens)
	}
	if o.state.CodexTotals.TotalTokens != 125 {
		t.Fatalf("total tokens = %d, want 125", o.state.CodexTotals.TotalTokens)
	}
	if o.state.CodexTotals.SecondsRunning < 1 {
		t.Fatalf("seconds running = %d, want at least 1", o.state.CodexTotals.SecondsRunning)
	}
	rateLimits, ok := o.state.CodexRateLimits.(*agent.RateLimitSnapshot)
	if !ok {
		t.Fatalf("codex rate limits type = %T", o.state.CodexRateLimits)
	}
	if rateLimits.LimitID != "codex" || rateLimits.Primary == nil || rateLimits.Primary.UsedPercent != 42 {
		t.Fatalf("codex rate limits = %+v", rateLimits)
	}
}

func TestRecordAgentEventUpdatesLiveSessionTelemetry(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1"}
	o := New(nil, validConfig(), &fakeTracker{}, &recordingWorker{}, silentLogger())
	o.state.Running[issue.ID] = domain.RunningEntry{Issue: issue, StartedAt: time.Now()}
	eventAt := time.Date(2026, 5, 14, 12, 1, 0, 0, time.UTC)

	o.recordAgentEvent(issue.ID, agent.Event{
		Type:      agent.EventOtherMessage,
		Timestamp: eventAt,
		SessionID: "session-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		Raw:       json.RawMessage(`{"item":{"content":[{"text":"working through tests"}]}}`),
		Usage:     agent.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		RateLimits: &agent.RateLimitSnapshot{
			LimitID: "codex",
			Primary: &agent.RateLimitWindow{
				UsedPercent: 70,
			},
		},
	})

	o.mu.Lock()
	defer o.mu.Unlock()
	session := o.state.Running[issue.ID].Session
	if session.SessionID != "session-1" || session.ThreadID != "thread-1" || session.TurnID != "turn-1" {
		t.Fatalf("session ids = %+v, want session/thread/turn", session)
	}
	if session.LastEvent != string(agent.EventOtherMessage) || !session.LastTimestamp.Equal(eventAt) {
		t.Fatalf("last event = %q at %v, want other_message at %v", session.LastEvent, session.LastTimestamp, eventAt)
	}
	if session.LastMessage != "working through tests" {
		t.Fatalf("last message = %q, want summarized raw message", session.LastMessage)
	}
	if session.InputTokens != 10 || session.OutputTokens != 5 || session.TotalTokens != 15 {
		t.Fatalf("tokens = %+v, want 10/5/15", session)
	}
	rateLimits, ok := session.RateLimits.(*agent.RateLimitSnapshot)
	if !ok || rateLimits.LimitID != "codex" || rateLimits.Primary == nil || rateLimits.Primary.UsedPercent != 70 {
		t.Fatalf("session rate limits = %+v", session.RateLimits)
	}
}

func TestDispatchSkipsIssuesNeedingHumanReview(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusSucceeded}}
	o := New(nil, validConfig(), &fakeTracker{}, worker, silentLogger())
	o.state.NeedsHuman[issue.ID] = domain.NeedsHumanEntry{IssueID: issue.ID, Identifier: issue.Identifier}

	o.dispatchUpToCapacity(context.Background(), []domain.Issue{issue})

	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.calls) != 0 {
		t.Fatalf("worker calls = %v, want none", worker.calls)
	}
}

func TestRetryAttemptAndSessionArePassedToWorker(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}}
	worker := &recordingWorker{result: WorkerResult{Status: domain.RunStatusSucceeded}}
	o := New(nil, validConfig(), &fakeTracker{candidates: []domain.Issue{issue}}, worker, silentLogger())

	o.handleWorkerResult(WorkerResult{Issue: issue, Status: domain.RunStatusSucceeded, SessionID: "sess-1"})
	o.onRetryFire(issue, 1)
	o.dispatchUpToCapacity(context.Background(), []domain.Issue{issue})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		worker.mu.Lock()
		if len(worker.attempts) > 0 {
			got := worker.attempts[0]
			worker.mu.Unlock()
			if got == nil || *got != 1 {
				t.Fatalf("attempt = %v, want 1", got)
			}
			if worker.resumes[0] != "sess-1" {
				t.Fatalf("resume session = %q, want sess-1", worker.resumes[0])
			}
			return
		}
		worker.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker was not called")
}

func TestReconcile_CancelsTerminalRunAndCleansWorkspace(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}}
	tracker := &fakeTracker{
		candidates: []domain.Issue{issue},
		states:     map[string]string{issue.ID: "dOnE"},
	}
	worker := &recordingWorker{waitForCancel: true}
	o := New(nil, validConfig(), tracker, worker, silentLogger())

	o.dispatchUpToCapacity(context.Background(), []domain.Issue{issue})
	o.reconcile(context.Background())

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		o.drainWorkerResults()
		worker.mu.Lock()
		canceled := worker.canceled
		cleanupCalls := len(worker.cleanupCalls)
		worker.mu.Unlock()
		if canceled && cleanupCalls == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	t.Fatalf("canceled=%v cleanupCalls=%v, want canceled=true cleanupCalls=1", worker.canceled, worker.cleanupCalls)
}

func TestReconcile_TreatsMixedCaseActiveStateAsActive(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}}
	tracker := &fakeTracker{
		candidates: []domain.Issue{issue},
		states:     map[string]string{issue.ID: "rEaDy"},
	}
	worker := &recordingWorker{waitForCancel: true}
	o := New(nil, validConfig(), tracker, worker, silentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o.dispatchUpToCapacity(ctx, []domain.Issue{issue})
	o.reconcile(context.Background())
	time.Sleep(20 * time.Millisecond)

	o.mu.Lock()
	_, running := o.state.Running[issue.ID]
	_, cleanup := o.cleanupAfterRun[issue.ID]
	o.mu.Unlock()
	if !running {
		t.Fatal("mixed-case active state canceled the run")
	}
	if cleanup {
		t.Fatal("mixed-case active state scheduled cleanup")
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.canceled {
		t.Fatal("worker was canceled for mixed-case active state")
	}
}

func TestReconcile_CancelsStalledRunAndSchedulesRetry(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}}
	tracker := &fakeTracker{
		candidates: []domain.Issue{issue},
		states:     map[string]string{issue.ID: "Ready"},
	}
	worker := &recordingWorker{waitForCancel: true}
	cfg := validConfig()
	cfg.Codex.StallTimeoutMs = 10
	o := New(nil, cfg, tracker, worker, silentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o.dispatchUpToCapacity(ctx, []domain.Issue{issue})
	o.mu.Lock()
	entry := o.state.Running[issue.ID]
	entry.Session.SessionID = "session-1"
	entry.Session.ThreadID = "thread-1"
	entry.Session.TurnID = "turn-1"
	entry.Session.LastEvent = string(agent.EventOtherMessage)
	entry.Session.LastTimestamp = time.Now().Add(-time.Second)
	entry.Session.InputTokens = 12
	entry.Session.OutputTokens = 6
	entry.Session.TotalTokens = 18
	entry.Session.RateLimits = &agent.RateLimitSnapshot{LimitID: "live-codex"}
	o.state.Running[issue.ID] = entry
	o.mu.Unlock()

	o.reconcile(context.Background())

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		o.drainWorkerResults()
		o.mu.Lock()
		retry, retrying := o.state.RetryAttempts[issue.ID]
		recentRuns := append([]domain.RunAttempt(nil), o.state.RecentRuns...)
		o.mu.Unlock()
		if retrying && len(recentRuns) == 1 {
			defer o.retryTimers[issue.ID].Stop()
			if retry.Attempt != 1 {
				t.Fatalf("retry attempt = %d, want 1", retry.Attempt)
			}
			if !strings.Contains(retry.Error, "no agent events for") {
				t.Fatalf("retry error = %q, want stall reason", retry.Error)
			}
			if recentRuns[0].Status != domain.RunStatusStalled {
				t.Fatalf("recent status = %q, want stalled", recentRuns[0].Status)
			}
			if recentRuns[0].SessionID != "session-1" || recentRuns[0].ThreadID != "thread-1" || recentRuns[0].TurnID != "turn-1" {
				t.Fatalf("recent session ids = %+v", recentRuns[0])
			}
			if recentRuns[0].TotalTokens != 18 {
				t.Fatalf("recent total tokens = %d, want 18", recentRuns[0].TotalTokens)
			}
			rateLimits, ok := recentRuns[0].RateLimits.(*agent.RateLimitSnapshot)
			if !ok || rateLimits.LimitID != "live-codex" {
				t.Fatalf("recent rate limits = %+v", recentRuns[0].RateLimits)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stalled run did not schedule retry")
}

func TestReconcile_StallDetectionFallsBackToStartedAt(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}}
	tracker := &fakeTracker{
		candidates: []domain.Issue{issue},
		states:     map[string]string{issue.ID: "Ready"},
	}
	worker := &recordingWorker{waitForCancel: true}
	cfg := validConfig()
	cfg.Codex.StallTimeoutMs = 10
	o := New(nil, cfg, tracker, worker, silentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o.dispatchUpToCapacity(ctx, []domain.Issue{issue})
	o.mu.Lock()
	entry := o.state.Running[issue.ID]
	entry.StartedAt = time.Now().Add(-time.Second)
	entry.Session.LastTimestamp = time.Time{}
	o.state.Running[issue.ID] = entry
	o.mu.Unlock()

	o.reconcile(context.Background())

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		_, stalled := o.stalledRunning[issue.ID]
		o.mu.Unlock()
		if stalled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run was not marked stalled using started_at fallback")
}

func TestReconcile_DisabledStallDetectionDoesNotCancel(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "Ready", Labels: []string{"afk"}}
	tracker := &fakeTracker{
		candidates: []domain.Issue{issue},
		states:     map[string]string{issue.ID: "Ready"},
	}
	worker := &recordingWorker{waitForCancel: true}
	cfg := validConfig()
	cfg.Codex.StallTimeoutMs = 0
	o := New(nil, cfg, tracker, worker, silentLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o.dispatchUpToCapacity(ctx, []domain.Issue{issue})
	o.mu.Lock()
	entry := o.state.Running[issue.ID]
	entry.StartedAt = time.Now().Add(-time.Hour)
	entry.Session.LastTimestamp = time.Now().Add(-time.Hour)
	o.state.Running[issue.ID] = entry
	o.mu.Unlock()

	o.reconcile(context.Background())
	time.Sleep(20 * time.Millisecond)

	o.mu.Lock()
	_, stalled := o.stalledRunning[issue.ID]
	o.mu.Unlock()
	worker.mu.Lock()
	canceled := worker.canceled
	worker.mu.Unlock()
	if stalled {
		t.Fatal("run marked stalled with disabled stall detection")
	}
	if canceled {
		t.Fatal("worker canceled with disabled stall detection")
	}
}

func TestCleanupTerminalWorkspaces(t *testing.T) {
	t.Parallel()
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1"}
	tracker := &fakeTracker{terminal: []domain.Issue{issue}}
	worker := &recordingWorker{}
	o := New(nil, validConfig(), tracker, worker, silentLogger())

	o.cleanupTerminalWorkspaces(context.Background())

	worker.mu.Lock()
	defer worker.mu.Unlock()
	if !reflect.DeepEqual(worker.cleanupCalls, []string{"SAM-1"}) {
		t.Fatalf("cleanup calls = %v, want [SAM-1]", worker.cleanupCalls)
	}
}

func TestReloadWorkflowIfChangedUpdatesSchedulerAndWorker(t *testing.T) {
	t.Parallel()
	path := writeWorkflowForReloadTest(t, 1, 1000, "# Old prompt")
	def, err := workflow.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := workflow.NewServiceConfig(def.Config)
	worker := &recordingWorker{}
	o := New(def, cfg, &fakeTracker{}, worker, silentLogger())
	if err := o.EnableWorkflowReload(path); err != nil {
		t.Fatalf("EnableWorkflowReload: %v", err)
	}

	writeWorkflowForReloadTestAtPath(t, path, 3, 2000, "# New prompt")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	o.reloadWorkflowIfChanged()

	if o.Config.Agent.MaxConcurrentAgents != 3 {
		t.Fatalf("orchestrator max_concurrent_agents = %d, want 3", o.Config.Agent.MaxConcurrentAgents)
	}
	if o.Workflow.PromptTemplate != "# New prompt" {
		t.Fatalf("orchestrator prompt = %q, want new prompt", o.Workflow.PromptTemplate)
	}
	if got := o.pollInterval(); got != 2*time.Second {
		t.Fatalf("poll interval = %s, want 2s", got)
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.workflowCfg.Agent.MaxConcurrentAgents != 3 {
		t.Fatalf("worker max_concurrent_agents = %d, want 3", worker.workflowCfg.Agent.MaxConcurrentAgents)
	}
	if worker.workflowPrompt != "# New prompt" {
		t.Fatalf("worker prompt = %q, want new prompt", worker.workflowPrompt)
	}
}

func TestReloadWorkflowIfChangedKeepsLastGoodConfigOnInvalidRunner(t *testing.T) {
	t.Parallel()
	path := writeWorkflowForReloadTest(t, 1, 1000, "# Old prompt")
	def, err := workflow.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := workflow.NewServiceConfig(def.Config)
	worker := &recordingWorker{}
	o := New(def, cfg, &fakeTracker{}, worker, silentLogger())
	if err := o.EnableWorkflowReload(path); err != nil {
		t.Fatalf("EnableWorkflowReload: %v", err)
	}

	writeWorkflowForReloadTestAtPathWithRunner(t, path, "other", 3, 2000, "# New prompt")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	o.reloadWorkflowIfChanged()

	if got := o.Config.AgentRunnerName(); got != "codex" {
		t.Fatalf("orchestrator runner = %q, want codex", got)
	}
	if o.Config.Agent.MaxConcurrentAgents != 1 {
		t.Fatalf("orchestrator max_concurrent_agents = %d, want original 1", o.Config.Agent.MaxConcurrentAgents)
	}
	if o.Workflow.PromptTemplate != "# Old prompt" {
		t.Fatalf("orchestrator prompt = %q, want old prompt", o.Workflow.PromptTemplate)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.workflowCfg.Agent.MaxConcurrentAgents != 0 {
		t.Fatalf("worker should not have been updated: %+v", worker.workflowCfg)
	}
}

func TestStatusSnapshotIncludesRunningAndRetryingWork(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	issue := domain.Issue{ID: "uuid-1", Identifier: "SAM-1", State: "In Progress"}
	retryIssue := domain.Issue{ID: "uuid-2", Identifier: "SAM-2"}
	o := New(nil, validConfig(), &fakeTracker{}, &recordingWorker{}, silentLogger())
	attempt := 2
	o.state.Running[issue.ID] = domain.RunningEntry{
		Issue:     issue,
		StartedAt: now.Add(-5 * time.Second),
		Attempt:   &attempt,
		Session: domain.LiveSession{
			SessionID:     "session-1",
			ThreadID:      "thread-1",
			TurnID:        "turn-1",
			LastEvent:     string(agent.EventOtherMessage),
			LastTimestamp: now.Add(-time.Second),
			LastMessage:   "checking status",
			InputTokens:   8,
			OutputTokens:  3,
			TotalTokens:   11,
			RateLimits:    &agent.RateLimitSnapshot{LimitID: "live-codex"},
		},
	}
	o.state.Claimed[issue.ID] = struct{}{}
	o.state.Claimed[retryIssue.ID] = struct{}{}
	o.state.CodexTotals = domain.CodexTotals{
		InputTokens:    10,
		OutputTokens:   5,
		TotalTokens:    15,
		SecondsRunning: 7,
	}
	o.state.CodexRateLimits = &agent.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &agent.RateLimitWindow{
			UsedPercent: 65,
		},
	}
	o.state.RecentRuns = []domain.RunAttempt{
		{
			IssueID:         "uuid-3",
			IssueIdentifier: "SAM-3",
			SessionID:       "thread-3",
			ThreadID:        "thread-3",
			TurnID:          "turn-3",
			StartedAt:       now.Add(-3 * time.Second),
			FinishedAt:      now.Add(-1 * time.Second),
			InputTokens:     4,
			OutputTokens:    2,
			TotalTokens:     6,
			RateLimits:      &agent.RateLimitSnapshot{LimitID: "run-codex"},
			Status:          domain.RunStatusSucceeded,
		},
	}
	o.mu.Lock()
	o.scheduleRetryLocked(retryIssue, 1, "still active after success", time.Minute)
	o.mu.Unlock()
	defer o.retryTimers[retryIssue.ID].Stop()

	snapshot := o.StatusSnapshot(now)

	if snapshot.PollIntervalMs != validConfig().Polling.IntervalMs {
		t.Fatalf("poll interval = %d", snapshot.PollIntervalMs)
	}
	if snapshot.AgentRunner != "codex" || snapshot.LinearAccess != "host_proxy" {
		t.Fatalf("runner/access = %q/%q, want codex/host_proxy", snapshot.AgentRunner, snapshot.LinearAccess)
	}
	if len(snapshot.Running) != 1 || snapshot.Running[0].Identifier != "SAM-1" {
		t.Fatalf("running = %+v, want SAM-1", snapshot.Running)
	}
	if snapshot.Running[0].DurationMs != 5000 {
		t.Fatalf("duration_ms = %d, want 5000", snapshot.Running[0].DurationMs)
	}
	if snapshot.Running[0].SessionID != "session-1" || snapshot.Running[0].ThreadID != "thread-1" || snapshot.Running[0].TurnID != "turn-1" {
		t.Fatalf("running session ids = %+v", snapshot.Running[0])
	}
	if snapshot.Running[0].LastEvent != string(agent.EventOtherMessage) || snapshot.Running[0].LastTimestamp == nil || !snapshot.Running[0].LastTimestamp.Equal(now.Add(-time.Second)) {
		t.Fatalf("running last event = %+v", snapshot.Running[0])
	}
	if snapshot.Running[0].LastMessage != "checking status" {
		t.Fatalf("running last message = %q, want checking status", snapshot.Running[0].LastMessage)
	}
	if snapshot.Running[0].TokenInfo.TotalTokens != 11 {
		t.Fatalf("running token_info = %+v, want total 11", snapshot.Running[0].TokenInfo)
	}
	liveRateLimits, ok := snapshot.Running[0].RateLimits.(*agent.RateLimitSnapshot)
	if !ok || liveRateLimits.LimitID != "live-codex" {
		t.Fatalf("running rate_limits = %+v", snapshot.Running[0].RateLimits)
	}
	if len(snapshot.Retrying) != 1 || snapshot.Retrying[0].Identifier != "SAM-2" {
		t.Fatalf("retrying = %+v, want SAM-2", snapshot.Retrying)
	}
	if len(snapshot.NeedsHuman) != 0 {
		t.Fatalf("needs_human = %+v, want none", snapshot.NeedsHuman)
	}
	if snapshot.ClaimedCount != 2 {
		t.Fatalf("claimed_count = %d, want 2", snapshot.ClaimedCount)
	}
	if snapshot.CodexTotals.TotalTokens != 15 || snapshot.CodexTotals.SecondsRunning != 7 {
		t.Fatalf("codex_totals = %+v", snapshot.CodexTotals)
	}
	if len(snapshot.RecentRuns) != 1 || snapshot.RecentRuns[0].Identifier != "SAM-3" {
		t.Fatalf("recent_runs = %+v, want SAM-3", snapshot.RecentRuns)
	}
	if snapshot.RecentRuns[0].DurationMs != 2000 {
		t.Fatalf("recent duration_ms = %d, want 2000", snapshot.RecentRuns[0].DurationMs)
	}
	if snapshot.RecentRuns[0].SessionID != "thread-3" {
		t.Fatalf("recent session_id = %q, want thread-3", snapshot.RecentRuns[0].SessionID)
	}
	if snapshot.RecentRuns[0].ThreadID != "thread-3" || snapshot.RecentRuns[0].TurnID != "turn-3" {
		t.Fatalf("recent thread/turn = %q/%q, want thread-3/turn-3", snapshot.RecentRuns[0].ThreadID, snapshot.RecentRuns[0].TurnID)
	}
	if snapshot.RecentRuns[0].TokenInfo.TotalTokens != 6 {
		t.Fatalf("recent token_info = %+v, want total 6", snapshot.RecentRuns[0].TokenInfo)
	}
	recentRateLimits, ok := snapshot.RecentRuns[0].RateLimits.(*agent.RateLimitSnapshot)
	if !ok || recentRateLimits.LimitID != "run-codex" {
		t.Fatalf("recent rate_limits = %+v", snapshot.RecentRuns[0].RateLimits)
	}
	rateLimits, ok := snapshot.CodexRateLimits.(*agent.RateLimitSnapshot)
	if !ok {
		t.Fatalf("codex_rate_limits type = %T", snapshot.CodexRateLimits)
	}
	if rateLimits.LimitID != "codex" || rateLimits.Primary == nil || rateLimits.Primary.UsedPercent != 65 {
		t.Fatalf("codex_rate_limits = %+v", rateLimits)
	}
}

func TestStatusSnapshotIncludesNeedsHuman(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	o := New(nil, validConfig(), &fakeTracker{}, &recordingWorker{}, silentLogger())
	o.state.NeedsHuman["uuid-1"] = domain.NeedsHumanEntry{
		IssueID:    "uuid-1",
		Identifier: "SAM-1",
		Reason:     "successful_continuation_loop",
		Attempts:   3,
		Since:      now.Add(-time.Minute),
	}

	snapshot := o.StatusSnapshot(now)

	if len(snapshot.NeedsHuman) != 1 {
		t.Fatalf("needs_human = %+v, want one entry", snapshot.NeedsHuman)
	}
	entry := snapshot.NeedsHuman[0]
	if entry.Identifier != "SAM-1" || entry.Attempts != 3 || entry.Reason != "successful_continuation_loop" {
		t.Fatalf("needs_human[0] = %+v", entry)
	}
}

func TestWriteJSONAtomicCreatesStatusFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "status", "orchestrator.json")
	if err := writeJSONAtomic(context.Background(), path, map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("writeJSONAtomic: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"status": "ok"`) {
		t.Fatalf("status file contents = %s", data)
	}
}

func validConfig() workflow.ServiceConfig {
	return workflow.ServiceConfig{
		Tracker: workflow.TrackerConfig{
			Kind:           "linear",
			Endpoint:       "https://api.linear.app/graphql",
			APIKeyLiteral:  "lin_test",
			ProjectSlug:    "spur",
			ActiveStates:   []string{"Ready", "In Progress"},
			TerminalStates: []string{"Done", "Canceled", "Duplicate"},
		},
		Polling: workflow.PollingConfig{IntervalMs: 30000},
		Agent: workflow.AgentConfig{
			MaxConcurrentAgents:    2,
			MaxTurns:               20,
			MaxRetryBackoffMs:      300000,
			MaxUnproductiveSuccess: 3,
		},
		Credentials: workflow.CredentialsConfig{LinearAccess: "host_proxy"},
		Codex: workflow.CodexConfig{
			Command:        "codex app-server",
			TurnTimeoutMs:  3600000,
			ReadTimeoutMs:  5000,
			StallTimeoutMs: 300000,
		},
	}
}

func writeWorkflowForReloadTest(t *testing.T, maxConcurrent, pollIntervalMs int, prompt string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	writeWorkflowForReloadTestAtPath(t, path, maxConcurrent, pollIntervalMs, prompt)
	return path
}

func writeWorkflowForReloadTestAtPath(t *testing.T, path string, maxConcurrent, pollIntervalMs int, prompt string) {
	t.Helper()
	writeWorkflowForReloadTestAtPathWithRunner(t, path, "", maxConcurrent, pollIntervalMs, prompt)
}

func writeWorkflowForReloadTestAtPathWithRunner(t *testing.T, path string, runner string, maxConcurrent, pollIntervalMs int, prompt string) {
	t.Helper()
	runnerLine := ""
	if runner != "" {
		runnerLine = "  runner: " + runner + "\n"
	}
	data := []byte(`---
tracker:
  kind: linear
  api_key: lin_test
  project_slug: spur
polling:
  interval_ms: ` + strconv.Itoa(pollIntervalMs) + `
agent:
  max_concurrent_agents: ` + strconv.Itoa(maxConcurrent) + `
  max_unproductive_successes: 3
` + runnerLine + `
codex:
  command: codex app-server
---

` + prompt + `
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.NewFile(0, "/dev/null"), &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// Avoid unused imports in the test compile when refactoring.
var _ = linear.SpurDefault
