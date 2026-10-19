package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPreset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                string
		total, floor, patch float64
	}{
		{"", 90, 80, 90},
		{PresetStrict, 90, 80, 90},
		{PresetStandard, 80, 70, 80},
		{PresetAdopt, 0, 0, 80},
	} {
		cfg := Default()
		cfg.Go.Coverage.Ratchet = false
		if err := ApplyPreset(&cfg, tc.name); err != nil {
			t.Fatal(err)
		}
		if cfg.Go.Coverage.Total != tc.total || cfg.Go.Coverage.PerPackageFloor != tc.floor || cfg.Go.PatchCoverage != tc.patch ||
			cfg.Python.Coverage.Total != tc.total || cfg.Python.PatchCoverage != tc.patch || !cfg.Go.Coverage.Ratchet || cfg.Preset != tc.name {
			t.Errorf("%q: %+v %+v", tc.name, cfg.Go, cfg.Python)
		}
		if !strings.HasPrefix(PresetSummary(tc.name), map[string]string{"": "strict"}[tc.name]+tc.name) {
			t.Errorf("summary %q = %q", tc.name, PresetSummary(tc.name))
		}
	}
	cfg := Default()
	if err := ApplyPreset(&cfg, "lax"); err == nil {
		t.Error("unknown preset accepted")
	}
	cfg.Preset = "lax"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "preset") {
		t.Errorf("Validate = %v", err)
	}
}

func TestLoadPresetLayers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	global := filepath.Join(dir, "global.yaml")
	repo := filepath.Join(dir, "repo")
	write(t, global, "preset: standard\ngo:\n  patch_coverage: 85\n")
	write(t, filepath.Join(repo, RepoFile), "version: 1\n")

	cfg, err := Load(global, repo)
	if err != nil {
		t.Fatal(err)
	}
	// The global preset applies, and the global file's own keys win over it.
	if cfg.Preset != PresetStandard || cfg.Go.Coverage.Total != 80 || cfg.Go.PatchCoverage != 85 {
		t.Errorf("global preset: %+v", cfg.Go)
	}

	// The repo's preset wins over the global one; global keys still apply.
	write(t, filepath.Join(repo, RepoFile), "version: 1\npreset: adopt\npython:\n  patch_coverage: 70\n")
	cfg, err = Load(global, repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Preset != PresetAdopt || cfg.Go.Coverage.Total != 0 || cfg.Go.PatchCoverage != 85 || cfg.Python.PatchCoverage != 70 {
		t.Errorf("repo preset: %+v %+v", cfg.Go, cfg.Python)
	}

	write(t, filepath.Join(repo, RepoFile), "preset: lax\n")
	if _, err := Load(global, repo); err == nil || !strings.Contains(err.Error(), "lax") {
		t.Errorf("bad preset: %v", err)
	}
	// A file that isn't valid YAML is reported by the merge, not the peek.
	write(t, filepath.Join(repo, RepoFile), "preset: [\n")
	if _, err := Load(global, repo); err == nil || !strings.Contains(err.Error(), RepoFile) {
		t.Errorf("bad yaml: %v", err)
	}
	// A path that can't be read (here, a directory) is an error.
	if _, err := Load(dir, ""); err == nil {
		t.Error("unreadable global config accepted")
	}
}
