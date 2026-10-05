package runner

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestFile is the file name that marks a directory as a scenario.
const ManifestFile = "scenario.json"

// Discover scans root recursively for scenario manifests and returns the
// scenarios sorted by name, so the discovered set is deterministic.
func Discover(root string) ([]Scenario, error) {
	var scenarios []Scenario
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir // skip .git and friends
			}
			return nil
		}
		if d.Name() != ManifestFile {
			return nil
		}
		s, err := loadScenario(root, path)
		if err != nil {
			return err
		}
		scenarios = append(scenarios, s)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover scenarios under %s: %w", root, err)
	}
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].Name < scenarios[j].Name })
	return scenarios, nil
}

// loadScenario reads and validates one manifest file.
func loadScenario(root, path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Scenario{}, fmt.Errorf("parse %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return Scenario{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	name := m.Name
	if name == "" {
		name = filepath.ToSlash(rel)
	}
	if m.Command == "" {
		return Scenario{}, fmt.Errorf("scenario %q (%s): command is required", name, path)
	}
	retries := -1 // -1 = inherit from config
	if m.Retries != nil {
		retries = *m.Retries
	}
	return Scenario{
		Name:    name,
		Dir:     dir,
		Command: m.Command,
		Timeout: m.Timeout,
		Retries: retries,
		Skip:    m.Skip,
		Tags:    m.Tags,
	}, nil
}
