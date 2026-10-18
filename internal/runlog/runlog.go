// Package runlog appends gate and session records to a JSONL log, the raw
// material for failure analytics.
package runlog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// FileName is the log's name inside the plugin data directory.
const FileName = "runs.jsonl"

// Record is one log line. Type is "gate" or "session".
type Record struct {
	Type      string    `json:"type"`
	At        time.Time `json:"at"`
	Session   string    `json:"session"`
	Repo      string    `json:"repo"`
	Event     string    `json:"event,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	Passed    bool      `json:"passed,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	HandedOff bool      `json:"handed_off,omitempty"`
	Failures  []Failure `json:"failures,omitempty"`
	Coverage  float64   `json:"coverage,omitempty"`
	// Modules names the language modules that ran in a gate, so failures
	// without a file can still be attributed to a language.
	Modules []string `json:"modules,omitempty"`
	Millis  int64    `json:"ms,omitempty"`
	// Session records.
	Reason  string `json:"reason,omitempty"`
	Gates   int    `json:"gates,omitempty"`
	Blocked int    `json:"blocked,omitempty"`
	Touched int    `json:"touched,omitempty"`
}

// Failure is one failing check, flattened for analytics.
type Failure struct {
	Check string `json:"check"`
	Rule  string `json:"rule,omitempty"`
	File  string `json:"file,omitempty"`
}

// Append writes rec as one line to dir/runs.jsonl.
func Append(dir string, rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	return errors.Join(werr, f.Close())
}
