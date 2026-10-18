// Package insights summarizes the gate history in runs.jsonl: how often
// Claude passes the Stop gate first time, which rules it trips most, which
// checks time out, and which rules recur often enough to belong in a
// .claude/rules file.
package insights

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rossedman/guardrails/internal/runlog"
)

// Options filter and tune the report.
type Options struct {
	// Days is the window; records older than Now-Days are ignored.
	Days int
	// Repo limits the report to one repository; empty means all.
	Repo string
	// MinSessions is how many distinct sessions a rule must trip in to
	// become a rule candidate.
	MinSessions int
	// Now is the clock.
	Now time.Time
}

// Report is the summary of a window of gate history.
type Report struct {
	Since     time.Time `json:"since"`
	Repo      string    `json:"repo,omitempty"`
	Malformed int       `json:"malformed_lines,omitempty"`

	Sessions  int `json:"sessions"`
	GateRuns  int `json:"gate_runs"`
	Turns     int `json:"turns"`
	FirstPass int `json:"first_pass_turns"`
	// FirstPassRate is FirstPass/Turns, the success metric: it should trend
	// up as rules teach Claude to meet the bar the first time.
	FirstPassRate  float64 `json:"first_pass_rate"`
	BlocksPerTurn  float64 `json:"blocks_per_turn"`
	HandOffs       int     `json:"hand_offs"`
	Timeouts       int     `json:"timeouts"`
	GateMillisP50  int64   `json:"gate_ms_p50"`
	GateMillisP95  int64   `json:"gate_ms_p95"`
	TopRules       []Rule  `json:"top_rules"`
	RuleCandidates []Rule  `json:"rule_candidates"`
}

// Rule is one check+rule pair and how often it tripped.
type Rule struct {
	Check    string   `json:"check"`
	Rule     string   `json:"rule,omitempty"`
	Count    int      `json:"count"`
	Sessions int      `json:"sessions"`
	Examples []string `json:"examples,omitempty"`
	// RulesFile is the .claude/rules file an addition belongs in, e.g.
	// "go.md"; empty when the language is unknown.
	RulesFile string `json:"rules_file,omitempty"`
}

// Name renders "check/rule", or just the check when there is no rule.
func (r Rule) Name() string {
	if r.Rule == "" {
		return r.Check
	}
	return r.Check + "/" + r.Rule
}

// Load reads dir/runs.jsonl. A missing log is empty, not an error.
func Load(dir string) ([]runlog.Record, int, error) {
	f, err := os.Open(filepath.Join(dir, runlog.FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse reads JSONL records, skipping and counting malformed lines.
func Parse(r io.Reader) ([]runlog.Record, int, error) {
	var (
		recs      []runlog.Record
		malformed int
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec runlog.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Type == "" {
			malformed++
			continue
		}
		recs = append(recs, rec)
	}
	return recs, malformed, sc.Err()
}

// Summarize computes the report for recs under opts.
func Summarize(recs []runlog.Record, opts Options) Report {
	if opts.Days <= 0 {
		opts.Days = 30
	}
	if opts.MinSessions <= 0 {
		opts.MinSessions = 5
	}
	since := opts.Now.Add(-time.Duration(opts.Days) * 24 * time.Hour)
	rep := Report{Since: since, Repo: opts.Repo, TopRules: []Rule{}, RuleCandidates: []Rule{}}

	var gates []runlog.Record
	sessions := map[string]bool{}
	for _, r := range recs {
		if r.At.Before(since) || (opts.Repo != "" && r.Repo != opts.Repo) {
			continue
		}
		sessions[r.Session] = true
		if r.Type == "gate" {
			gates = append(gates, r)
		}
	}
	rep.Sessions = len(sessions)
	rep.GateRuns = len(gates)
	sort.SliceStable(gates, func(i, j int) bool { return gates[i].At.Before(gates[j].At) })

	turns(gates, &rep)
	rep.GateMillisP50, rep.GateMillisP95 = percentiles(gates)
	rules(gates, opts.MinSessions, &rep)
	return rep
}

// turns groups each session's gate runs (per agent) into turns: runs up to
// and including a pass or a hand-off.
func turns(gates []runlog.Record, rep *Report) {
	type key struct{ session, agent string }
	open := map[key]int{} // gates so far in the current turn
	blocks := 0
	for _, g := range gates {
		k := key{g.Session, g.AgentType}
		open[k]++
		for _, f := range g.Failures {
			if f.Check == "timeout" {
				rep.Timeouts++
				break
			}
		}
		switch {
		case g.Passed:
			rep.Turns++
			if open[k] == 1 {
				rep.FirstPass++
			}
			delete(open, k)
		case g.HandedOff:
			rep.Turns++
			rep.HandOffs++
			delete(open, k)
		default:
			blocks++
		}
	}
	if rep.Turns > 0 {
		rep.FirstPassRate = float64(rep.FirstPass) / float64(rep.Turns)
		rep.BlocksPerTurn = float64(blocks) / float64(rep.Turns)
	}
}

func percentiles(gates []runlog.Record) (p50, p95 int64) {
	if len(gates) == 0 {
		return 0, 0
	}
	ms := make([]int64, len(gates))
	for i, g := range gates {
		ms[i] = g.Millis
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	at := func(p float64) int64 {
		i := int(p*float64(len(ms))+0.5) - 1
		return ms[min(max(i, 0), len(ms)-1)]
	}
	return at(0.50), at(0.95)
}

func rules(gates []runlog.Record, minSessions int, rep *Report) {
	type agg struct {
		Rule
		sessions map[string]bool
		files    map[string]int
		modules  map[string]int
	}
	byKey := map[string]*agg{}
	for _, g := range gates {
		for _, f := range g.Failures {
			if f.Check == "timeout" {
				continue
			}
			k := f.Check + "\x00" + f.Rule
			a := byKey[k]
			if a == nil {
				a = &agg{Rule: Rule{Check: f.Check, Rule: f.Rule}, sessions: map[string]bool{}, files: map[string]int{}, modules: map[string]int{}}
				byKey[k] = a
			}
			a.Count++
			a.sessions[g.Session] = true
			if f.File != "" {
				a.files[f.File]++
			}
			for _, m := range g.Modules {
				a.modules[m]++
			}
		}
	}
	all := make([]Rule, 0, len(byKey))
	for _, a := range byKey {
		r := a.Rule
		r.Sessions = len(a.sessions)
		r.Examples = topKeys(a.files, 3)
		r.RulesFile = rulesFile(a.files, a.modules)
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Sessions != all[j].Sessions {
			return all[i].Sessions > all[j].Sessions
		}
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		return all[i].Name() < all[j].Name()
	})
	rep.TopRules = append(rep.TopRules, all[:min(10, len(all))]...)
	for _, r := range all {
		if r.Sessions >= minSessions {
			rep.RuleCandidates = append(rep.RuleCandidates, r)
		}
	}
}

// topKeys returns up to n keys with the highest counts, ties by name.
func topKeys(m map[string]int, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys[:min(n, len(keys))]
}

// rulesFile picks the rules file for the language most failing files are
// in, falling back to the only module that ran.
func rulesFile(files, modules map[string]int) string {
	votes := map[string]int{}
	for f, n := range files {
		if lang := Language(f); lang != "" {
			votes[lang] += n
		}
	}
	if len(votes) == 0 && len(modules) == 1 {
		for m := range modules {
			votes[m] = 1
		}
	}
	if best := topKeys(votes, 1); len(best) == 1 {
		return best[0] + ".md"
	}
	return ""
}

// Language maps a file to the module (and rules file) that owns it.
func Language(path string) string {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "Dockerfile") || strings.HasPrefix(base, "Containerfile") ||
		strings.HasSuffix(strings.ToLower(base), ".dockerfile") {
		return "docker"
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".sh", ".bash", ".ksh":
		return "shell"
	case ".yml", ".yaml":
		return "yaml"
	case ".tf", ".tfvars":
		return "terraform"
	}
	return ""
}
