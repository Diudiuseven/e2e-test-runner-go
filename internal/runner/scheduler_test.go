package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sleepScript = "#!/bin/sh\nsleep 0.4\n"

// TestConcurrencyIsBounded verifies that parallel=1 runs serially and
// parallel=N overlaps execution, by comparing wall-clock times.
func TestConcurrencyIsBounded(t *testing.T) {
	newRoot := func() string {
		root := t.TempDir()
		for _, n := range []string{"s1", "s2", "s3", "s4"} {
			writeScenario(t, root, n, `{"command":"sh run.sh","timeout":"10s"}`, sleepScript)
		}
		return root
	}

	start := time.Now()
	if _, err := Run(context.Background(), Config{Root: newRoot(), Parallel: 1}); err != nil {
		t.Fatal(err)
	}
	serial := time.Since(start)

	start = time.Now()
	if _, err := Run(context.Background(), Config{Root: newRoot(), Parallel: 4}); err != nil {
		t.Fatal(err)
	}
	parallel := time.Since(start)

	if serial < 1500*time.Millisecond {
		t.Errorf("serial run too fast (%s); scenarios did not run one at a time", serial)
	}
	if parallel > 1200*time.Millisecond {
		t.Errorf("parallel run too slow (%s); scenarios did not overlap", parallel)
	}
}

// TestResultOrderIndependentOfConcurrency runs the same set with different
// parallelism and shuffle seeds and requires identical per-scenario verdicts.
func TestResultOrderIndependentOfConcurrency(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "ok", `{"command":"sh run.sh","timeout":"5s"}`, "#!/bin/sh\nexit 0\n")
	writeScenario(t, root, "bad", `{"command":"sh run.sh","timeout":"5s"}`, "#!/bin/sh\nexit 1\n")

	var baseline []Result
	for i, cfg := range []Config{
		{Root: root, Parallel: 1},
		{Root: root, Parallel: 8, Shuffle: true, Seed: 42},
		{Root: root, Parallel: 3, Shuffle: true, Seed: 7},
	} {
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		// compare only the stable fields
		strip := func(rs []Result) []Result {
			out := make([]Result, len(rs))
			for i, r := range rs {
				out[i] = Result{Name: r.Name, Status: r.Status}
			}
			return out
		}
		if i == 0 {
			baseline = strip(res)
			continue
		}
		if !reflect.DeepEqual(baseline, strip(res)) {
			t.Errorf("run %d differs: %+v vs %+v", i, baseline, strip(res))
		}
	}
}

// TestTimeoutKillsProcessGroup ensures a timed-out scenario leaves no
// detached children behind.
func TestTimeoutKillsProcessGroup(t *testing.T) {
	root := t.TempDir()
	shared := t.TempDir()
	t.Setenv("E2E_SHARED", shared)
	// The child would write a marker after 1s if it survived the timeout.
	script := "#!/bin/sh\nsleep 1 && touch \"$E2E_SHARED/escaped\" &\nsleep 30\n"
	writeScenario(t, root, "hanger", `{"command":"sh run.sh","timeout":"200ms"}`, script)

	res, err := Run(context.Background(), Config{Root: root, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := res[0].Status; got != StatusTimeout {
		t.Fatalf("want timeout, got %q (%s)", got, res[0].Reason)
	}
	// Give a hypothetical escaped child time to write its marker.
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(shared, "escaped")); !os.IsNotExist(err) {
		t.Fatal("child process survived the timeout; process group was not killed")
	}
}

// TestCrashIsolation: a crashing scenario must not affect its neighbours.
func TestCrashIsolation(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "crasher", `{"command":"sh run.sh","timeout":"5s"}`, "#!/bin/sh\nkill -SEGV $$\n")
	writeScenario(t, root, "healthy", `{"command":"sh run.sh","timeout":"5s"}`, "#!/bin/sh\nexit 0\n")

	res, err := Run(context.Background(), Config{Root: root, Parallel: 2})
	if err != nil {
		t.Fatal(err)
	}
	m := byName(res)
	if m["crasher"].Status != StatusFailed {
		t.Errorf("crasher: want failed, got %q", m["crasher"].Status)
	}
	if m["healthy"].Status != StatusPassed {
		t.Errorf("healthy: want passed, got %q", m["healthy"].Status)
	}
}

// TestRetryFlaky: a scenario failing once then passing is reported flaky,
// with the full attempt history in a single result entry.
func TestRetryFlaky(t *testing.T) {
	root := t.TempDir()
	script := `#!/bin/sh
c=0
[ -f "$SCENARIO_STATE_DIR/c" ] && c=$(cat "$SCENARIO_STATE_DIR/c")
c=$((c+1)); echo "$c" > "$SCENARIO_STATE_DIR/c"
[ "$c" -ge 2 ]
`
	writeScenario(t, root, "flaky", `{"command":"sh run.sh","timeout":"5s","retries":2}`, script)

	res, err := Run(context.Background(), Config{Root: root, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("want exactly 1 result entry, got %d", len(res))
	}
	r := res[0]
	if r.Status != StatusFlaky {
		t.Fatalf("want flaky, got %q", r.Status)
	}
	if len(r.Attempts) != 2 || r.Attempts[0].Status != StatusFailed || r.Attempts[1].Status != StatusPassed {
		t.Fatalf("unexpected attempt history: %+v", r.Attempts)
	}
}

// TestRetryExhausted: retries do not duplicate result entries and a
// permanently failing scenario stays failed.
func TestRetryExhausted(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "bad", `{"command":"sh run.sh","timeout":"5s","retries":2}`, "#!/bin/sh\nexit 1\n")

	res, err := Run(context.Background(), Config{Root: root, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("retries must not add result entries; got %d", len(res))
	}
	if res[0].Status != StatusFailed || len(res[0].Attempts) != 3 {
		t.Fatalf("want failed with 3 attempts, got %q with %d", res[0].Status, len(res[0].Attempts))
	}
}

// TestCancellationPreservesPartialResults: with a tight deadline, finished
// scenarios keep their verdicts and the rest is marked cancelled.
func TestCancellationPreservesPartialResults(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "a-quick", `{"command":"sh run.sh","timeout":"5s"}`, "#!/bin/sh\nexit 0\n")
	writeScenario(t, root, "b-slow", `{"command":"sh run.sh","timeout":"30s"}`, "#!/bin/sh\nsleep 30\n")
	writeScenario(t, root, "c-slow", `{"command":"sh run.sh","timeout":"30s"}`, "#!/bin/sh\nsleep 30\n")

	cfg := Config{Root: root, Parallel: 1, Deadline: 800 * time.Millisecond}
	res, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected a non-nil error for an interrupted run")
	}
	m := byName(res)
	if m["a-quick"].Status != StatusPassed {
		t.Errorf("completed scenario lost its verdict: %q", m["a-quick"].Status)
	}
	for _, n := range []string{"b-slow", "c-slow"} {
		if m[n].Status != StatusCancelled {
			t.Errorf("%s: want cancelled, got %q (%s)", n, m[n].Status, m[n].Reason)
		}
	}
	if code := ExitCode(res, true); code != ExitInterrupted {
		t.Errorf("want exit code %d, got %d", ExitInterrupted, code)
	}
}

// TestShuffleSeedDeterminism: same seed => same execution order; different
// seeds may only reorder, never change the scenario set.
func TestShuffleSeedDeterminism(t *testing.T) {
	root := t.TempDir()
	shared := t.TempDir()
	t.Setenv("E2E_SHARED", shared)
	names := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"}
	for _, n := range names {
		writeScenario(t, root, n, `{"command":"sh run.sh","timeout":"5s"}`,
			"#!/bin/sh\necho \"$SCENARIO_NAME\" >> \"$E2E_SHARED/order.$E2E_RUN_ID\"\n")
	}

	runID := 0
	order := func(seed int64) []string {
		runID++
		id := strings.ReplaceAll(t.Name(), "/", "_") + "." + strconv.Itoa(runID)
		t.Setenv("E2E_RUN_ID", id)
		res, err := Run(context.Background(), Config{Root: root, Parallel: 1, Shuffle: true, Seed: seed})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if r.Status != StatusPassed {
				t.Fatalf("seed %d: %s = %s", seed, r.Name, r.Status)
			}
		}
		data, err := os.ReadFile(filepath.Join(shared, "order."+id))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Fields(string(data))
	}

	o1, o1again, o2 := order(1), order(1), order(2)
	if !reflect.DeepEqual(o1, o1again) {
		t.Errorf("same seed produced different orders: %v vs %v", o1, o1again)
	}
	if len(o2) != len(names) {
		t.Fatalf("seed changed the scenario set: %v", o2)
	}
	set := map[string]bool{}
	for _, n := range o2 {
		set[n] = true
	}
	for _, n := range names {
		if !set[n] {
			t.Errorf("scenario %s missing under a different seed", n)
		}
	}
	if reflect.DeepEqual(o1, o2) {
		t.Logf("warning: different seeds produced the same order (unlikely but legal): %v", o1)
	}
}
