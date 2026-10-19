package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// Preset names. An empty preset is strict.
const (
	PresetStrict   = "strict"
	PresetStandard = "standard"
	PresetAdopt    = "adopt"
)

// Presets lists the preset names, strictest first.
var Presets = []string{PresetStrict, PresetStandard, PresetAdopt}

func validPreset(name string) bool { return name == "" || slices.Contains(Presets, name) }

// ApplyPreset sets the coverage bar for a preset on cfg:
//
//   - strict: 90% coverage per language, 80% per Go package, 90% of the
//     lines changed this session. The defaults.
//   - standard: 80% coverage, 70% per Go package, 80% of changed lines.
//   - adopt: for existing repositories. No absolute coverage bar; coverage
//     may not drop (the ratchet) and 80% of changed lines must be covered.
//
// Every preset keeps the linters, guards and ratchet on.
func ApplyPreset(cfg *Config, name string) error {
	if !validPreset(name) {
		return fmt.Errorf("unknown preset %q (want one of %v)", name, Presets)
	}
	cfg.Preset = name
	switch name {
	case PresetStandard:
		setBar(cfg, 80, 70, 80)
	case PresetAdopt:
		setBar(cfg, 0, 0, 80)
	default:
		setBar(cfg, 90, 80, 90)
	}
	return nil
}

func setBar(cfg *Config, total, floor, patch float64) {
	cfg.Go.Coverage.Total, cfg.Go.Coverage.PerPackageFloor, cfg.Go.PatchCoverage = total, floor, patch
	cfg.Python.Coverage.Total, cfg.Python.PatchCoverage = total, patch
	cfg.Go.Coverage.Ratchet, cfg.Python.Coverage.Ratchet = true, true
}

// PresetSummary describes a preset in one line.
func PresetSummary(name string) string {
	switch name {
	case PresetStandard:
		return "standard: 80% coverage, 80% of changed lines"
	case PresetAdopt:
		return "adopt: coverage may not drop, 80% of changed lines"
	default:
		return "strict: 90% coverage, 90% of changed lines"
	}
}

// presetOf reads the preset key from the config files without applying
// them. The repo file's preset wins over the global file's.
func presetOf(paths ...string) (string, error) {
	preset := ""
	for _, p := range paths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		var peek struct {
			Preset string `yaml:"preset"`
		}
		// Errors surface when the file is merged, with the full message.
		if yaml.NewDecoder(bytes.NewReader(data)).Decode(&peek) == nil && peek.Preset != "" {
			preset = peek.Preset
		}
	}
	return preset, nil
}
