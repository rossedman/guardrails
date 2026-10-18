// Package state persists per-session and per-repo data between the
// short-lived hook processes.
//
// Claude Code runs matching hooks in parallel, so every read-modify-write
// goes through Update, which holds a per-session lock file.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Session is everything Guardrails remembers about one Claude session.
type Session struct {
	ID string `json:"id"`
	// Repo is the repository root the session works in.
	Repo string `json:"repo,omitempty"`
	// BaseCommit is HEAD at SessionStart; patch coverage diffs against it.
	BaseCommit string `json:"base_commit,omitempty"`
	// Touched is the set of absolute file paths edited this session.
	Touched map[string]bool `json:"touched,omitempty"`
	// AgentTouched holds the files each subagent edited, keyed by agent id.
	AgentTouched map[string]map[string]bool `json:"agent_touched,omitempty"`
	// StopBlocks counts consecutive Stop gate blocks.
	StopBlocks int `json:"stop_blocks"`
	// AgentBlocks counts consecutive SubagentStop blocks per agent id.
	AgentBlocks map[string]int `json:"agent_blocks,omitempty"`
	// Baseline maps package import path to coverage % at SessionStart.
	Baseline map[string]float64 `json:"baseline,omitempty"`
	// Reported holds fingerprints of issues already shown to Claude.
	Reported map[string]bool `json:"reported,omitempty"`
	// Asked holds borderline test-depth requirements already raised; each
	// is raised once per session.
	Asked map[string]bool `json:"asked,omitempty"`
	// Gates counts Stop gate runs; Blocked counts the ones that blocked.
	Gates   int `json:"gates"`
	Blocked int `json:"blocked"`
	// HandedOff counts times the retry cap was hit.
	HandedOff int       `json:"handed_off"`
	Started   time.Time `json:"started"`
}

// Touch records path as edited, by the main agent or by the subagent with
// agentID.
func (s *Session) Touch(agentID, path string) {
	if s.Touched == nil {
		s.Touched = map[string]bool{}
	}
	s.Touched[path] = true
	if agentID == "" {
		return
	}
	if s.AgentTouched == nil {
		s.AgentTouched = map[string]map[string]bool{}
	}
	if s.AgentTouched[agentID] == nil {
		s.AgentTouched[agentID] = map[string]bool{}
	}
	s.AgentTouched[agentID][path] = true
}

// TouchedFiles returns the files touched this session, or only by the
// subagent with agentID when it is non-empty, sorted.
func (s *Session) TouchedFiles(agentID string) []string {
	set := s.Touched
	if agentID != "" {
		set = s.AgentTouched[agentID]
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Blocks returns the consecutive block counter for the main agent or a
// subagent.
func (s *Session) Blocks(agentID string) int {
	if agentID == "" {
		return s.StopBlocks
	}
	return s.AgentBlocks[agentID]
}

// SetBlocks sets the consecutive block counter for the main agent or a
// subagent.
func (s *Session) SetBlocks(agentID string, n int) {
	if agentID == "" {
		s.StopBlocks = n
		return
	}
	if s.AgentBlocks == nil {
		s.AgentBlocks = map[string]int{}
	}
	s.AgentBlocks[agentID] = n
}

// Store is rooted at the plugin data directory.
type Store struct {
	Root string
	// LockTimeout bounds how long Update waits for the session lock.
	LockTimeout time.Duration
}

// New returns a Store rooted at dir.
func New(dir string) *Store {
	return &Store{Root: dir, LockTimeout: 5 * time.Second}
}

// SessionDir is the directory holding one session's files; it doubles as
// the scratchpad for overflow reports.
func (s *Store) SessionDir(id string) string {
	return filepath.Join(s.Root, "sessions", safe(id))
}

func (s *Store) sessionFile(id string) string {
	return filepath.Join(s.SessionDir(id), "state.json")
}

// Load reads a session without locking. A missing session is returned
// empty, not as an error.
func (s *Store) Load(id string) (*Session, error) {
	sess := &Session{ID: id}
	data, err := os.ReadFile(s.sessionFile(id))
	if errors.Is(err, os.ErrNotExist) {
		return sess, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, sess); err != nil {
		return nil, fmt.Errorf("corrupt session state %s: %w", s.sessionFile(id), err)
	}
	return sess, nil
}

// Update loads the session under its lock, applies fn and saves the result.
// If fn returns an error nothing is written.
func (s *Store) Update(id string, fn func(*Session) error) (*Session, error) {
	dir := s.SessionDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	unlock, err := lock(filepath.Join(dir, "state.lock"), s.LockTimeout)
	if err != nil {
		return nil, err
	}
	defer unlock()

	sess, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	if err := fn(sess); err != nil {
		return nil, err
	}
	if err := writeJSON(s.sessionFile(id), sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// RepoCache is data about a repository that outlives sessions.
type RepoCache struct {
	// Coverage maps package import path to the last measured coverage %.
	Coverage map[string]float64 `json:"coverage,omitempty"`
	// LastGate summarizes the most recent gate run, for the status line.
	LastGate *GateSummary `json:"last_gate,omitempty"`
	// LastReport is the path of the most recent full gate report.
	LastReport string `json:"last_report,omitempty"`
}

// GateSummary is the status-line view of a gate run.
type GateSummary struct {
	Passed    bool      `json:"passed"`
	Coverage  float64   `json:"coverage"`
	Threshold float64   `json:"threshold"`
	Issues    int       `json:"issues"`
	At        time.Time `json:"at"`
}

func (s *Store) repoFile(repo string) string {
	return filepath.Join(s.Root, "repos", Key(repo)+".json")
}

// LoadRepo reads the cache for repo; a missing cache is empty.
func (s *Store) LoadRepo(repo string) (*RepoCache, error) {
	rc := &RepoCache{}
	data, err := os.ReadFile(s.repoFile(repo))
	if errors.Is(err, os.ErrNotExist) {
		return rc, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, rc); err != nil {
		return nil, fmt.Errorf("corrupt repo cache: %w", err)
	}
	return rc, nil
}

// SaveRepo writes the cache for repo.
func (s *Store) SaveRepo(repo string, rc *RepoCache) error {
	return writeJSON(s.repoFile(repo), rc)
}

// CacheGet reads a content-addressed cache entry into v. It reports
// whether the entry existed.
func (s *Store) CacheGet(key string, v any) bool {
	data, err := os.ReadFile(filepath.Join(s.Root, "cache", safe(key)+".json"))
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

// CachePut writes a content-addressed cache entry.
func (s *Store) CachePut(key string, v any) error {
	return writeJSON(filepath.Join(s.Root, "cache", safe(key)+".json"), v)
}

// Key hashes an arbitrary string into a short filesystem-safe key.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// writeJSON writes atomically via a temp file and rename.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// safe maps an id to a single path element. Ids that are already safe are
// kept readable; anything else is hashed.
func safe(id string) string {
	if id == "" {
		return "_"
	}
	for _, r := range id {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok || id == "." || id == ".." {
			return Key(id)
		}
	}
	return id
}
