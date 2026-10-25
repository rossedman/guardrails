package config

import (
	"os"
	"path/filepath"
	"testing"
)

const benchRepoYAML = `version: 1
mode: warn
preset: strict
go:
  coverage:
    total: 95
    per_package_floor: 70
  lint:
    on_edit: fast
python:
  typecheck: mypy
  coverage:
    total: 85
docker:
  enabled: false
deps:
  vulnerabilities: deny
  min_age_days: 14
gate:
  max_consecutive_blocks: 5
  timeout_seconds: 120
guards:
  allow_suppression_tag: allow-lint
`

func BenchmarkMerge(b *testing.B) {
	data := []byte(benchRepoYAML)
	b.ReportAllocs()
	for b.Loop() {
		cfg := Default()
		if err := Merge(&cfg, data); err != nil {
			b.Fatal(err)
		}
		if err := cfg.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoad(b *testing.B) {
	dir := b.TempDir()
	global := filepath.Join(dir, "global.yaml")
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("go:\n  coverage:\n    total: 85\ngate:\n  timeout_seconds: 60\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, RepoFile), []byte(benchRepoYAML), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Load(global, repo); err != nil {
			b.Fatal(err)
		}
	}
}

func FuzzMerge(f *testing.F) {
	f.Add(benchRepoYAML)
	f.Add("mode: warn\ngo:\n  coverage:\n    total: 90\n")
	f.Add("go:\n  enabled: false\n")
	f.Add("deps:\n  enabled: false\nlessons:\n  enabled: false\n")
	f.Add("unknown_key: 1\n")
	f.Add("mode: [")
	f.Add("")
	f.Fuzz(func(t *testing.T, data string) {
		once := Default()
		err := Merge(&once, []byte(data))

		// A failed merge must leave a config that is still usable.
		_ = once.Hash()
		_ = once.Validate()
		if err != nil {
			return
		}

		// Deterministic and idempotent: merging the same data again
		// changes nothing.
		again := Default()
		if err := Merge(&again, []byte(data)); err != nil || again.Hash() != once.Hash() {
			t.Fatalf("Merge is not deterministic for %q (err %v)", data, err)
		}
		if err := Merge(&again, []byte(data)); err != nil {
			t.Fatalf("second Merge of %q failed: %v", data, err)
		}
		if again.Hash() != once.Hash() {
			t.Fatalf("Merge is not idempotent for %q", data)
		}

		// Validation depends only on the merged values.
		if (once.Validate() == nil) != (again.Validate() == nil) {
			t.Fatalf("Validate disagrees after a repeated Merge of %q", data)
		}
	})
}
