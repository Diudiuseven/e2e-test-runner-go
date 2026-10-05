package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// outputTailLimit is how many bytes of combined output are kept per attempt.
const outputTailLimit = 4096

// execute runs a single attempt of s in an isolated environment:
// a fresh temporary copy of the scenario directory is used as the working
// directory, the command runs in its own process group, and on timeout or
// cancellation the whole group is killed so no processes leak.
// stateDir persists across the attempts of one scenario (and only that
// scenario), so genuinely flaky scenarios can be expressed without
// polluting the source tree.
func execute(ctx context.Context, s Scenario, attemptIdx int, timeout time.Duration, stateDir string) Attempt {
	att := Attempt{Index: attemptIdx, StartedAt: time.Now()}
	defer func() { att.Duration = time.Since(att.StartedAt) }()

	workDir, err := prepareWorkDir(s)
	if err != nil {
		att.Status = StatusFailed
		att.ExitCode = -1
		att.Reason = "prepare isolated workdir: " + err.Error()
		return att
	}
	defer os.RemoveAll(workDir)

	attCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(attCtx, "sh", "-c", s.Command)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"SCENARIO_NAME="+s.Name,
		"SCENARIO_SOURCE_DIR="+s.Dir,
		"SCENARIO_STATE_DIR="+stateDir,
		"TMPDIR="+workDir,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Kill the entire process group, not just the shell, so children that
	// outlive their parent (e.g. a detached sleeper) cannot leak.
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	var buf tailBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()
	att.OutputTail = buf.String()

	switch {
	case ctx.Err() != nil:
		// The run-wide context (deadline / signal) was cancelled. Check this
		// first: a parent deadline also cancels the per-attempt context, so
		// it must win over the per-attempt timeout classification.
		att.Status = StatusCancelled
		att.ExitCode = -1
		att.Reason = "interrupted: " + ctx.Err().Error()
	case errors.Is(attCtx.Err(), context.DeadlineExceeded):
		// The per-attempt timeout fired (and the parent context is fine).
		att.Status = StatusTimeout
		att.ExitCode = -1
		att.Reason = fmt.Sprintf("exceeded timeout %s; process group killed", timeout)
	case runErr == nil:
		att.Status = StatusPassed
		att.ExitCode = 0
	default:
		att.Status = StatusFailed
		att.ExitCode = exitCodeOf(runErr)
		att.Reason = runErr.Error()
	}
	return att
}

// exitCodeOf extracts a process exit code from an exec error.
func exitCodeOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// prepareWorkDir copies the scenario directory into a fresh temp dir so
// attempts cannot pollute each other or the original sources.
func prepareWorkDir(s Scenario) (string, error) {
	tmp, err := os.MkdirTemp("", "e2e-scenario-*")
	if err != nil {
		return "", err
	}
	if err := copyDir(s.Dir, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return tmp, nil
}

// copyDir recursively copies src into dst, preserving permission bits.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil // skip symlinks, sockets, ...
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

// tailBuffer keeps only the last outputTailLimit bytes written to it.
type tailBuffer struct {
	buf bytes.Buffer
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n, err := t.buf.Write(p)
	if t.buf.Len() > outputTailLimit {
		b := t.buf.Bytes()
		keep := make([]byte, outputTailLimit)
		copy(keep, b[len(b)-outputTailLimit:])
		t.buf.Reset()
		t.buf.Write(keep)
	}
	return n, err
}

func (t *tailBuffer) String() string { return t.buf.String() }

var _ io.Writer = (*tailBuffer)(nil)
