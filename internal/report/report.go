// Package report renders run results in machine- and human-readable form and
// maps an outcome to a process exit code.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/runner"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

// Exit codes.
const (
	ExitOK          = 0 // every executed scenario passed (flaky counts as pass)
	ExitFailed      = 1 // at least one scenario failed/crashed/timed out
	ExitInterrupted = 2 // run canceled or overall deadline exceeded
	ExitUsage       = 3 // bad flags / configuration / discovery error
)

// ExitCode translates a run outcome into the process exit code. An
// interruption takes precedence over individual scenario failures so that
// truncated runs cannot be mistaken for complete failing runs.
func ExitCode(res *runner.RunResult) int {
	if res == nil {
		return ExitInterrupted
	}
	if res.Canceled || res.Deadline {
		return ExitInterrupted
	}
	for _, r := range res.Results {
		switch r.Status {
		case scenario.StatusFailed, scenario.StatusCrashed, scenario.StatusTimeout:
			return ExitFailed
		}
	}
	return ExitOK
}

// WriteJSON emits a stable, indented machine-readable JSON document.
func WriteJSON(w io.Writer, res *runner.RunResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return nil
}

// Counts summarizes a run.
type Counts struct {
	Passed, Flaky, Failed, Crashed, Timeout, Canceled, Skipped, Excluded, Unmatched, NotExecuted int
}

// Count tallies scenarios by status.
func Count(res *runner.RunResult) Counts {
	var c Counts
	if res == nil {
		return c
	}
	for _, r := range res.Results {
		switch r.Status {
		case scenario.StatusPassed:
			c.Passed++
		case scenario.StatusFlaky:
			c.Flaky++
		case scenario.StatusFailed:
			c.Failed++
		case scenario.StatusCrashed:
			c.Crashed++
		case scenario.StatusTimeout:
			c.Timeout++
		case scenario.StatusCanceled:
			c.Canceled++
		case scenario.StatusSkipped:
			c.Skipped++
		case scenario.StatusExcluded:
			c.Excluded++
		case scenario.StatusUnmatched:
			c.Unmatched++
		case scenario.StatusNotExecuted:
			c.NotExecuted++
		}
	}
	return c
}

// WriteText emits a deterministic human-readable summary to w.
func WriteText(w io.Writer, res *runner.RunResult) {
	c := Count(res)
	total := len(res.Results)
	dur := res.EndedAt.Sub(res.StartedAt)
	fmt.Fprintf(w, "e2e run: %d scenario(s) in %s\n", total, dur.Round(1_000_000))
	fmt.Fprintf(w, "  passed=%d flaky=%d failed=%d crashed=%d timeout=%d\n",
		c.Passed, c.Flaky, c.Failed, c.Crashed, c.Timeout)
	fmt.Fprintf(w, "  canceled=%d skipped=%d excluded=%d unmatched=%d not_executed=%d\n",
		c.Canceled, c.Skipped, c.Excluded, c.Unmatched, c.NotExecuted)
	if res.Deadline {
		fmt.Fprintln(w, "  run interrupted: overall deadline exceeded")
	}
	if res.Canceled {
		fmt.Fprintln(w, "  run interrupted: canceled")
	}
	for _, r := range res.Results {
		line := fmt.Sprintf("  [%s] %s", r.Status, r.Name)
		if r.Retries > 0 {
			line += fmt.Sprintf(" (retries=%d)", r.Retries)
		}
		if r.Reason != "" {
			line += " — " + truncate(r.Reason, 120)
		}
		fmt.Fprintln(w, line)
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
