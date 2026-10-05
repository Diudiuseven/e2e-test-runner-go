package scenario

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverSortedAndParsed(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "b"), `{
		"name":"b","command":["./b.sh"],"timeout":"250ms","retries":1,
		"idempotent":true,"tags":["x"]}`)
	writeManifest(t, filepath.Join(root, "a"), `{"name":"a","command":["./a.sh"]}`)

	got, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 scenarios, got %d", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("discovery not path-sorted: %q, %q", got[0].Name, got[1].Name)
	}
	if got[1].Timeout != 250*time.Millisecond {
		t.Fatalf("timeout parsed wrong: %v", got[1].Timeout)
	}
	if got[1].Dir != filepath.Join(root, "b") {
		t.Fatalf("dir not set: %q", got[1].Dir)
	}
}

func TestDiscoverErrors(t *testing.T) {
	cases := map[string]string{
		"no name":          `{"command":["./x.sh"]}`,
		"empty command":    `{"name":"x","command":[]}`,
		"bad timeout":      `{"name":"x","command":["./x.sh"],"timeout":"nope"}`,
		"negative retries": `{"name":"x","command":["./x.sh"],"retries":-1}`,
		"skip no reason":   `{"name":"x","command":["./x.sh"],"skip":true}`,
		"unknown field":    `{"name":"x","command":["./x.sh"],"bogus":1}`,
		"invalid json":     `{not json`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeManifest(t, filepath.Join(root, "s"), body)
			if _, err := Discover(context.Background(), root); err == nil {
				t.Fatal("expected discovery error")
			}
		})
	}
}

func TestDiscoverDuplicateNames(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "a"), `{"name":"dup","command":["./x.sh"]}`)
	writeManifest(t, filepath.Join(root, "b"), `{"name":"dup","command":["./x.sh"]}`)
	if _, err := Discover(context.Background(), root); err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestAttemptMarshalDurationMS(t *testing.T) {
	a := Attempt{Status: StatusPassed, Duration: 1500 * time.Millisecond}
	b, err := a.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if want := `"duration_ms":1500`; !contains(string(b), want) {
		t.Fatalf("marshaled JSON %s missing %s", b, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
