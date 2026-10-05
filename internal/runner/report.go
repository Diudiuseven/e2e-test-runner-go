package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Exit codes used by the CLI.
const (
	ExitOK          = 0 // no failed / timeout / cancelled scenario
	ExitFailure     = 1 // at least one scenario failed or timed out
	ExitInterrupted = 2 // the run was interrupted (deadline or signal)
	ExitUsage       = 3 // bad flags or unreadable configuration
)

// ExitCode maps a result set to a process exit code. interrupted should be
// true when the run context was cancelled (deadline exceeded or signal).
func ExitCode(results []Result, interrupted bool) int {
	if interrupted {
		return ExitInterrupted
	}
	for _, r := range results {
		switch r.Status {
		case StatusFailed, StatusTimeout, StatusCancelled:
			return ExitFailure
		}
	}
	return ExitOK
}

// Summary aggregates per-status counts.
type Summary struct {
	Total     int            `json:"total"`
	ByStatus  map[Status]int `json:"by_status"`
	Duration  time.Duration  `json:"duration_ns"`
	Seed      int64          `json:"seed"`
	Parallel  int            `json:"parallel"`
	Interrupt bool           `json:"interrupted"`
}

// Report is the machine-readable output of a whole run.
type Report struct {
	Summary Summary  `json:"summary"`
	Results []Result `json:"results"`
}

// BuildReport assembles a Report from results and run metadata.
func BuildReport(results []Result, cfg Config, elapsed time.Duration, interrupted bool) Report {
	s := Summary{
		Total:     len(results),
		ByStatus:  map[Status]int{},
		Duration:  elapsed,
		Seed:      cfg.Seed,
		Parallel:  cfg.Parallel,
		Interrupt: interrupted,
	}
	for _, r := range results {
		s.ByStatus[r.Status]++
	}
	return Report{Summary: s, Results: results}
}

// WriteReport serializes the report as indented JSON.
func WriteReport(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// PrintHuman writes a short human-readable summary.
func PrintHuman(w io.Writer, rep Report) {
	fmt.Fprintf(w, "\n%d scenarios in %s (seed=%d, parallel=%d)\n",
		rep.Summary.Total, rep.Summary.Duration.Round(time.Millisecond),
		rep.Summary.Seed, rep.Summary.Parallel)
	for _, st := range []Status{
		StatusPassed, StatusFlaky, StatusFailed, StatusTimeout,
		StatusCancelled, StatusSkipped, StatusExcluded, StatusUnmatched,
	} {
		if n := rep.Summary.ByStatus[st]; n > 0 {
			fmt.Fprintf(w, "  %-10s %d\n", st+":", n)
		}
	}
	for _, r := range rep.Results {
		if r.Status == StatusFailed || r.Status == StatusTimeout || r.Status == StatusCancelled {
			fmt.Fprintf(w, "  %-10s %s (%s)\n", r.Status+":", r.Name, r.Reason)
		}
	}
}
