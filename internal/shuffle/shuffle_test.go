package shuffle

import (
	"testing"

	"github.com/Diudiuseven/e2e-test-runner-go/internal/scenario"
)

func names(ss []*scenario.Scenario) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}

func TestSeedDeterminism(t *testing.T) {
	in := make([]*scenario.Scenario, 10)
	for i := range in {
		in[i] = &scenario.Scenario{ManifestFile: scenario.ManifestFile{Name: string(rune('a' + i))}}
	}
	o1 := names(Order(in, 42))
	o2 := names(Order(in, 42))
	for i := range o1 {
		if o1[i] != o2[i] {
			t.Fatalf("same seed produced different orders:\n%v\n%v", o1, o2)
		}
	}
}

func TestDifferentSeedCanReorder(t *testing.T) {
	in := make([]*scenario.Scenario, 20)
	for i := range in {
		in[i] = &scenario.Scenario{ManifestFile: scenario.ManifestFile{Name: string(rune('a' + i))}}
	}
	foundDiff := false
	for seed := uint64(1); seed < 20; seed++ {
		o := names(Order(in, seed))
		for i := range o {
			if o[i] != in[i].Name {
				foundDiff = true
				break
			}
		}
	}
	if !foundDiff {
		t.Fatal("no seed produced a different order")
	}
}

func TestOrderPreservesSetAndInput(t *testing.T) {
	in := []*scenario.Scenario{
		{ManifestFile: scenario.ManifestFile{Name: "a"}},
		{ManifestFile: scenario.ManifestFile{Name: "b"}},
		{ManifestFile: scenario.ManifestFile{Name: "c"}},
	}
	out := Order(in, 99)
	if len(out) != 3 {
		t.Fatalf("set size changed: %d", len(out))
	}
	present := map[string]bool{}
	for _, s := range out {
		present[s.Name] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !present[want] {
			t.Fatalf("scenario %q missing after shuffle", want)
		}
	}
	// Input slice must remain in its original order.
	if in[0].Name != "a" || in[1].Name != "b" || in[2].Name != "c" {
		t.Fatalf("input slice was mutated: %v", names(in))
	}
}
