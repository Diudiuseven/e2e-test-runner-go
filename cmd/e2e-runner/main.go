// Command e2e-runner discovers, isolates and runs e2e test scenarios and
// emits a stable machine-readable report. See README.md for the full flag and
// exit-code reference.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/filter"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/report"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/runner"
)

type options struct {
	root        string
	concurrency int
	deadline    time.Duration
	seed        uint64
	shuffle     bool
	names       csvFlag
	tags        csvFlag
	excludes    csvFlag
	reportPath  string
	format      string // json | text
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("e2e-runner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts options
	fs.StringVar(&opts.root, "root", "testdata/e2e", "directory tree to scan for scenario.json files")
	fs.IntVar(&opts.concurrency, "concurrency", 1, "number of scenarios executed in parallel (>=1)")
	fs.DurationVar(&opts.deadline, "deadline", 0, "overall deadline for the whole run, e.g. 30s (0 = unlimited)")
	fs.Uint64Var(&opts.seed, "seed", 0, "random seed used when -shuffle is set; same seed => same order")
	fs.BoolVar(&opts.shuffle, "shuffle", false, "shuffle included scenarios before execution (order set by -seed)")
	fs.Var(&opts.names, "name", "include scenario by exact name (repeatable, comma-separated)")
	fs.Var(&opts.tags, "tag", "include scenarios carrying this tag (repeatable, comma-separated)")
	fs.Var(&opts.excludes, "exclude", "force-exclude scenario by exact name (repeatable, comma-separated)")
	fs.StringVar(&opts.reportPath, "report", "", "write the JSON report to this path as well as stdout (- = stdout only)")
	fs.StringVar(&opts.format, "format", "json", "stdout format: json or text")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: e2e-runner [flags]\n\nflags:\n")
		fs.PrintDefaults()
		fmt.Fprintln(stderr, "\nexit codes: 0 ok, 1 failure/timeout/crash, 2 interrupted, 3 usage/config error")
	}
	if err := fs.Parse(args); err != nil {
		return report.ExitUsage
	}
	if opts.concurrency < 1 {
		fmt.Fprintln(stderr, "error: -concurrency must be >= 1")
		return report.ExitUsage
	}
	if opts.format != "json" && opts.format != "text" {
		fmt.Fprintln(stderr, "error: -format must be json or text")
		return report.ExitUsage
	}

	// SIGINT/SIGTERM cancel the run; already-finished results are still saved.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := runner.Config{
		Root:        opts.root,
		Concurrency: opts.concurrency,
		Deadline:    opts.deadline,
		Seed:        opts.seed,
		Shuffle:     opts.shuffle,
		Selector: filter.Selector{
			Names:    opts.names,
			Tags:     opts.tags,
			Excludes: opts.excludes,
		},
	}

	r := runner.New(cfg)
	res, err := r.Run(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		// Discovery/config errors are fatal; there is no partial report.
		if errors.Is(err, os.ErrNotExist) {
			return report.ExitUsage
		}
		return report.ExitUsage
	}

	if opts.reportPath != "" && opts.reportPath != "-" {
		if err := writeFileAtomic(opts.reportPath, res); err != nil {
			fmt.Fprintf(stderr, "error writing report: %v\n", err)
		}
	}
	switch opts.format {
	case "text":
		report.WriteText(stdout, res)
	default:
		_ = report.WriteJSON(stdout, res)
	}
	return report.ExitCode(res)
}

// writeFileAtomic writes the JSON report via a temp file + rename so a crash
// mid-write never leaves a truncated report in place. The temp file is
// created next to the destination so the rename stays on one filesystem.
func writeFileAtomic(path string, res *runner.RunResult) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".e2e-report-*.json")
	if err != nil {
		return err
	}
	tmpName := f.Name()
	encErr := report.WriteJSON(f, res)
	if cerr := f.Close(); encErr == nil {
		encErr = cerr
	}
	if encErr != nil {
		os.Remove(tmpName)
		return encErr
	}
	return os.Rename(tmpName, path)
}

// csvFlag is a repeatable, comma-separated string-list flag.
type csvFlag []string

func (f *csvFlag) String() string { return strings.Join(*f, ",") }

func (f *csvFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*f = append(*f, part)
		}
	}
	return nil
}
