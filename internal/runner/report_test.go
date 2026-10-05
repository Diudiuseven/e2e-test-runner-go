package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		name        string
		results     []Result
		interrupted bool
		want        int
	}{
		{"all good", []Result{{Status: StatusPassed}, {Status: StatusFlaky}, {Status: StatusSkipped}}, false, ExitOK},
		{"failure", []Result{{Status: StatusPassed}, {Status: StatusFailed}}, false, ExitFailure},
		{"timeout", []Result{{Status: StatusTimeout}}, false, ExitFailure},
		{"interrupted wins", []Result{{Status: StatusFailed}}, true, ExitInterrupted},
		{"interrupted clean", []Result{{Status: StatusPassed}, {Status: StatusCancelled}}, true, ExitInterrupted},
	}
	for _, c := range cases {
		if got := ExitCode(c.results, c.interrupted); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

// TestFilterStatusesAreDistinct runs a set where every non-running status
// occurs and checks each scenario lands in the right bucket with a reason.
func TestFilterStatusesAreDistinct(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "keep", `{"command":"sh run.sh","timeout":"5s","tags":["fast"]}`, "#!/bin/sh\nexit 0\n")
	writeScenario(t, root, "skipper", `{"command":"sh run.sh","skip":true,"tags":["fast"]}`, "#!/bin/sh\nexit 0\n")
	writeScenario(t, root, "banned", `{"command":"sh run.sh","timeout":"5s","tags":["fast","banned"]}`, "#!/bin/sh\nexit 0\n")
	writeScenario(t, root, "slow-one", `{"command":"sh run.sh","timeout":"5s","tags":["slow"]}`, "#!/bin/sh\nexit 0\n")

	cfg := Config{Root: root, Parallel: 2, Include: []string{"fast"}, Exclude: []string{"banned"}}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := byName(res)
	want := map[string]Status{
		"keep":     StatusPassed,
		"skipper":  StatusSkipped,
		"banned":   StatusExcluded,
		"slow-one": StatusUnmatched,
	}
	for name, st := range want {
		r, ok := m[name]
		if !ok {
			t.Fatalf("scenario %s missing from results", name)
		}
		if r.Status != st {
			t.Errorf("%s: got %q, want %q", name, r.Status, st)
		}
		if st != StatusPassed && r.Reason == "" {
			t.Errorf("%s: filtered scenario must carry a reason", name)
		}
		if st != StatusPassed && len(r.Attempts) != 0 {
			t.Errorf("%s: filtered scenario must not execute", name)
		}
	}
}

// TestSkippedScenarioNeverExecutes guards against skip leaking into runs.
func TestSkippedScenarioNeverExecutes(t *testing.T) {
	root := t.TempDir()
	shared := t.TempDir()
	t.Setenv("E2E_SHARED", shared)
	writeScenario(t, root, "skipper", `{"command":"sh run.sh","skip":true}`,
		"#!/bin/sh\ntouch \"$E2E_SHARED/ran\"\n")

	if _, err := Run(context.Background(), Config{Root: root, Parallel: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shared, "ran")); err == nil {
		t.Fatal("skipped scenario executed")
	}
}
