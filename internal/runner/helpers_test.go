package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// writeScenario creates a scenario directory with a manifest and a run.sh.
func writeScenario(t *testing.T, root, name, manifestJSON, script string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(manifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// byName indexes results by scenario name.
func byName(results []Result) map[string]Result {
	m := make(map[string]Result, len(results))
	for _, r := range results {
		m[r.Name] = r
	}
	return m
}
