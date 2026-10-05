package runner

import (
	"context"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// defaultTimeout is used when neither the scenario nor the config sets one.
const defaultTimeout = 30 * time.Second

// Run discovers, filters and executes scenarios according to cfg.
// The returned slice is always sorted by scenario name, independent of
// concurrency, shuffle order or completion order, so repeated runs over the
// same scenario set produce comparable results. Partial results are
// returned together with ctx.Err() when the run is interrupted.
func Run(ctx context.Context, cfg Config) ([]Result, error) {
	if cfg.Parallel < 1 {
		cfg.Parallel = 1
	}
	if cfg.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Deadline)
		defer cancel()
	}

	scenarios, err := Discover(cfg.Root)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(scenarios))
	var runnable []Scenario
	for _, s := range scenarios {
		if st := classify(s, cfg); st != "" {
			results = append(results, Result{Name: s.Name, Status: st, Reason: filterReason(st)})
			continue
		}
		runnable = append(runnable, s)
	}

	if cfg.Shuffle {
		r := rand.New(rand.NewSource(cfg.Seed))
		r.Shuffle(len(runnable), func(i, j int) { runnable[i], runnable[j] = runnable[j], runnable[i] })
	}

	execResults := runAll(ctx, runnable, cfg)
	results = append(results, execResults...)

	// Deterministic output order: by scenario name.
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, ctx.Err()
}

// filterReason explains why a scenario did not run.
func filterReason(st Status) string {
	switch st {
	case StatusSkipped:
		return "scenario opted out via its manifest (skip: true)"
	case StatusExcluded:
		return "matched the exclude filter"
	case StatusUnmatched:
		return "did not match the include filter"
	default:
		return ""
	}
}

// runAll executes scenarios with a bounded worker pool. Scenarios that never
// start because the run is interrupted are reported as cancelled, so the
// result set always covers the whole discovered set.
func runAll(ctx context.Context, scenarios []Scenario, cfg Config) []Result {
	type item struct {
		idx int
		res Result
	}
	jobs := make(chan int)
	out := make(chan item, len(scenarios))

	var wg sync.WaitGroup
	for w := 0; w < cfg.Parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out <- item{idx: i, res: runScenario(ctx, scenarios[i], cfg)}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := range scenarios {
			select {
			case <-ctx.Done():
				return // stop feeding; remaining scenarios get cancelled below
			case jobs <- i:
			}
		}
	}()

	go func() { wg.Wait(); close(out) }()

	results := make([]Result, len(scenarios))
	started := make([]bool, len(scenarios))
	for it := range out {
		results[it.idx] = it.res
		started[it.idx] = true
	}
	for i, s := range scenarios {
		if !started[i] {
			results[i] = Result{
				Name:   s.Name,
				Status: StatusCancelled,
				Reason: "run interrupted before this scenario started",
			}
		}
	}
	return results
}

// runScenario executes one scenario with retries and folds the attempts into
// a single Result. Retries never produce extra Result entries.
func runScenario(ctx context.Context, s Scenario, cfg Config) Result {
	start := time.Now()
	res := Result{Name: s.Name}

	maxAttempts := retriesFor(s, cfg) + 1
	timeout := timeoutFor(s, cfg)

	// One state dir per scenario, shared by its attempts, removed afterwards.
	stateDir, err := os.MkdirTemp("", "e2e-state-*")
	if err != nil {
		res.Status = StatusFailed
		res.Reason = "prepare state dir: " + err.Error()
		res.Duration = time.Since(start)
		return res
	}
	defer os.RemoveAll(stateDir)

	for i := 0; i < maxAttempts; i++ {
		if ctx.Err() != nil {
			res.Status = StatusCancelled
			res.Reason = "run interrupted before attempt " + strconv.Itoa(i+1)
			break
		}
		att := execute(ctx, s, i, timeout, stateDir)
		res.Attempts = append(res.Attempts, att)

		switch att.Status {
		case StatusPassed:
			if i == 0 {
				res.Status = StatusPassed
			} else {
				res.Status = StatusFlaky
				res.Reason = "passed after retry; earlier attempts failed"
			}
		case StatusCancelled:
			res.Status = StatusCancelled
			res.Reason = att.Reason
		case StatusTimeout, StatusFailed:
			if i == maxAttempts-1 {
				res.Status = att.Status
				res.Reason = att.Reason
			}
			// otherwise: retry with a fresh isolated environment
		}
		if res.Status != "" {
			break
		}
	}
	res.Duration = time.Since(start)
	return res
}

// retriesFor resolves the effective retry count (scenario overrides config).
func retriesFor(s Scenario, cfg Config) int {
	if s.Retries >= 0 {
		return s.Retries
	}
	return cfg.Retries
}

// timeoutFor resolves the effective per-scenario timeout.
func timeoutFor(s Scenario, cfg Config) time.Duration {
	if s.Timeout > 0 {
		return s.Timeout.D()
	}
	if cfg.Timeout > 0 {
		return cfg.Timeout
	}
	return defaultTimeout
}
