package module

import (
	"reflect"
	"testing"

	"github.com/rossedman/guardrails/internal/feedback"
)

func TestParseLocations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  string
		want []feedback.Issue
	}{
		{name: "empty", out: "", want: nil},
		{
			name: "line and column",
			out:  "# example.com/x\n./x.go:3:5: undefined: foo\n",
			want: []feedback.Issue{{File: "./x.go", Line: 3, Col: 5, Rule: "vet", Message: "undefined: foo"}},
		},
		{
			name: "line only, vet prefix and no trailing newline",
			out:  "vet: pkg/y.go:10: something odd",
			want: []feedback.Issue{{File: "pkg/y.go", Line: 10, Rule: "vet", Message: "something odd"}},
		},
		{
			name: "windows drive letter",
			out:  `C:\src\z.go:1:2: bad`,
			want: []feedback.Issue{{File: `C:\src\z.go`, Line: 1, Col: 2, Rule: "vet", Message: "bad"}},
		},
		{
			name: "non-location lines ignored",
			out:  "ok  \texample.com/x\t0.1s\nFAIL\n\n",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseLocations(tc.out, "vet")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseLocations() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\n\nb", []string{"a", "", "b"}},
	}
	for _, tc := range tests {
		if got := splitLines(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitLines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
