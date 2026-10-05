package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

// mkScenario writes a shell-script scenario into a temp dir.
func mkScenario(t *testing.T, body string, timeout time.Duration) *scenario.Scenario {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &scenario.Scenario{
		ManifestFile: scenario.ManifestFile{Name: "s", Command: []string{"./run.sh"}},
		Timeout:      timeout,
		Dir:          dir,
	}
}

func alive(pid int) bool {
	// Signal 0 checks existence without sending a signal.
	return syscall.Kill(pid, 0) == nil
}

func TestExecutePass(t *testing.T) {
	e := New()
	s := mkScenario(t, `echo hi; exit 0`, 0)
	r, err := e.Execute(context.Background(), s, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != scenario.StatusPassed {
		t.Fatalf("want passed, got %s: %s", r.Status, r.Reason)
	}
	if !strings.Contains(r.OutputTail, "hi") {
		t.Fatalf("output not captured: %q", r.OutputTail)
	}
}

func TestExecuteFailVsCrash(t *testing.T) {
	e := New()
	fail := mkScenario(t, `exit 3`, 0)
	r, _ := e.Execute(context.Background(), fail, 0)
	if r.Status != scenario.StatusFailed || r.ExitCode != 3 {
		t.Fatalf("want failed/exit3, got %s/%d", r.Status, r.ExitCode)
	}

	crash := mkScenario(t, `kill -9 $$`, 0)
	r, _ = e.Execute(context.Background(), crash, 0)
	if r.Status != scenario.StatusCrashed {
		t.Fatalf("want crashed, got %s: %s", r.Status, r.Reason)
	}
	if r.Signal == "" {
		t.Fatal("crashed attempt should record the signal")
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	e := New()
	// The script forks a grandchild sleeping long, writes its pid, then hangs.
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	body := "#!/bin/sh\n(sleep 300 & echo $! > child.pid)\nwhile true; do sleep 10; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &scenario.Scenario{
		ManifestFile: scenario.ManifestFile{Name: "hang", Command: []string{"./run.sh"}},
		Timeout:      300 * time.Millisecond,
		Dir:          dir,
	}
	start := time.Now()
	r, err := e.Execute(context.Background(), s, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != scenario.StatusTimeout {
		t.Fatalf("want timeout, got %s: %s", r.Status, r.Reason)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("timeout took too long: %v", d)
	}

	// Give the kernel a moment, then verify the grandchild is gone too.
	time.Sleep(200 * time.Millisecond)
	pidRaw, err := os.ReadFile(filepath.Join(dir, "child.pid"))
	if err != nil {
		t.Fatalf("grandchild pid file missing: %v", err)
	}
	var pid int
	if _, err := parsePID(string(pidRaw), &pid); err != nil {
		t.Fatal(err)
	}
	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("grandchild %d survived process-group timeout kill", pid)
	}
}

func TestParentCancelReportsCanceled(t *testing.T) {
	e := New()
	s := mkScenario(t, `while true; do sleep 10; done`, 0)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	r, err := e.Execute(ctx, s, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != scenario.StatusCanceled {
		t.Fatalf("want canceled, got %s: %s", r.Status, r.Reason)
	}
}

func TestEnvIsolationAndAttemptIndex(t *testing.T) {
	e := New()
	// HOME/TMPDIR point inside an isolated temp dir; attempt index is exposed.
	s := mkScenario(t, `
case "$HOME" in "$TMPDIR") ;; *) echo "HOME/TMPDIR mismatch"; exit 1;; esac
[ "$E2E_RUNNER_ATTEMPT" = "2" ] || { echo "bad attempt: $E2E_RUNNER_ATTEMPT"; exit 1; }
[ "$E2E_RUNNER_SCENARIO" = "s" ] || exit 1
echo isolated-ok`, 0)
	r, err := e.Execute(context.Background(), s, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != scenario.StatusPassed {
		t.Fatalf("want passed, got %s: %s output=%s", r.Status, r.Reason, r.OutputTail)
	}
}

func TestMissingCommandCrashes(t *testing.T) {
	e := New()
	s := &scenario.Scenario{
		ManifestFile: scenario.ManifestFile{Name: "s", Command: []string{"./nope.sh"}},
		Dir:          t.TempDir(),
	}
	r, err := e.Execute(context.Background(), s, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != scenario.StatusCrashed {
		t.Fatalf("want crashed for missing command, got %s", r.Status)
	}
}

func parsePID(s string, pid *int) (int, error) {
	s = strings.TrimSpace(s)
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	*pid = n
	return n, nil
}
