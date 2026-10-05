package runner

import (
	"testing"
)

func TestDiscoverFindsAndSortsScenarios(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "b-second", `{"command":"true"}`, "")
	writeScenario(t, root, "a-first", `{"command":"true"}`, "")

	scenarios, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 2 {
		t.Fatalf("want 2 scenarios, got %d", len(scenarios))
	}
	if scenarios[0].Name != "a-first" || scenarios[1].Name != "b-second" {
		t.Fatalf("scenarios not sorted by name: %v", scenarios)
	}
}

func TestDiscoverDefaultsNameToRelativeDir(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "nested/deep", `{"command":"true"}`, "")

	scenarios, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 || scenarios[0].Name != "nested/deep" {
		t.Fatalf("unexpected scenarios: %+v", scenarios)
	}
}

func TestDiscoverRejectsMissingCommand(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "bad", `{"name":"bad"}`, "")

	if _, err := Discover(root); err == nil {
		t.Fatal("expected error for manifest without command")
	}
}

func TestClassifyPrecedence(t *testing.T) {
	cfg := Config{Include: []string{"keep-*"}, Exclude: []string{"keep-but-no"}}
	cases := []struct {
		name string
		s    Scenario
		want Status
	}{
		{"skip wins over filters", Scenario{Name: "keep-but-no", Skip: true}, StatusSkipped},
		{"excluded", Scenario{Name: "keep-but-no"}, StatusExcluded},
		{"unmatched", Scenario{Name: "other"}, StatusUnmatched},
		{"runs", Scenario{Name: "keep-me"}, ""},
		{"tag match", Scenario{Name: "x", Tags: []string{"keep-me"}}, ""},
	}
	for _, c := range cases {
		if got := classify(c.s, cfg); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
