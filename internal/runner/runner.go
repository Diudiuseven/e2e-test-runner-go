// Package runner orchestrates scenario discovery, selection, deterministic
// ordering, bounded-concurrency execution, retry/flaky aggregation and the
// collection of a stable result set.
//
// Results are always emitted in discovery order, independent of the
// configured concurrency, shuffling or completion order. When the run is
// interrupted (deadline or cancel), scenarios already finished keep their
// outcome, in-flight scenarios are reported as canceled/timed out, and
// scenarios that never started are reported as not_executed.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/executor"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/filter"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/shuffle"
)

// Config controls a whole run.
type Config struct {
	Root        string
	Concurrency int
	Deadline    time.Duration
	Seed        uint64
	Shuffle     bool
	Selector    filter.Selector
}

// ScenarioResult is the aggregated outcome for one scenario.
type ScenarioResult struct {
	Name     string             `json:"name"`
	Dir      string             `json:"dir"`
	Tags     []string           `json:"tags"`
	Status   scenario.Status    `json:"status"`
	Reason   string             `json:"reason,omitempty"`
	Duration time.Duration      `json:"-"`
	Retries  int                `json:"retries"`
	Attempts []scenario.Attempt `json:"attempts"`
	Order    int                `json:"-"`
}

// MarshalJSON renders Duration in milliseconds.
func (r ScenarioResult) MarshalJSON() ([]byte, error) {
	type alias ScenarioResult
	return json.Marshal(struct {
		alias
		DurationMS int64 `json:"duration_ms"`
	}{alias: alias(r), DurationMS: r.Duration.Milliseconds()})
}

// RunResult is the full, deterministically ordered report payload.
type RunResult struct {
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Deadline  bool              `json:"deadline_exceeded"`
	Canceled  bool              `json:"canceled"`
	Results   []*ScenarioResult `json:"results"`
}

// Runner orchestrates a run.
type Runner struct {
	cfg Config
	exe *executor.Executor
}

// New builds a Runner with default dependencies.
func New(cfg Config) *Runner {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	return &Runner{cfg: cfg, exe: executor.New()}
}

// Run executes the configured suite and returns the stable result set.
func (r *Runner) Run(ctx context.Context) (*RunResult, error) {
	started := time.Now()
	if r.cfg.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Deadline)
		defer cancel()
	}

	scenarios, err := scenario.Discover(ctx, r.cfg.Root)
	if err != nil {
		return nil, err
	}
	decisions := filter.Apply(ctx, scenarios, r.cfg.Selector)

	orderByName := make(map[string]int, len(decisions))
	results := make([]*ScenarioResult, len(decisions))
	var included []*scenario.Scenario
	for i, d := range decisions {
		orderByName[d.Scenario.Name] = i
		tags := d.Scenario.Tags
		if tags == nil {
			tags = []string{}
		}
		sr := &ScenarioResult{
			Name:     d.Scenario.Name,
			Dir:      d.Scenario.Dir,
			Tags:     tags,
			Status:   scenario.StatusNotExecuted,
			Order:    i,
			Attempts: []scenario.Attempt{},
		}
		switch d.Disposition {
		case scenario.DispExcluded:
			sr.Status, sr.Reason = scenario.StatusExcluded, d.Reason
		case scenario.DispSkipped:
			sr.Status, sr.Reason = scenario.StatusSkipped, d.Reason
		case scenario.DispUnmatched:
			sr.Status, sr.Reason = scenario.StatusUnmatched, d.Reason
		default:
			included = append(included, d.Scenario)
		}
		results[i] = sr
	}

	// Deterministic permutation of the included set only; the set is intact.
	toRun := included
	if r.cfg.Shuffle {
		toRun = shuffle.Order(toRun, r.cfg.Seed)
	}

	r.execute(ctx, toRun, results, orderByName)

	res := &RunResult{
		StartedAt: started,
		EndedAt:   time.Now(),
		Results:   results,
	}
	switch ctx.Err() {
	case context.DeadlineExceeded:
		res.Deadline = true
	case context.Canceled:
		res.Canceled = true
	}
	return res, nil
}

// execute runs scenarios through a fixed worker pool and fills result slots.
func (r *Runner) execute(
	ctx context.Context,
	toRun []*scenario.Scenario,
	results []*ScenarioResult,
	orderByName map[string]int,
) {
	jobs := make(chan *scenario.Scenario)
	var wg sync.WaitGroup
	for w := 0; w < r.cfg.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				results[orderByName[s.Name]] = r.runScenario(ctx, s, orderByName[s.Name])
			}
		}()
	}
	for _, s := range toRun {
		select {
		case jobs <- s:
		case <-ctx.Done():
			// Handed to no worker (unbuffered channel): mark never-started.
			slot := results[orderByName[s.Name]]
			slot.Status = scenario.StatusNotExecuted
			slot.Reason = notStartedReason(ctx)
		}
	}
	close(jobs)
	wg.Wait()
}

// runScenario runs the initial attempt plus any allowed retries.
func (r *Runner) runScenario(parent context.Context, s *scenario.Scenario, order int) *ScenarioResult {
	tags := s.Tags
	if tags == nil {
		tags = []string{}
	}
	sr := &ScenarioResult{
		Name:     s.Name,
		Dir:      s.Dir,
		Tags:     tags,
		Order:    order,
		Status:   scenario.StatusNotExecuted,
		Attempts: []scenario.Attempt{},
	}
	scStart := time.Now()
	defer func() { sr.Duration = time.Since(scStart) }()

	maxAttempts := 1 + s.Retries
	retriesSuppressed := !s.Idempotent && s.Retries > 0
	if retriesSuppressed {
		// A scenario with side effects must never be repeated; configured
		// retries are deliberately suppressed and explained in the result.
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := parent.Err(); err != nil {
			if len(sr.Attempts) == 0 {
				sr.Status = scenario.StatusNotExecuted
				sr.Reason = notStartedReason(parent)
			} else if sr.Status != scenario.StatusFlaky {
				sr.Status = scenario.StatusCanceled
				sr.Reason = interruptedReason(parent)
			}
			return sr
		}

		eres, err := r.exe.Execute(parent, s, attempt)
		if err != nil {
			eres = &executor.Result{
				Status:   scenario.StatusCrashed,
				Reason:   "executor error: " + err.Error(),
				Duration: time.Since(scStart),
			}
		}
		sr.Attempts = append(sr.Attempts, scenario.Attempt{
			Index:      attempt,
			Status:     eres.Status,
			ExitCode:   eres.ExitCode,
			Duration:   eres.Duration,
			Reason:     eres.Reason,
			Signal:     eres.Signal,
			OutputTail: eres.OutputTail,
			StartedAt:  eres.StartedAt,
		})

		switch eres.Status {
		case scenario.StatusPassed:
			if attempt == 0 {
				sr.Status = scenario.StatusPassed
			} else {
				sr.Status = scenario.StatusFlaky
				sr.Retries = attempt
				sr.Reason = fmt.Sprintf("passed on attempt %d after %d failed attempt(s)", attempt+1, attempt)
			}
			return sr
		case scenario.StatusCanceled, scenario.StatusTimeout:
			// Interruptions are final: never retry a killed attempt.
			sr.Status = eres.Status
			sr.Reason = eres.Reason
			sr.Retries = attempt
			return sr
		default:
			// failed / crashed: retry while attempts remain.
			sr.Status = eres.Status
			sr.Reason = eres.Reason
			if attempt+1 < maxAttempts {
				continue
			}
			sr.Retries = attempt
			if retriesSuppressed {
				sr.Reason = eres.Reason + "; retries suppressed for non-idempotent scenario"
			}
			return sr
		}
	}
	return sr
}

func notStartedReason(ctx context.Context) string {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return "not started: overall deadline exceeded"
	case context.Canceled:
		return "not started: run canceled"
	default:
		return "not started"
	}
}

func interruptedReason(ctx context.Context) string {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return "interrupted: overall deadline exceeded"
	case context.Canceled:
		return "interrupted: run canceled"
	default:
		return "interrupted"
	}
}
