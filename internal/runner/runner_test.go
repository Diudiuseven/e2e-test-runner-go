package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/filter"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

type fixture struct {
	name     string
	script   string
	manifest string
}

// buildRoot materializes scenarios under a temp dir in the given order.
func buildRoot(t *testing.T, fs []fixture) string {
	t.Helper()
	root := t.TempDir()
	for i, f := range fs {
		dir := filepath.Join(root, fmt.Sprintf("s%02d-%s", i, f.name))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if f.script != "" {
			if err := os.WriteFile(filepath.Join(dir, "run.sh"),
				[]byte("#!/bin/sh\n"+f.script), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		body := f.manifest
		if body == "" {
			body = `{"name":"` + f.name + `","command":["./run.sh"]}`
		}
		if err := os.WriteFile(filepath.Join(dir, "scenario.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func statusMap(res *RunResult) map[string]scenario.Status {
	m := map[string]scenario.Status{}
	for _, r := range res.Results {
		m[r.Name] = r.Status
	}
	return m
}

func find(res *RunResult, name string) *ScenarioResult {
	for _, r := range res.Results {
		if r.Name == name {
			return r
		}
	}
	return nil
}

func simplePass(n string) fixture { return fixture{name: n, script: "exit 0"} }
func simpleFail(n string) fixture { return fixture{name: n, script: "exit 1"} }

func TestConcurrencyDoesNotChangeConclusions(t *testing.T) {
	fs := []fixture{
		simplePass("a"),
		simpleFail("b"),
		simplePass("c"),
		simpleFail("d"),
		simplePass("e"),
	}
	root := buildRoot(t, fs)
	var prev map[string]scenario.Status
	for _, c := range []int{1, 2, 5} {
		res, err := New(Config{Root: root, Concurrency: c}).Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got := statusMap(res)
		if prev != nil {
			for k, v := range prev {
				if got[k] != v {
					t.Fatalf("concurrency %d changed %s: %s -> %s", c, k, v, got[k])
				}
			}
		}
		prev = got
	}
	want := map[string]scenario.Status{
		"a": scenario.StatusPassed, "b": scenario.StatusFailed,
		"c": scenario.StatusPassed, "d": scenario.StatusFailed, "e": scenario.StatusPassed,
	}
	for k, v := range want {
		if prev[k] != v {
			t.Fatalf("%s: want %s got %s", k, v, prev[k])
		}
	}
}

func TestResultOrderIsDiscoveryOrder(t *testing.T) {
	// First scenario is slowest; completion order must not reorder results.
	fs := []fixture{
		{name: "slow-first", script: "sleep 0.3; exit 0"},
		simplePass("fast-1"),
		simplePass("fast-2"),
	}
	root := buildRoot(t, fs)
	for _, c := range []int{1, 3} {
		res, err := New(Config{Root: root, Concurrency: c}).Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, len(res.Results))
		for i, r := range res.Results {
			names[i] = r.Name
		}
		want := []string{"slow-first", "fast-1", "fast-2"}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("concurrency %d reordered results: %v", c, names)
			}
		}
	}
}

func TestFlakyPassedAfterRetry(t *testing.T) {
	fs := []fixture{{
		name:     "flaky",
		script:   `[ "$E2E_RUNNER_ATTEMPT" = "0" ] && exit 1; exit 0`,
		manifest: `{"name":"flaky","command":["./run.sh"],"retries":2,"idempotent":true}`,
	}}
	root := buildRoot(t, fs)
	res, err := New(Config{Root: root, Concurrency: 1}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := find(res, "flaky")
	if r.Status != scenario.StatusFlaky {
		t.Fatalf("want flaky, got %s", r.Status)
	}
	if len(r.Attempts) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(r.Attempts))
	}
	if r.Attempts[0].Status != scenario.StatusFailed || r.Attempts[1].Status != scenario.StatusPassed {
		t.Fatalf("attempt history wrong: %+v", r.Attempts)
	}
	// Exactly one result entry for the scenario.
	if len(res.Results) != 1 {
		t.Fatalf("retries duplicated result rows: %d", len(res.Results))
	}
}

func TestPersistentFailureRetriedButCountedOnce(t *testing.T) {
	fs := []fixture{{
		name:     "doomed",
		script:   "exit 1",
		manifest: `{"name":"doomed","command":["./run.sh"],"retries":2,"idempotent":true}`,
	}}
	root := buildRoot(t, fs)
	res, err := New(Config{Root: root, Concurrency: 1}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := find(res, "doomed")
	if r.Status != scenario.StatusFailed {
		t.Fatalf("want failed, got %s", r.Status)
	}
	if len(r.Attempts) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(r.Attempts))
	}
}

func TestNonIdempotentNeverRetried(t *testing.T) {
	fs := []fixture{{
		name:     "sideeffects",
		script:   "exit 1",
		manifest: `{"name":"sideeffects","command":["./run.sh"],"retries":3}`,
	}}
	root := buildRoot(t, fs)
	res, err := New(Config{Root: root, Concurrency: 1}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := find(res, "sideeffects")
	if len(r.Attempts) != 1 {
		t.Fatalf("non-idempotent scenario executed %d times", len(r.Attempts))
	}
	if r.Status != scenario.StatusFailed {
		t.Fatalf("want failed, got %s", r.Status)
	}
}

func TestCrashIsolation(t *testing.T) {
	fs := []fixture{
		simplePass("before"),
		{name: "crasher", script: "kill -9 $$"},
		simplePass("after"),
	}
	root := buildRoot(t, fs)
	res, err := New(Config{Root: root, Concurrency: 3}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := statusMap(res)
	if m["before"] != scenario.StatusPassed || m["after"] != scenario.StatusPassed {
		t.Fatalf("neighbors affected by crash: %v", m)
	}
	if m["crasher"] != scenario.StatusCrashed {
		t.Fatalf("want crashed, got %s", m["crasher"])
	}
}

func TestDeadlineMarksInFlightAndNotStarted(t *testing.T) {
	fs := []fixture{
		{name: "slow1", script: "sleep 0.5; exit 0"},
		{name: "slow2", script: "sleep 0.5; exit 0"},
		{name: "slow3", script: "sleep 0.5; exit 0"},
	}
	root := buildRoot(t, fs)
	start := time.Now()
	res, err := New(Config{Root: root, Concurrency: 1, Deadline: 250 * time.Millisecond}).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Deadline {
		t.Fatal("deadline flag not set")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("deadline run hung")
	}
	r1 := find(res, "slow1")
	if r1.Status != scenario.StatusCanceled && r1.Status != scenario.StatusTimeout {
		t.Fatalf("in-flight scenario want canceled/timeout, got %s", r1.Status)
	}
	r2 := find(res, "slow2")
	if r2.Status != scenario.StatusNotExecuted {
		t.Fatalf("queued scenario want not_executed, got %s", r2.Status)
	}
}

func TestSelectorDispositions(t *testing.T) {
	fs := []fixture{
		simplePass("a"),
		simplePass("b"),
		{name: "c", script: "exit 0", manifest: `{"name":"c","command":["./run.sh"],"skip":true,"skip_reason":"wip"}`},
	}
	root := buildRoot(t, fs)
	cfg := Config{
		Root:        root,
		Concurrency: 2,
		Selector:    filter.Selector{Names: []string{"a"}, Excludes: []string{"a"}},
	}
	res, err := New(cfg).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := statusMap(res)
	if m["a"] != scenario.StatusExcluded {
		t.Fatalf("a: want excluded, got %s", m["a"])
	}
	if m["b"] != scenario.StatusUnmatched {
		t.Fatalf("b: want unmatched, got %s", m["b"])
	}
	if m["c"] != scenario.StatusSkipped {
		t.Fatalf("c: want skipped, got %s", m["c"])
	}
	for _, r := range res.Results {
		if r.Status != scenario.StatusPassed && r.Reason == "" {
			t.Fatalf("%s: %s needs a reason", r.Name, r.Status)
		}
	}
}

func TestShuffleSameSeedSameSet(t *testing.T) {
	fs := []fixture{simplePass("a"), simplePass("b"), simplePass("c"), simplePass("d")}
	root := buildRoot(t, fs)
	run := func(seed uint64) []string {
		res, err := New(Config{Root: root, Concurrency: 1, Shuffle: true, Seed: seed}).
			Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(res.Results))
		for _, r := range res.Results {
			names = append(names, r.Name)
		}
		sort.Strings(names)
		return names
	}
	a, b := run(11), run(11)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("same seed different sets: %v vs %v", a, b)
	}
	c := run(22)
	if fmt.Sprint(a) != fmt.Sprint(c) {
		t.Fatal("different seed changed the participant set")
	}
}

func TestScenarioResultJSONShape(t *testing.T) {
	sr := ScenarioResult{Name: "x", Status: scenario.StatusPassed, Duration: 2500 * time.Millisecond, Retries: 0}
	raw, err := json.Marshal(sr)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["duration_ms"].(float64) != 2500 {
		t.Fatalf("duration_ms wrong: %v", m["duration_ms"])
	}
	if _, ok := m["Duration"]; ok {
		t.Fatal("nanosecond duration field leaked into JSON")
	}
}
