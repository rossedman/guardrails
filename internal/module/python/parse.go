package python

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rossedman/guardrails/internal/feedback"
)

type ruffIssue struct {
	Code     *string `json:"code"`
	Message  string  `json:"message"`
	Filename string  `json:"filename"`
	Location struct {
		Row    int `json:"row"`
		Column int `json:"column"`
	} `json:"location"`
}

// parseRuff reads `ruff check --output-format=json` output.
func parseRuff(out string) ([]feedback.Issue, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, fmt.Errorf("empty ruff output")
	}
	var raw []ruffIssue
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse ruff output: %w", err)
	}
	issues := make([]feedback.Issue, 0, len(raw))
	for _, r := range raw {
		rule := "syntax"
		if r.Code != nil && *r.Code != "" {
			rule = *r.Code
		}
		issues = append(issues, feedback.Issue{
			File: r.Filename, Line: r.Location.Row, Col: r.Location.Column,
			Rule: rule, Message: r.Message,
		})
	}
	return issues, nil
}

var mypyRe = regexp.MustCompile(`^(.+?\.pyi?):(\d+)(?::(\d+))?: error: (.*?)(?:  \[([a-z0-9-]+)\])?$`)

// parseMypy reads mypy's one-line-per-error output; notes are dropped.
func parseMypy(out string) []feedback.Issue {
	var issues []feedback.Issue
	for _, l := range strings.Split(out, "\n") {
		m := mypyRe.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if m == nil {
			continue
		}
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		rule := m[5]
		if rule == "" {
			rule = "mypy"
		}
		issues = append(issues, feedback.Issue{File: m[1], Line: line, Col: col, Rule: rule, Message: m[4]})
	}
	return issues
}

type pyrightReport struct {
	GeneralDiagnostics []struct {
		File     string `json:"file"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Rule     string `json:"rule"`
		Range    struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	} `json:"generalDiagnostics"`
}

// parsePyright reads `pyright --outputjson`, keeping errors. Pyright
// positions are 0-based.
func parsePyright(out string) ([]feedback.Issue, error) {
	var rep pyrightReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rep); err != nil {
		return nil, fmt.Errorf("parse pyright output: %w", err)
	}
	var issues []feedback.Issue
	for _, d := range rep.GeneralDiagnostics {
		if d.Severity != "error" {
			continue
		}
		rule := d.Rule
		if rule == "" {
			rule = "pyright"
		}
		issues = append(issues, feedback.Issue{
			File: d.File, Line: d.Range.Start.Line + 1, Col: d.Range.Start.Character + 1,
			Rule: rule, Message: strings.SplitN(d.Message, "\n", 2)[0],
		})
	}
	return issues, nil
}

// covReport is the subset of coverage.py's JSON report Guardrails reads.
type covReport struct {
	Files map[string]covFile `json:"files"`
}

type covFile struct {
	ExecutedLines []int `json:"executed_lines"`
	MissingLines  []int `json:"missing_lines"`
	Summary       struct {
		CoveredLines  int `json:"covered_lines"`
		NumStatements int `json:"num_statements"`
	} `json:"summary"`
}

// parseCoverage decodes the JSON report written by pytest-cov.
func parseCoverage(data []byte) (*covReport, error) {
	var r covReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse coverage report: %w", err)
	}
	return &r, nil
}

var failedRe = regexp.MustCompile(`^(FAILED|ERROR) (\S+)`)

// failedTests extracts test ids from pytest's short summary (-rfE).
func failedTests(out string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if m := failedRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil && !seen[m[2]] {
			seen[m[2]] = true
			ids = append(ids, m[2])
		}
	}
	return ids
}
