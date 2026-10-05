package filter

import (
	"context"
	"testing"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

func mk(name string, tags []string, skip bool, reason string) *scenario.Scenario {
	return &scenario.Scenario{
		ManifestFile: scenario.ManifestFile{Name: name, Tags: tags, Skip: skip, SkipReason: reason},
	}
}

func TestClassify(t *testing.T) {
	scenarios := []*scenario.Scenario{
		mk("a", nil, false, ""),
		mk("b", nil, true, "broken"),
		mk("c", []string{"smoke"}, false, ""),
		mk("d", []string{"slow"}, false, ""),
	}
	sel := Selector{
		Names:    []string{"a"},
		Tags:     []string{"smoke"},
		Excludes: []string{"a"},
	}
	ds := Apply(context.Background(), scenarios, sel)

	want := map[string]scenario.Disposition{
		"a": scenario.DispExcluded, // exclude wins even though named
		"b": scenario.DispSkipped,
		"c": scenario.DispNone,      // matches tag
		"d": scenario.DispUnmatched, // no name/tag match
	}
	for _, d := range ds {
		if d.Disposition != want[d.Scenario.Name] {
			t.Errorf("%s: want %q got %q (reason=%q)",
				d.Scenario.Name, want[d.Scenario.Name], d.Disposition, d.Reason)
		}
		if d.Disposition != scenario.DispNone && d.Reason == "" {
			t.Errorf("%s: disposition %q must carry a reason", d.Scenario.Name, d.Disposition)
		}
	}
}

func TestNoSelectorMeansAllIncluded(t *testing.T) {
	scenarios := []*scenario.Scenario{mk("a", []string{"z"}, false, ""), mk("b", nil, false, "")}
	ds := Apply(context.Background(), scenarios, Selector{})
	for _, d := range ds {
		if !d.Included() {
			t.Errorf("%s unexpectedly not included", d.Scenario.Name)
		}
	}
}

func TestExcludeBeatsSkipBeatsUnmatched(t *testing.T) {
	s := mk("a", nil, true, "why")
	d := classify(s, Selector{Excludes: []string{"a"}, Names: []string{"zzz"}})
	if d.Disposition != scenario.DispExcluded {
		t.Fatalf("exclude must win, got %q", d.Disposition)
	}
	d = classify(s, Selector{Names: []string{"zzz"}})
	if d.Disposition != scenario.DispSkipped {
		t.Fatalf("skip beats unmatched, got %q", d.Disposition)
	}
}
