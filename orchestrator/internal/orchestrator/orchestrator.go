// Package orchestrator implements the central scheduler from Symphony
// spec §7 (state machine) and §8 (poll loop, dispatch, retry,
// reconciliation).
//
// All scheduling-state mutations flow through one authority (the
// Orchestrator's mutex) per spec §7.4. The tick is single-threaded by
// construction — concurrent workers report back via channels and the
// orchestrator integrates their results during the next tick.
package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/domain"
	"github.com/sasilver75/events/orchestrator/internal/tracker/linear"
	"github.com/sasilver75/events/orchestrator/internal/workflow"
)

// Tracker is the subset of linear.Client behavior the orchestrator depends
// on. Defined as an interface so tests can supply fakes.
type Tracker interface {
	FetchCandidateIssues(ctx context.Context, activeStates []string) ([]domain.Issue, error)
	FetchIssueStatesByIDs(ctx context.Context, ids []string) (map[string]string, error)
	FetchIssuesByStates(ctx context.Context, states []string) ([]domain.Issue, error)
}

// Worker runs one ticket end-to-end. The orchestrator hands ownership of
// the issue to the worker, gets a result back, and decides what to do
// next based on the result.
type Worker interface {
	Run(ctx context.Context, issue domain.Issue, attempt *int) WorkerResult
}

// WorkerResult is what a Worker returns. Maps to Symphony spec §7.2.
type WorkerResult struct {
	Issue     domain.Issue
	Status    domain.RunStatus
	Error     string
	SessionID string // for continuation turns
	Tokens    int
}

// Orchestrator is the central scheduler.
type Orchestrator struct {
	Workflow *workflow.Definition
	Config   workflow.ServiceConfig

	Tracker Tracker
	Worker  Worker

	Eligibility linear.EligibilityFilter

	Logger *slog.Logger

	mu    sync.Mutex
	state domain.OrchestratorRuntimeState

	// retryTimers holds the *time.Timer for each scheduled retry so we
	// can cancel one before re-scheduling. Symphony spec §8.4.
	retryTimers map[string]*time.Timer

	// workerDone receives results from in-flight workers. The tick loop
	// drains this channel during reconciliation.
	workerDone chan WorkerResult
}

// New constructs an Orchestrator with empty runtime state.
func New(def *workflow.Definition, cfg workflow.ServiceConfig, t Tracker, w Worker, logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		Workflow:    def,
		Config:      cfg,
		Tracker:     t,
		Worker:      w,
		Eligibility: linear.SpurDefault,
		Logger:      logger,
		state: domain.OrchestratorRuntimeState{
			PollIntervalMs:      cfg.Polling.IntervalMs,
			MaxConcurrentAgents: cfg.Agent.MaxConcurrentAgents,
			Running:             map[string]domain.RunningEntry{},
			Claimed:             map[string]struct{}{},
			RetryAttempts:       map[string]domain.RetryEntry{},
			Completed:           map[string]struct{}{},
		},
		retryTimers: map[string]*time.Timer{},
		workerDone:  make(chan WorkerResult, 16),
	}
}

// RunDaemon runs the poll loop until ctx is canceled. Symphony spec §8.1.
//
// On each tick:
//  1. Drain finished workers from workerDone, update state.
//  2. Reconcile running issues (spec §8.5).
//  3. Preflight-validate config.
//  4. Fetch candidates, apply eligibility filter, sort.
//  5. Dispatch up to MaxConcurrentAgents.
func (o *Orchestrator) RunDaemon(ctx context.Context) error {
	if err := o.Config.Validate(); err != nil {
		return err
	}
	interval := time.Duration(o.Config.Polling.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 30 * time.Second
	}

	o.Logger.Info("orchestrator starting",
		"poll_interval_ms", o.Config.Polling.IntervalMs,
		"max_concurrent_agents", o.Config.Agent.MaxConcurrentAgents,
		"active_states", o.Config.Tracker.ActiveStates,
	)

	// Initial tick immediately, then on interval.
	if err := o.tick(ctx); err != nil {
		o.Logger.Error("initial tick failed", "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			o.Logger.Info("orchestrator stopping", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			if err := o.tick(ctx); err != nil {
				o.Logger.Error("tick failed", "err", err)
			}
		}
	}
}

// RunOnce dispatches a single specified issue (or one tick if issueIdentifier
// is ""), waits for it to terminate, and exits. Powers `scripts/spur-agent`.
func (o *Orchestrator) RunOnce(ctx context.Context, issueIdentifier string) error {
	if err := o.Config.Validate(); err != nil {
		return err
	}

	candidates, err := o.Tracker.FetchCandidateIssues(ctx, o.Config.Tracker.ActiveStates)
	if err != nil {
		return err
	}
	eligible, rejected := o.Eligibility.Apply(candidates)
	for _, r := range rejected {
		o.Logger.Debug("rejected by eligibility", "issue", r.Issue.Identifier, "reason", r.Reason)
	}

	var target *domain.Issue
	if issueIdentifier == "" {
		if len(eligible) == 0 {
			o.Logger.Info("no eligible issues; exiting")
			return nil
		}
		sortDispatch(eligible)
		target = &eligible[0]
	} else {
		for i := range candidates {
			if candidates[i].Identifier == issueIdentifier {
				target = &candidates[i]
				break
			}
		}
		if target == nil {
			return errors.New("issue not found among active candidates: " + issueIdentifier)
		}
	}

	o.Logger.Info("dispatching one-shot", "issue", target.Identifier)
	result := o.Worker.Run(ctx, *target, nil)
	o.Logger.Info("one-shot complete",
		"issue", target.Identifier,
		"status", string(result.Status),
		"err", result.Error,
	)
	if result.Status == domain.RunStatusFailed ||
		result.Status == domain.RunStatusTimedOut ||
		result.Status == domain.RunStatusStalled {
		return errors.New(string(result.Status) + ": " + result.Error)
	}
	return nil
}

// tick is one poll iteration. Spec §8.1.
func (o *Orchestrator) tick(ctx context.Context) error {
	o.drainWorkerResults()
	o.reconcile(ctx)

	if err := o.Config.Validate(); err != nil {
		o.Logger.Error("preflight validation failed; skipping dispatch", "err", err)
		return err
	}

	candidates, err := o.Tracker.FetchCandidateIssues(ctx, o.Config.Tracker.ActiveStates)
	if err != nil {
		o.Logger.Error("candidate fetch failed; skipping dispatch", "err", err)
		return nil // per spec §8.1, candidate fetch failure doesn't propagate
	}

	eligible, rejected := o.Eligibility.Apply(candidates)
	for _, r := range rejected {
		o.Logger.Debug("rejected by eligibility", "issue", r.Issue.Identifier, "reason", r.Reason)
	}

	sortDispatch(eligible)
	o.dispatchUpToCapacity(ctx, eligible)
	return nil
}

// drainWorkerResults pulls all completed worker results without blocking.
func (o *Orchestrator) drainWorkerResults() {
	for {
		select {
		case res := <-o.workerDone:
			o.handleWorkerResult(res)
		default:
			return
		}
	}
}

// handleWorkerResult updates running state and (re)schedules retries
// per spec §7.3 transition triggers (Worker Exit normal / abnormal).
func (o *Orchestrator) handleWorkerResult(res WorkerResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.state.Running, res.Issue.ID)

	switch res.Status {
	case domain.RunStatusSucceeded:
		// Spec §7.1: schedule a short continuation retry to re-check
		// whether the issue is still active.
		o.scheduleRetryLocked(res.Issue, 1, "", 1*time.Second)
	case domain.RunStatusFailed, domain.RunStatusTimedOut, domain.RunStatusStalled:
		attempt := 1
		if existing, ok := o.state.RetryAttempts[res.Issue.ID]; ok {
			attempt = existing.Attempt + 1
		}
		backoff := backoffDelay(attempt, o.Config.Agent.MaxRetryBackoffMs)
		o.scheduleRetryLocked(res.Issue, attempt, res.Error, backoff)
	default:
		// Canceled / reconciliation-killed: just release the claim.
		delete(o.state.Claimed, res.Issue.ID)
	}
}

// scheduleRetryLocked schedules (or reschedules) a retry. Caller must
// hold o.mu.
func (o *Orchestrator) scheduleRetryLocked(issue domain.Issue, attempt int, errStr string, delay time.Duration) {
	if t, ok := o.retryTimers[issue.ID]; ok {
		t.Stop()
	}
	entry := domain.RetryEntry{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Attempt:    attempt,
		DueAtMs:    time.Now().Add(delay).UnixMilli(),
		Error:      errStr,
	}
	entry.TimerHandle = time.AfterFunc(delay, func() {
		o.onRetryFire(issue, attempt)
	})
	o.state.RetryAttempts[issue.ID] = entry
	o.retryTimers[issue.ID] = entry.TimerHandle.(*time.Timer)
}

// onRetryFire runs when a retry timer fires. The orchestrator's next tick
// will pick the issue up if it's still candidate-eligible.
func (o *Orchestrator) onRetryFire(issue domain.Issue, attempt int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.state.RetryAttempts, issue.ID)
	delete(o.retryTimers, issue.ID)
	delete(o.state.Claimed, issue.ID)
	o.Logger.Info("retry timer fired; releasing claim for re-dispatch",
		"issue", issue.Identifier, "attempt", attempt)
}

// reconcile is spec §8.5 (active run reconciliation).
//
// Part A: stall detection (TODO when we add per-event timestamps to RunningEntry)
// Part B: refresh tracker state for all running issues. If a state becomes
//
//	terminal, signal the worker to stop and clean up.
func (o *Orchestrator) reconcile(ctx context.Context) {
	o.mu.Lock()
	if len(o.state.Running) == 0 {
		o.mu.Unlock()
		return
	}
	ids := make([]string, 0, len(o.state.Running))
	for id := range o.state.Running {
		ids = append(ids, id)
	}
	o.mu.Unlock()

	states, err := o.Tracker.FetchIssueStatesByIDs(ctx, ids)
	if err != nil {
		o.Logger.Warn("reconcile state refresh failed", "err", err)
		return
	}

	terminalSet := stringSet(o.Config.Tracker.TerminalStates)
	activeSet := stringSet(o.Config.Tracker.ActiveStates)

	o.mu.Lock()
	defer o.mu.Unlock()
	for id, state := range states {
		if _, terminal := terminalSet[state]; terminal {
			o.Logger.Info("issue moved to terminal state mid-run; signaling cleanup",
				"issue_id", id, "new_state", state)
			// Worker context cancellation is owned by the worker's
			// run context; orchestrator can't reach into the worker.
			// In v0 we just log; full cancel comes when we add a
			// context registry in the orchestrator. TODO(task#16).
		} else if _, active := activeSet[state]; !active {
			o.Logger.Info("issue in neither active nor terminal state",
				"issue_id", id, "state", state)
		}
	}
}

// dispatchUpToCapacity claims and runs eligible issues until either the
// queue is empty or we hit MaxConcurrentAgents. Spec §8.3.
func (o *Orchestrator) dispatchUpToCapacity(ctx context.Context, sorted []domain.Issue) {
	for _, issue := range sorted {
		o.mu.Lock()
		availableSlots := o.Config.Agent.MaxConcurrentAgents - len(o.state.Running)
		if availableSlots <= 0 {
			o.mu.Unlock()
			return
		}
		if _, running := o.state.Running[issue.ID]; running {
			o.mu.Unlock()
			continue
		}
		if _, claimed := o.state.Claimed[issue.ID]; claimed {
			o.mu.Unlock()
			continue
		}
		// Claim.
		o.state.Claimed[issue.ID] = struct{}{}
		o.state.Running[issue.ID] = domain.RunningEntry{
			Issue:     issue,
			StartedAt: time.Now(),
		}
		o.mu.Unlock()

		go o.runWorker(ctx, issue)
	}
}

// runWorker invokes the Worker and pushes its result into workerDone.
// The orchestrator integrates the result in the next tick.
func (o *Orchestrator) runWorker(ctx context.Context, issue domain.Issue) {
	defer func() {
		// Recover from worker panics so one bad worker doesn't take
		// down the orchestrator.
		if r := recover(); r != nil {
			o.workerDone <- WorkerResult{
				Issue:  issue,
				Status: domain.RunStatusFailed,
				Error:  "worker panic: " + toString(r),
			}
		}
	}()
	res := o.Worker.Run(ctx, issue, nil)
	o.workerDone <- res
}

// sortDispatch implements spec §8.2 sort order:
//  1. priority asc (1..4 preferred; null/unknown sorts last)
//  2. created_at oldest first
//  3. identifier lexicographic tiebreak
func sortDispatch(issues []domain.Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		pa := priorityOrLast(issues[i].Priority)
		pb := priorityOrLast(issues[j].Priority)
		if pa != pb {
			return pa < pb
		}
		if !issues[i].CreatedAt.Equal(issues[j].CreatedAt) {
			return issues[i].CreatedAt.Before(issues[j].CreatedAt)
		}
		return issues[i].Identifier < issues[j].Identifier
	})
}

// priorityOrLast returns the int value of priority, or a very large number
// (so null/unknown sorts last) when nil. Linear priorities are 1..4 with
// 1 = urgent.
func priorityOrLast(p *int) int {
	if p == nil {
		return 1 << 30
	}
	return *p
}

// backoffDelay implements spec §8.4 exponential backoff.
//
//	delay = min(10000 * 2^(attempt-1), maxBackoffMs)
func backoffDelay(attempt, maxBackoffMs int) time.Duration {
	if maxBackoffMs <= 0 {
		maxBackoffMs = 300000
	}
	base := 10000 * (1 << (attempt - 1))
	if base > maxBackoffMs || base < 0 { // overflow guard
		base = maxBackoffMs
	}
	return time.Duration(base) * time.Millisecond
}

func stringSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, s := range items {
		m[s] = struct{}{}
	}
	return m
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return ""
	}
}
