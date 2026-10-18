// Package lessons captures the corrections a user gives Claude ("don't mock
// the database", "from now on use table tests") so they can become rules
// instead of being forgotten next session.
//
// Detection is deterministic and conservative: a false positive is noise in
// someone's rules, a miss costs one more correction. Lessons are appended to
// a local JSONL log; nothing leaves the machine.
package lessons

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileName is the log's name inside the plugin data directory.
const FileName = "lessons.jsonl"

// MaxPromptRunes bounds how much of a prompt is stored.
const MaxPromptRunes = 2000

// Lesson is one correction the user gave Claude.
type Lesson struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Session  string    `json:"session"`
	Repo     string    `json:"repo"`
	Prompt   string    `json:"prompt"`
	Signals  []string  `json:"signals"`
	Explicit bool      `json:"explicit"`
}

// ID returns a short stable hash of repo and the normalized prompt, so the
// same correction in the same repo has the same ID.
func ID(repo, prompt string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(prompt)), " ")
	sum := sha256.Sum256([]byte(repo + "\x00" + norm))
	return hex.EncodeToString(sum[:5])
}

// Record appends l to dataDir/lessons.jsonl. It fills the ID and time when
// empty, truncates the prompt, and skips a lesson whose ID was already
// recorded in the same session.
func Record(dataDir string, l Lesson) error {
	l.Prompt = truncate(strings.TrimSpace(l.Prompt), MaxPromptRunes)
	if l.ID == "" {
		l.ID = ID(l.Repo, l.Prompt)
	}
	if l.Time.IsZero() {
		l.Time = time.Now().UTC()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	existing, _, err := Load(dataDir)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.ID == l.ID && e.Session == l.Session {
			return nil
		}
	}
	data, _ := json.Marshal(l) // a Lesson always marshals
	f, err := os.OpenFile(filepath.Join(dataDir, FileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	return errors.Join(werr, f.Close())
}

// Load reads every recorded lesson, skipping and counting malformed lines.
// A missing log is not an error.
func Load(dataDir string) ([]Lesson, int, error) {
	raw, err := readLines(dataDir)
	if err != nil {
		return nil, 0, err
	}
	var (
		out       []Lesson
		malformed int
	)
	for _, line := range raw {
		l, ok := parse(line)
		if !ok {
			malformed++
			continue
		}
		out = append(out, l)
	}
	return out, malformed, nil
}

// Pending counts the lessons recorded for repo.
func Pending(dataDir, repo string) (int, error) {
	ls, _, err := Load(dataDir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range ls {
		if l.Repo == repo {
			n++
		}
	}
	return n, nil
}

// Clear removes lessons for repo ("" for every repo): those with the given
// IDs, or all of them when ids is empty. Malformed lines are kept. The log
// is rewritten atomically. It returns how many lessons were removed.
func Clear(dataDir, repo string, ids []string) (int, error) {
	raw, err := readLines(dataDir)
	if err != nil || len(raw) == 0 {
		return 0, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var (
		keep    strings.Builder
		removed int
	)
	for _, line := range raw {
		if l, ok := parse(line); ok && (repo == "" || l.Repo == repo) && (len(ids) == 0 || want[l.ID]) {
			removed++
			continue
		}
		keep.WriteString(line + "\n")
	}
	if removed == 0 {
		return 0, nil
	}
	path := filepath.Join(dataDir, FileName)
	tmp, err := createTemp(dataDir, FileName+".*")
	if err != nil {
		return 0, err
	}
	_, werr := tmp.WriteString(keep.String())
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	if err := rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	return removed, nil
}

// Nudge is the context Claude gets when a correction is recorded. An empty
// ID is derived the way Record derives it.
func Nudge(l Lesson) string {
	if l.ID == "" {
		l.ID = ID(l.Repo, truncate(strings.TrimSpace(l.Prompt), MaxPromptRunes))
	}
	lead := "guardrails: recorded this correction as a candidate rule (lesson " + l.ID + ")."
	if l.Explicit {
		lead = "guardrails: this sounds like a standing rule for this repo; recorded it as lesson " + l.ID + "."
	}
	return lead + " Follow it for the rest of this session. The user can run /guardrails:learn to turn recorded corrections into .claude/rules so future sessions know it too."
}

// Seams for the atomic rewrite's failure paths.
var (
	createTemp = os.CreateTemp
	rename     = os.Rename
)

func readLines(dataDir string) ([]string, error) {
	f, err := os.Open(filepath.Join(dataDir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

func parse(line string) (Lesson, bool) {
	var l Lesson
	if err := json.Unmarshal([]byte(line), &l); err != nil || l.ID == "" {
		return Lesson{}, false
	}
	return l, true
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
