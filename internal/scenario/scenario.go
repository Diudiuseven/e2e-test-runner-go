// Package scenario defines e2e test scenarios and discovers them on disk.
//
// A scenario is a directory containing a scenario.json manifest and any
// support files it needs. The manifest names a command (executable plus
// arguments) which is executed with the scenario directory as its working
// directory. Discovery order is the sorted manifest path order, which is
// the stable identity used for result ordering.
package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Status is the disposition of a single attempt or of an aggregated scenario.
type Status string

const (
	StatusPassed      Status = "passed"
	StatusFailed      Status = "failed"
	StatusFlaky       Status = "flaky"
	StatusTimeout     Status = "timeout"
	StatusCrashed     Status = "crashed"
	StatusCanceled    Status = "canceled"
	StatusSkipped     Status = "skipped"
	StatusExcluded    Status = "excluded"
	StatusUnmatched   Status = "unmatched"
	StatusNotExecuted Status = "not_executed"
)

// Disposition records why a scenario was classified before execution.
type Disposition string

const (
	DispNone      Disposition = ""
	DispSkipped   Disposition = "skipped"
	DispExcluded  Disposition = "excluded"
	DispUnmatched Disposition = "unmatched"
)

// ManifestFile is the JSON shape of scenario.json.
type ManifestFile struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Command     []string `json:"command"`
	// Timeout is a Go duration string, e.g. "2s", "500ms"; empty = no limit.
	Timeout string `json:"timeout,omitempty"`
	// Retries is the number of EXTRA attempts after the initial attempt.
	Retries    int      `json:"retries,omitempty"`
	Idempotent bool     `json:"idempotent"`
	Tags       []string `json:"tags,omitempty"`
	Skip       bool     `json:"skip,omitempty"`
	SkipReason string   `json:"skip_reason,omitempty"`
}

// Scenario is a loaded, validated manifest paired with its directory.
type Scenario struct {
	ManifestFile
	Timeout time.Duration
	Dir     string
}

// Attempt is one execution of a scenario (initial run or a retry).
type Attempt struct {
	Index      int           `json:"index"`
	Status     Status        `json:"status"`
	ExitCode   int           `json:"exit_code"`
	Duration   time.Duration `json:"-"`
	Reason     string        `json:"reason,omitempty"`
	Signal     string        `json:"signal,omitempty"`
	OutputTail string        `json:"output_tail,omitempty"`
	StartedAt  time.Time     `json:"started_at"`
}

// DurationMS serializes Duration as whole milliseconds.
func (a Attempt) DurationMS() int64 { return a.Duration.Milliseconds() }

// MarshalJSON renders Duration in milliseconds.
func (a Attempt) MarshalJSON() ([]byte, error) {
	type alias Attempt
	return json.Marshal(struct {
		alias
		DurationMS int64 `json:"duration_ms"`
	}{alias: alias(a), DurationMS: a.Duration.Milliseconds()})
}

const manifestName = "scenario.json"

// ErrInvalidManifest marks a malformed scenario manifest.
var ErrInvalidManifest = errors.New("invalid manifest")

// Discover walks root and loads every scenario.json manifest, sorted by path.
func Discover(ctx context.Context, root string) ([]*Scenario, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == manifestName {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q: %w", root, err)
	}
	slices.Sort(paths)

	out := make([]*Scenario, 0, len(paths))
	seen := map[string]string{}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, err := load(p)
		if err != nil {
			return nil, err
		}
		if other, dup := seen[s.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate scenario name %q in %q and %q",
				ErrInvalidManifest, s.Name, other, s.Dir)
		}
		seen[s.Name] = s.Dir
		out = append(out, s)
	}
	return out, nil
}

func load(path string) (*Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", path, err)
	}
	var mf ManifestFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&mf); err != nil {
		return nil, fmt.Errorf("%w: parse manifest %q: %v", ErrInvalidManifest, path, err)
	}
	s := &Scenario{ManifestFile: mf, Dir: filepath.Dir(path)}
	if s.Name == "" {
		return nil, fmt.Errorf("%w: %q: name is required", ErrInvalidManifest, path)
	}
	if len(s.Command) == 0 || s.Command[0] == "" {
		return nil, fmt.Errorf("%w: %q (%s): command must be a non-empty array", ErrInvalidManifest, path, s.Name)
	}
	if s.Retries < 0 {
		return nil, fmt.Errorf("%w: %q (%s): retries must be >= 0", ErrInvalidManifest, path, s.Name)
	}
	if mf.Timeout != "" {
		d, err := time.ParseDuration(mf.Timeout)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%w: %q (%s): timeout must be a positive duration, got %q",
				ErrInvalidManifest, path, s.Name, mf.Timeout)
		}
		s.Timeout = d
	}
	if s.Skip && s.SkipReason == "" {
		return nil, fmt.Errorf("%w: %q (%s): skip_reason is required when skip is true",
			ErrInvalidManifest, path, s.Name)
	}
	return s, nil
}
