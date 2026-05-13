package orchestrator

import (
	"context"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

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

// recordingWorker captures every issue it was asked to run, then reports
// the canned result.
type recordingWorker struct {
	mu            sync.Mutex
	calls         []string
	attempts      []*int
	resumes       []string
	cleanupCalls  []string
	canceled      bool
	waitForCancel bool
	result        WorkerResult
	delay         time.Duration
}

func (w *recordingWorker) Run(ctx context.Context, issue domain.Issue, attempt *int, resumeSessionID string) WorkerResult {
	w.mu.Lock()
	w.calls = append(w.calls, issue.Identifier)
	w.attempts = append(w.attempts, attempt)
	w.resumes = append(w.resumes, resumeSessionID)
	w.mu.Unlock()
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
		states:     map[string]string{issue.ID: "Done"},
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

func validConfig() workflow.ServiceConfig {
	return workflow.ServiceConfig{
		Tracker: workflow.TrackerConfig{
			Kind:           "linear",
			Endpoint:       "https://api.linear.app/graphql",
			ProjectSlug:    "spur",
			ActiveStates:   []string{"Ready", "In Progress"},
			TerminalStates: []string{"Done", "Canceled", "Duplicate"},
		},
		Polling: workflow.PollingConfig{IntervalMs: 30000},
		Agent: workflow.AgentConfig{
			MaxConcurrentAgents: 2,
			MaxTurns:            20,
			MaxRetryBackoffMs:   300000,
		},
		ClaudeCode: workflow.ClaudeCodeConfig{
			Command: "claude --print",
		},
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.NewFile(0, "/dev/null"), &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// Avoid unused imports in the test compile when refactoring.
var _ = linear.SpurDefault
