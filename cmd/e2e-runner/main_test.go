package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func mkCLIScenario(t *testing.T, root, name, script string, manifest ...string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"),
		[]byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":%q,"command":["./run.sh"]}`, name)
	if len(manifest) > 0 {
		body = manifest[0]
	}
	if err := os.WriteFile(filepath.Join(dir, "scenario.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIFailureExitCodeAndReport(t *testing.T) {
	root := t.TempDir()
	mkCLIScenario(t, root, "p", "exit 0")
	mkCLIScenario(t, root, "f", "exit 1")
	reportPath := filepath.Join(root, "out", "report.json")

	var out, errb bytes.Buffer
	code := run([]string{"-root", root, "-concurrency", "2",
		"-report", reportPath, "-format", "text"}, &out, &errb)
	if code != 1 {
		t.Fatalf("want exit 1, got %d (stderr=%s)", code, errb.String())
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report file not written: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	results := doc["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("want 2 result rows, got %d", len(results))
	}
	if !bytes.Contains(out.Bytes(), []byte("[failed] f")) {
		t.Fatalf("text summary missing failure line:\n%s", out.String())
	}
}

func TestCLIAllPassExitZero(t *testing.T) {
	root := t.TempDir()
	mkCLIScenario(t, root, "p1", "exit 0")
	mkCLIScenario(t, root, "p2", "exit 0")
	code := run([]string{"-root", root}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("want exit 0, got %d", code)
	}
}

func TestCLIUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-concurrency", "0"},
		{"-format", "xml"},
		{"-root", filepath.Join(t.TempDir(), "missing")},
		{"-bogus-flag"},
	} {
		code := run(args, &bytes.Buffer{}, &bytes.Buffer{})
		if code != 3 {
			t.Fatalf("args %v: want exit 3, got %d", args, code)
		}
	}
}

func TestCLIInterruptedReportPreserved(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		mkCLIScenario(t, root, fmt.Sprintf("slow-%d", i), "sleep 5; exit 0")
	}
	reportPath := filepath.Join(t.TempDir(), "report.json")

	// Run in a goroutine, signal after scenarios started.
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"-root", root, "-concurrency", "1",
			"-report", reportPath}, &bytes.Buffer{}, &bytes.Buffer{})
	}()
	time.Sleep(300 * time.Millisecond)
	if err := sendInterrupt(); err != nil {
		t.Skipf("cannot signal self: %v", err)
	}
	select {
	case code := <-done:
		if code != 2 {
			t.Fatalf("want exit 2 after interrupt, got %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("interrupted run did not return")
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal("partial report was not preserved after interrupt")
	}
	var doc struct {
		Canceled bool `json:"canceled"`
		Results  []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Canceled {
		t.Fatal("report does not record cancellation")
	}
	if len(doc.Results) != 3 {
		t.Fatalf("want 3 result rows, got %d", len(doc.Results))
	}
}

func TestCLIRepeatedRunsStable(t *testing.T) {
	root := t.TempDir()
	mkCLIScenario(t, root, "a", "exit 0")
	mkCLIScenario(t, root, "b", "exit 1")
	mkCLIScenario(t, root, "c", `[ "$E2E_RUNNER_ATTEMPT" = "0" ] && exit 1; exit 0`,
		`{"name":"c","command":["./run.sh"],"retries":2,"idempotent":true}`)

	normalize := func() string {
		var out bytes.Buffer
		code := run([]string{"-root", root, "-shuffle", "-seed", "5",
			"-concurrency", "2"}, &out, &bytes.Buffer{})
		if code != 1 {
			t.Fatalf("want 1, got %d", code)
		}
		var doc struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		// Strip timing fields that legitimately vary.
		for _, r := range doc.Results {
			delete(r, "duration_ms")
			for _, a := range r["attempts"].([]any) {
				am := a.(map[string]any)
				delete(am, "duration_ms")
				delete(am, "started_at")
			}
		}
		b, _ := json.Marshal(doc.Results)
		return string(b)
	}
	first := normalize()
	for i := 0; i < 3; i++ {
		if got := normalize(); got != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", i+2, first, got)
		}
	}
}

// sendInterrupt is defined per-OS in signal_unix.go.
var _ = syscall.SIGINT
