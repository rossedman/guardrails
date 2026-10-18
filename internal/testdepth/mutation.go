package testdepth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rossedman/guardrails/internal/feedback"
)

// Mutant outcomes.
const (
	mutKilled = iota
	mutLived
	mutNotViable
	mutNotRun
)

// mutWorkers is the number of mutants tested in parallel.
var mutWorkers = func() int {
	return min(8, max(1, runtime.NumCPU()/2))
}

// mutPkg is one package's sites and baseline.
type mutPkg struct {
	dir      string
	rel      string
	sites    []mutSite
	baseline time.Duration
}

// Mutation mutates the changed lines of funcs and runs their package tests
// against each mutant; survivors mean the tests don't check the behavior.
func Mutation(ctx context.Context, e Env, funcs []Func) Result {
	var res Result
	cfg := e.Config.Go.Mutation
	now := e.Now
	if now == nil {
		now = time.Now
	}

	byDir := map[string]*mutPkg{}
	var dirs []string
	total := 0
	for _, f := range funcs {
		sites := mutSites(f)
		if len(sites) == 0 {
			continue
		}
		p := byDir[f.Dir]
		if p == nil {
			p = &mutPkg{dir: f.Dir, rel: mutPkgPattern(e.Root, f.Dir)}
			byDir[f.Dir] = p
			dirs = append(dirs, f.Dir)
		}
		p.sites = append(p.sites, sites...)
		total += len(sites)
	}
	if total == 0 {
		return res
	}
	sort.Strings(dirs)

	budget := time.Duration(cfg.BudgetSeconds) * time.Second
	sctx, cancel := Slice(ctx, budget)
	defer cancel()

	// Baselines: a package whose tests fail unmutated can't be scored.
	var pkgs []*mutPkg
	for _, d := range dirs {
		p := byDir[d]
		start := now()
		r, err := e.Runner.Run(sctx, e.Root, "go", "test", "-count=1", "-failfast", p.rel)
		p.baseline = now().Sub(start)
		if err != nil || r.ExitCode != 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("mutation: skipped %s; its tests do not pass unmutated", p.rel))
			continue
		}
		mutSort(p.sites)
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		return res
	}

	var all []mutSite
	timeouts := map[string]time.Duration{}
	rels := map[string]string{}
	var est time.Duration
	for _, p := range pkgs {
		all = append(all, p.sites...)
		timeouts[p.dir] = max(3*p.baseline, p.baseline+5*time.Second)
		rels[p.dir] = p.rel
		est += time.Duration(len(p.sites)) * max(p.baseline, time.Millisecond)
	}
	workers := mutWorkers()
	est /= time.Duration(workers)
	found := len(all)
	if budget > 0 && est > budget {
		keep := int(float64(len(all)) * float64(budget) / float64(est))
		all = mutSample(all, max(1, keep))
		res.Notes = append(res.Notes, fmt.Sprintf("mutation: sampled %d of %d mutants to fit the %s budget", len(all), found, budget))
	}

	tmp, err := os.MkdirTemp("", "guardrails-mutants-*")
	if err != nil {
		res.Notes = append(res.Notes, "mutation: skipped: "+err.Error())
		return res
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	sources := map[string][]byte{}
	for _, s := range all {
		if _, ok := sources[s.Func.File]; ok {
			continue
		}
		b, err := os.ReadFile(s.Func.File)
		if err != nil {
			res.Notes = append(res.Notes, "mutation: skipped: "+err.Error())
			return res
		}
		sources[s.Func.File] = b
	}

	outcomes := make([]int, len(all))
	for i := range outcomes {
		outcomes[i] = mutNotRun
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				s := all[i]
				outcomes[i] = mutRun(sctx, e, tmp, i, s, sources[s.Func.File], rels[s.Func.Dir], timeouts[s.Func.Dir])
			}
		}()
	}
launch:
	for i := range all {
		select {
		case <-sctx.Done():
			break launch
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()

	var killed, lived, ran int
	var survivors []string
	for i, o := range outcomes {
		switch o {
		case mutKilled:
			killed++
			ran++
		case mutLived:
			lived++
			ran++
			s := all[i]
			survivors = append(survivors, fmt.Sprintf("%s:%d  %s  %s, tests still pass", mutRel(e.Repo, s.Func.File), s.Line, s.Func.Name, s.Desc()))
		case mutNotViable:
			ran++
		}
	}
	if ran < len(all) {
		res.Notes = append(res.Notes, fmt.Sprintf("mutation: budget ran out after %d of %d mutants", ran, len(all)))
	}
	if ran == 0 {
		return res
	}
	res.Ran = append(res.Ran, "mutation")
	scored := killed + lived
	if scored == 0 {
		return res
	}
	score := float64(killed) * 100 / float64(scored)
	if score+1e-9 >= cfg.MinScore {
		return res
	}
	sort.Strings(survivors)
	res.Checks = append(res.Checks, feedback.Check{
		Name:    "mutation",
		Failed:  true,
		Summary: fmt.Sprintf("mutation score %.0f%% < %.0f%% (%d of %d mutants killed)", score, cfg.MinScore, killed, scored),
		Lines:   survivors,
		Fix:     "add assertions that tell the original behavior apart from each surviving mutant",
	})
	return res
}

// mutRun tests one mutant and classifies it.
func mutRun(ctx context.Context, e Env, tmp string, i int, s mutSite, src []byte, rel string, timeout time.Duration) int {
	if ctx.Err() != nil {
		return mutNotRun
	}
	dir := filepath.Join(tmp, fmt.Sprint(i))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return mutNotViable
	}
	mutant := filepath.Join(dir, filepath.Base(s.Func.File))
	overlay := filepath.Join(dir, "overlay.json")
	data, _ := json.Marshal(map[string]map[string]string{"Replace": {s.Func.File: mutant}})
	if os.WriteFile(mutant, mutApply(src, s), 0o600) != nil || os.WriteFile(overlay, data, 0o600) != nil {
		return mutNotViable
	}
	mctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := e.Runner.Run(mctx, e.Root, "go", "test", "-overlay="+overlay, "-count=1", "-failfast", rel)
	switch {
	case ctx.Err() != nil:
		return mutNotRun
	case errors.Is(err, context.DeadlineExceeded) || mctx.Err() != nil:
		return mutKilled
	case err != nil:
		return mutNotViable
	}
	out := r.Combined()
	if strings.Contains(out, "[build failed]") || strings.Contains(out, "setup failed") {
		return mutNotViable
	}
	if r.ExitCode == 0 {
		return mutLived
	}
	return mutKilled
}

// mutPkgPattern renders dir as a ./relative package pattern from root.
func mutPkgPattern(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return "."
	}
	return "./" + filepath.ToSlash(rel)
}

// mutRel renders path relative to repo for display.
func mutRel(repo, path string) string {
	if rel, err := filepath.Rel(repo, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
