// Package runner discovers, executes and reports end-to-end test scenarios.
package runner

import (
	"encoding/json"
	"fmt"
	"time"
)

// Status is the final verdict of a single scenario.
type Status string

const (
	StatusPassed    Status = "passed"    // succeeded (on the first attempt)
	StatusFlaky     Status = "flaky"     // failed some attempts, eventually passed
	StatusFailed    Status = "failed"    // exited non-zero on every attempt
	StatusTimeout   Status = "timeout"   // exceeded its per-scenario timeout
	StatusCancelled Status = "cancelled" // interrupted by global deadline / signal
	StatusSkipped   Status = "skipped"   // opted out in its own manifest
	StatusExcluded  Status = "excluded"  // matched by the exclude filter
	StatusUnmatched Status = "unmatched" // did not match the include filter
)

// Scenario describes one discovered end-to-end test.
type Scenario struct {
	Name    string   `json:"name"`
	Dir     string   `json:"dir"`
	Command string   `json:"command"`
	Timeout Duration `json:"timeout"`
	Retries int      `json:"retries"`
	Skip    bool     `json:"skip"`
	Tags    []string `json:"tags,omitempty"`
}

// manifest is the on-disk form (scenario.json); Dir is filled in by discovery.
type manifest struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Timeout Duration `json:"timeout"`
	Retries *int     `json:"retries"`
	Skip    bool     `json:"skip"`
	Tags    []string `json:"tags"`
}

// Attempt records a single execution try of a scenario.
type Attempt struct {
	Index      int           `json:"index"`
	Status     Status        `json:"status"`
	ExitCode   int           `json:"exit_code"`
	Duration   time.Duration `json:"duration_ns"`
	StartedAt  time.Time     `json:"started_at"`
	Reason     string        `json:"reason,omitempty"`
	OutputTail string        `json:"output_tail,omitempty"`
}

// Result is the machine-readable outcome of one scenario.
type Result struct {
	Name     string        `json:"name"`
	Status   Status        `json:"status"`
	Reason   string        `json:"reason,omitempty"`
	Duration time.Duration `json:"duration_ns"`
	Attempts []Attempt     `json:"attempts,omitempty"`
}

// Config controls a whole run.
type Config struct {
	Root       string        // directory scanned for scenarios
	Include    []string      // names/tags that must match (empty = all)
	Exclude    []string      // names/tags that must not match
	Parallel   int           // max concurrent scenarios
	Retries    int           // default retry count (scenario may override)
	Timeout    time.Duration // default per-scenario timeout
	Deadline   time.Duration // overall wall-clock deadline (0 = none)
	Seed       int64         // shuffle seed
	Shuffle    bool          // randomize execution order
	OutputFile string        // where to write the JSON report ("" = stdout only)
}

// Duration is a time.Duration that unmarshals from JSON strings like "5s"
// or plain numbers (nanoseconds).
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("invalid duration: %s", b)
	}
	*d = Duration(n)
	return nil
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}
