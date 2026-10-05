// Package filter classifies discovered scenarios before execution.
//
// Classification precedence (first match wins):
//
//  1. excluded  - name matches an --exclude entry (operator decision);
//  2. skipped   - the manifest sets skip:true with a skip_reason;
//  3. unmatched - a --name/--tag selector was given but the scenario matches
//     neither an included name nor any included tag;
//  4. included  - disposition empty, the scenario will run.
package filter

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

// Selector configures which scenarios participate in a run.
type Selector struct {
	// Names are exact scenario names to include. Empty means "no name filter".
	Names []string
	// Tags: a scenario carrying ANY of these tags is included.
	Tags []string
	// Excludes are exact names forcibly excluded, even if named/tag-selected.
	Excludes []string
}

// Decision is the pre-execution classification of one scenario.
type Decision struct {
	Scenario    *scenario.Scenario
	Disposition scenario.Disposition
	Reason      string
}

// Included reports whether the scenario should execute.
func (d Decision) Included() bool { return d.Disposition == scenario.DispNone }

// Apply classifies every scenario, preserving discovery order.
func Apply(ctx context.Context, scenarios []*scenario.Scenario, sel Selector) []Decision {
	out := make([]Decision, 0, len(scenarios))
	for _, s := range scenarios {
		if err := ctx.Err(); err != nil {
			break
		}
		out = append(out, classify(s, sel))
	}
	return out
}

func classify(s *scenario.Scenario, sel Selector) Decision {
	switch {
	case slices.Contains(sel.Excludes, s.Name):
		return Decision{
			Scenario:    s,
			Disposition: scenario.DispExcluded,
			Reason:      fmt.Sprintf("excluded by name via --exclude=%s", s.Name),
		}
	case s.Skip:
		return Decision{
			Scenario:    s,
			Disposition: scenario.DispSkipped,
			Reason:      fmt.Sprintf("skipped in manifest: %s", s.SkipReason),
		}
	case !matches(s, sel):
		return Decision{
			Scenario:    s,
			Disposition: scenario.DispUnmatched,
			Reason:      unmatchedReason(s, sel),
		}
	default:
		return Decision{Scenario: s, Disposition: scenario.DispNone}
	}
}

// matches reports whether s satisfies the active name/tag selectors.
// With no selectors active every scenario matches.
func matches(s *scenario.Scenario, sel Selector) bool {
	if slices.Contains(sel.Names, s.Name) {
		return true
	}
	for _, t := range sel.Tags {
		if slices.Contains(s.Tags, t) {
			return true
		}
	}
	return len(sel.Names) == 0 && len(sel.Tags) == 0
}

func unmatchedReason(s *scenario.Scenario, sel Selector) string {
	var parts []string
	if len(sel.Names) > 0 {
		parts = append(parts, fmt.Sprintf("name %q not in --name set [%s]", s.Name, strings.Join(sel.Names, ",")))
	}
	if len(sel.Tags) > 0 {
		parts = append(parts, fmt.Sprintf("tags %v do not include any of --tag set [%s]", s.Tags, strings.Join(sel.Tags, ",")))
	}
	return strings.Join(parts, "; ")
}
