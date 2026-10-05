package runner

import (
	"context"
	"fmt"
	"testing"
)

// TestConcurrencyStress runs many heterogeneous scenarios under high
// concurrency and asserts conclusions are identical to a serial run.
func TestConcurrencyStress(t *testing.T) {
	var fs []fixture
	for i := 0; i < 24; i++ {
		switch i % 4 {
		case 0:
			fs = append(fs, simplePass(fmt.Sprintf("p%d", i)))
		case 1:
			fs = append(fs, simpleFail(fmt.Sprintf("f%d", i)))
		case 2:
			fs = append(fs, fixture{name: fmt.Sprintf("c%d", i), script: "kill -9 $$"})
		default:
			fs = append(fs, fixture{
				name:   fmt.Sprintf("flk%d", i),
				script: `[ "$E2E_RUNNER_ATTEMPT" = "0" ] && exit 1; exit 0`,
				manifest: fmt.Sprintf(
					`{"name":"flk%d","command":["./run.sh"],"retries":1,"idempotent":true}`, i),
			})
		}
	}
	root := buildRoot(t, fs)

	serial, err := New(Config{Root: root, Concurrency: 1}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []int{4, 8, 16} {
		got, err := New(Config{Root: root, Concurrency: c}).Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range got.Results {
			s := serial.Results[i]
			if r.Name != s.Name || r.Status != s.Status || r.Retries != s.Retries ||
				len(r.Attempts) != len(s.Attempts) {
				t.Fatalf("concurrency %d differs at slot %d:\n%+v\nvs\n%+v",
					c, i, r, s)
			}
		}
	}
}
