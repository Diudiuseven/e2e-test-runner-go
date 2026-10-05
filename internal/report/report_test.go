package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/runner"
	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

func mkResult(name string, status scenario.Status) *runner.ScenarioResult {
	return &runner.ScenarioResult{Name: name, Status: status}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		res  *runner.RunResult
		want int
	}{
		{"all pass", &runner.RunResult{Results: []*runner.ScenarioResult{
			mkResult("a", scenario.StatusPassed), mkResult("b", scenario.StatusFlaky),
			mkResult("c", scenario.StatusSkipped), mkResult("d", scenario.StatusUnmatched),
		}}, ExitOK},
		{"failure", &runner.RunResult{Results: []*runner.ScenarioResult{
			mkResult("a", scenario.StatusFailed)}}, ExitFailed},
		{"crash", &runner.RunResult{Results: []*runner.ScenarioResult{
			mkResult("a", scenario.StatusCrashed)}}, ExitFailed},
		{"timeout", &runner.RunResult{Results: []*runner.ScenarioResult{
			mkResult("a", scenario.StatusTimeout)}}, ExitFailed},
		{"cancel beats failure", &runner.RunResult{
			Canceled: true,
			Results:  []*runner.ScenarioResult{mkResult("a", scenario.StatusFailed)},
		}, ExitInterrupted},
		{"deadline beats failure", &runner.RunResult{
			Deadline: true,
			Results:  []*runner.ScenarioResult{mkResult("a", scenario.StatusTimeout)},
		}, ExitInterrupted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.res); got != tc.want {
				t.Fatalf("want %d got %d", tc.want, got)
			}
		})
	}
}

func TestCount(t *testing.T) {
	res := &runner.RunResult{Results: []*runner.ScenarioResult{
		mkResult("1", scenario.StatusPassed),
		mkResult("2", scenario.StatusPassed),
		mkResult("3", scenario.StatusFlaky),
		mkResult("4", scenario.StatusFailed),
		mkResult("5", scenario.StatusCrashed),
		mkResult("6", scenario.StatusTimeout),
		mkResult("7", scenario.StatusCanceled),
		mkResult("8", scenario.StatusSkipped),
		mkResult("9", scenario.StatusExcluded),
		mkResult("10", scenario.StatusUnmatched),
		mkResult("11", scenario.StatusNotExecuted),
	}}
	c := Count(res)
	if c.Passed != 2 || c.Flaky != 1 || c.Failed != 1 || c.Crashed != 1 ||
		c.Timeout != 1 || c.Canceled != 1 || c.Skipped != 1 ||
		c.Excluded != 1 || c.Unmatched != 1 || c.NotExecuted != 1 {
		t.Fatalf("unexpected counts: %+v", c)
	}
}

func TestWriteJSONRoundTrip(t *testing.T) {
	res := &runner.RunResult{
		Results: []*runner.ScenarioResult{
			{Name: "a", Status: scenario.StatusFlaky, Retries: 1,
				Attempts: []scenario.Attempt{
					{Index: 0, Status: scenario.StatusFailed, ExitCode: 1},
					{Index: 1, Status: scenario.StatusPassed, ExitCode: 0},
				}},
		},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, res); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if _, ok := got["results"]; !ok {
		t.Fatal("report missing results key")
	}
}
