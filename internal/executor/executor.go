// Package executor runs a single scenario attempt in strict isolation.
//
// Every attempt gets:
//   - its own process group (Setpgid), so timeout/cancel can signal the
//     command AND every descendant at once, leaving no orphan processes;
//   - its own temporary HOME/TMPDIR, removed when the attempt ends;
//   - a scrubbed environment plus explicit runner metadata;
//   - a per-scenario timeout derived from the manifest.
//
// Execute only returns after the process group has been reaped, so callers
// can trust that nothing is still running in the background.
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

const (
	// maxOutputTail is how many trailing stdout/stderr bytes we retain.
	maxOutputTail = 4096
	// killGrace is how long the runtime may wait after group kill before
	// reaping; bounded by cmd.WaitDelay.
	killGrace = 5 * time.Second
)

// Result is the outcome of a single attempt.
type Result struct {
	Status     scenario.Status
	ExitCode   int
	Reason     string
	Signal     string
	Duration   time.Duration
	StartedAt  time.Time
	OutputTail string
}

// Executor runs scenario attempts.
type Executor struct{}

// New builds an Executor.
func New() *Executor { return &Executor{} }

// Execute runs the scenario command exactly once. attempt is zero-based.
func (e *Executor) Execute(parent context.Context, s *scenario.Scenario, attempt int) (*Result, error) {
	started := time.Now()
	res := &Result{StartedAt: started}

	// Per-scenario timeout, nested inside the caller's global deadline.
	ctx := parent
	var cancel context.CancelFunc
	if s.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, s.Timeout)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	defer cancel()

	tmp, err := os.MkdirTemp("", "e2e-attempt-*")
	if err != nil {
		return nil, fmt.Errorf("create attempt temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	name, args, err := resolveCommand(s)
	if err != nil {
		res.Status = scenario.StatusCrashed
		res.Reason = err.Error()
		res.Duration = time.Since(started)
		return res, nil
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = s.Dir
	cmd.Env = buildEnv(tmp, s, attempt)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}

	out := newTailBuffer(maxOutputTail)
	cmd.Stdout = out
	cmd.Stderr = out

	// Killing the whole process group guarantees descendants (e.g. a shell
	// that forked an infinite loop) are signalled together.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = killGrace

	runErr := cmd.Run()
	res.Duration = time.Since(started)
	res.OutputTail = out.String()
	res.ExitCode = exitCode(cmd, runErr)
	res.Signal = exitSignal(cmd)

	switch {
	case runErr == nil:
		res.Status = scenario.StatusPassed
		return res, nil
	case errors.Is(runErr, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded:
		// A parent (global) deadline/cancel takes precedence over the local one.
		if parent.Err() != nil {
			res.Status = scenario.StatusCanceled
			res.Reason = interruptedReason(parent)
		} else if s.Timeout > 0 {
			res.Status = scenario.StatusTimeout
			res.Reason = fmt.Sprintf("scenario timeout after %s", s.Timeout)
		} else {
			res.Status = scenario.StatusCanceled
			res.Reason = interruptedReason(parent)
		}
	case ctx.Err() == context.Canceled || parent.Err() != nil:
		res.Status = scenario.StatusCanceled
		res.Reason = interruptedReason(parent)
	default:
		classifyExit(res, runErr, cmd)
	}
	return res, nil
}

// resolveCommand turns a manifest command into an executable path.
// A command name containing a slash is resolved against the scenario dir;
// anything else is looked up in PATH.
func resolveCommand(s *scenario.Scenario) (string, []string, error) {
	cmd0 := s.Command[0]
	args := s.Command[1:]
	if strings.ContainsRune(cmd0, '/') || filepath.IsAbs(cmd0) {
		p := cmd0
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.Dir, p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", nil, fmt.Errorf("resolve command %q: %w", cmd0, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", nil, fmt.Errorf("command %q not found in scenario dir: %w", cmd0, err)
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			return "", nil, fmt.Errorf("command %q is not executable", abs)
		}
		return abs, args, nil
	}
	found, err := exec.LookPath(cmd0)
	if err != nil {
		return "", nil, fmt.Errorf("command %q not found in PATH: %w", cmd0, err)
	}
	return found, args, nil
}

func buildEnv(tmp string, s *scenario.Scenario, attempt int) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmp,
		"TMPDIR=" + tmp,
		"TEMP=" + tmp,
		"TMP=" + tmp,
		"LANG=C",
		"E2E_RUNNER_ATTEMPT=" + itoa(attempt),
		"E2E_RUNNER_SCENARIO=" + s.Name,
	}
	// Pass through only explicitly opt-in variables.
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "E2E_") &&
			!strings.HasPrefix(kv, "E2E_RUNNER_") {
			env = append(env, kv)
		}
	}
	return env
}

func classifyExit(res *Result, runErr error, cmd *exec.Cmd) {
	var exitErr *exec.ExitError
	if sig := exitSignal(cmd); sig != "" {
		res.Status = scenario.StatusCrashed
		res.Reason = fmt.Sprintf("process terminated by signal %s", sig)
		return
	}
	if errors.As(runErr, &exitErr) {
		res.Status = scenario.StatusFailed
		res.Reason = fmt.Sprintf("exit status %d", exitErr.ExitCode())
		return
	}
	res.Status = scenario.StatusCrashed
	res.Reason = fmt.Sprintf("failed to run: %v", runErr)
}

func exitCode(cmd *exec.Cmd, runErr error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func exitSignal(cmd *exec.Cmd) string {
	if cmd.ProcessState == nil {
		return ""
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return ""
	}
	if ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}

func interruptedReason(ctx context.Context) string {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return "interrupted: overall deadline exceeded"
	case context.Canceled:
		return "interrupted: run canceled"
	default:
		return "interrupted"
	}
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}

// tailBuffer retains only the last max bytes written to it.
type tailBuffer struct {
	max  int
	buf  bytes.Buffer
	full bool
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	if !t.full {
		room := t.max - t.buf.Len()
		if len(p) >= room {
			t.buf.Write(p[len(p)-room:])
			t.full = true
		} else {
			t.buf.Write(p)
		}
		return len(p), nil
	}
	if len(p) >= t.max {
		t.buf.Reset()
		t.buf.Write(p[len(p)-t.max:])
		return len(p), nil
	}
	// Drop len(p) oldest bytes, append the new ones.
	b := t.buf.Bytes()
	copy(b, b[len(p):])
	t.buf.Truncate(t.max - len(p))
	t.buf.Write(p)
	return len(p), nil
}

func (t *tailBuffer) String() string { return t.buf.String() }

var _ io.Writer = (*tailBuffer)(nil)
