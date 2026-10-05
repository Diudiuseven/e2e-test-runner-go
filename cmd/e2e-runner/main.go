// Command e2e-runner discovers and runs end-to-end test scenarios.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/runner"
)

// strList is a repeatable -include / -exclude flag.
type strList []string

func (s *strList) String() string { return strings.Join(*s, ",") }
func (s *strList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	os.Exit(realMain())
}

func realMain() int {
	var cfg runner.Config
	var include, exclude strList

	fs := flag.NewFlagSet("e2e-runner", flag.ContinueOnError)
	fs.StringVar(&cfg.Root, "root", "scenarios", "directory scanned for scenarios")
	fs.Var(&include, "include", "name/tag (glob) to include; repeatable, empty = all")
	fs.Var(&exclude, "exclude", "name/tag (glob) to exclude; repeatable")
	fs.IntVar(&cfg.Parallel, "parallel", 4, "max scenarios running concurrently")
	fs.IntVar(&cfg.Retries, "retries", 0, "default retries per scenario (scenario may override)")
	fs.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "default per-scenario timeout")
	fs.DurationVar(&cfg.Deadline, "deadline", 0, "overall run deadline (0 = none)")
	fs.Int64Var(&cfg.Seed, "seed", 1, "shuffle seed")
	fs.BoolVar(&cfg.Shuffle, "shuffle", false, "randomize execution order (deterministic per seed)")
	fs.StringVar(&cfg.OutputFile, "output", "", "write JSON report to this file (default: stdout)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return runner.ExitUsage
	}
	cfg.Include = include
	cfg.Exclude = exclude

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	results, runErr := runner.Run(ctx, cfg)
	elapsed := time.Since(start)
	interrupted := runErr != nil

	rep := runner.BuildReport(results, cfg, elapsed, interrupted)

	out := os.Stdout
	if cfg.OutputFile != "" {
		f, err := os.Create(cfg.OutputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot write report: %v\n", err)
			return runner.ExitUsage
		}
		defer f.Close()
		out = f
	}
	if err := runner.WriteReport(out, rep); err != nil {
		fmt.Fprintf(os.Stderr, "cannot encode report: %v\n", err)
		return runner.ExitUsage
	}
	runner.PrintHuman(os.Stderr, rep)

	return runner.ExitCode(results, interrupted)
}
