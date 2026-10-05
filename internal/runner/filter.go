package runner

import "path"

// classify decides whether a scenario should run and, if not, why.
// Precedence: skip (self-declared) > excluded (exclude filter) > unmatched
// (include filter present but not matched) > run.
func classify(s Scenario, cfg Config) Status {
	if s.Skip {
		return StatusSkipped
	}
	if matchesAny(cfg.Exclude, s) {
		return StatusExcluded
	}
	if len(cfg.Include) > 0 && !matchesAny(cfg.Include, s) {
		return StatusUnmatched
	}
	return "" // empty = should run
}

// matchesAny reports whether any pattern matches the scenario's name or tags.
// A pattern may be an exact string or a glob (e.g. "smoke-*").
func matchesAny(patterns []string, s Scenario) bool {
	for _, p := range patterns {
		if matchOne(p, s.Name) {
			return true
		}
		for _, t := range s.Tags {
			if matchOne(p, t) {
				return true
			}
		}
	}
	return false
}

func matchOne(pattern, value string) bool {
	if pattern == value {
		return true
	}
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}
