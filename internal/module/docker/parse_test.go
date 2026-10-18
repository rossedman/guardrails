package docker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []Instruction
	}{
		{
			name: "keywords are case-insensitive and comments skipped",
			in:   "# comment\nfrom alpine:3 as build\n\n  run echo hi\n",
			want: []Instruction{
				{Cmd: "FROM", Args: "alpine:3 as build", Line: 2},
				{Cmd: "RUN", Args: "echo hi", Line: 4},
			},
		},
		{
			name: "continuations are joined and inner comments dropped",
			in:   "RUN apt-get update && \\\n  # why\n  apt-get install -y curl\nUSER app",
			want: []Instruction{
				{Cmd: "RUN", Args: "apt-get update && apt-get install -y curl", Line: 1},
				{Cmd: "USER", Args: "app", Line: 4},
			},
		},
		{
			name: "escape directive switches the continuation character",
			in:   "# escape=`\nFROM mcr.microsoft.com/windows\nRUN dir C:\\ `\n  && echo ok\n",
			want: []Instruction{
				{Cmd: "FROM", Args: "mcr.microsoft.com/windows", Line: 2},
				{Cmd: "RUN", Args: `dir C:\ && echo ok`, Line: 3},
			},
		},
		{
			name: "directives after the first instruction are comments",
			in:   "FROM a\n# escape=`\nRUN x \\\n y",
			want: []Instruction{
				{Cmd: "FROM", Args: "a", Line: 1},
				{Cmd: "RUN", Args: "x y", Line: 3},
			},
		},
		{
			name: "heredoc bodies are skipped",
			in:   "FROM a\nRUN <<EOF\nUSER root\nFROM evil\nEOF\nUSER app\n",
			want: []Instruction{
				{Cmd: "FROM", Args: "a", Line: 1},
				{Cmd: "RUN", Args: "<<EOF", Line: 2},
				{Cmd: "USER", Args: "app", Line: 6},
			},
		},
		{
			name: "quoted and tab-stripped heredocs",
			in:   "FROM a\nCOPY <<-\"END\" /f\n\tUSER root\n\tEND\nRUN <<'X' <<Y\nx\nX\ny\nY\nUSER app",
			want: []Instruction{
				{Cmd: "FROM", Args: "a", Line: 1},
				{Cmd: "COPY", Args: `<<-"END" /f`, Line: 2},
				{Cmd: "RUN", Args: "<<'X' <<Y", Line: 5},
				{Cmd: "USER", Args: "app", Line: 10},
			},
		},
		{
			name: "CRLF line endings",
			in:   "FROM a\r\nUSER b\r\n",
			want: []Instruction{{Cmd: "FROM", Args: "a", Line: 1}, {Cmd: "USER", Args: "b", Line: 2}},
		},
		{name: "empty", in: "", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Parse(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse() =\n%#v\nwant\n%#v", got, tc.want)
			}
		})
	}
}

func TestParseFile(t *testing.T) {
	t.Parallel()
	df := ParseFile(`ARG BASE=golang:1.26 VERSION="1.2" EMPTY
FROM --platform=$BUILDPLATFORM ${BASE} AS Build
ARG INSIDE=x
RUN go build
FROM scratch
COPY --from=build /app /app
USER 65532
`)
	if want := map[string]string{"BASE": "golang:1.26", "VERSION": "1.2", "EMPTY": ""}; !reflect.DeepEqual(df.Args, want) {
		t.Errorf("Args = %v, want %v", df.Args, want)
	}
	if len(df.Stages) != 2 {
		t.Fatalf("stages = %d, want 2", len(df.Stages))
	}
	if s := df.Stages[0]; s.Image != "${BASE}" || s.Name != "Build" || s.Line != 2 || len(s.Instructions) != 2 {
		t.Errorf("stage 0 = %+v", s)
	}
	if s := df.Stages[1]; s.Image != "scratch" || s.Name != "" || len(s.Instructions) != 2 {
		t.Errorf("stage 1 = %+v", s)
	}
	if got := ParseFile("FROM\n"); len(got.Stages) != 1 || got.Stages[0].Image != "" {
		t.Errorf("bare FROM = %+v", got.Stages)
	}
}

func TestExpand(t *testing.T) {
	t.Parallel()
	args := map[string]string{"A": "alpine", "T": "3.20", "E": ""}
	tests := map[string]string{
		"$A:$T":           "alpine:3.20",
		"${A}:${T}":       "alpine:3.20",
		"${MISSING}":      "${MISSING}",
		"$MISSING":        "$MISSING",
		"${E}":            "${E}",
		"${E:-busybox}":   "busybox",
		"${MISSING-x}":    "x",
		"${A:-busybox}":   "alpine",
		"${A:+set}":       "set",
		"${MISSING:+set}": "",
		"plain":           "plain",
	}
	for in, want := range tests {
		if got := Expand(in, args); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"FROM a\nRUN b \\\n c\n",
		"# escape=`\nFROM a\nRUN b `\n c",
		"FROM a\nRUN <<EOF\nx\nEOF\n",
		"ARG X=${Y:-z}\nFROM $X AS s\nUSER 0:0",
		"\\\n\\\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ins := Parse(s)
		for _, in := range ins {
			if in.Line < 1 || in.Cmd != strings.ToUpper(in.Cmd) {
				t.Fatalf("bad instruction %+v", in)
			}
		}
		df := ParseFile(s)
		cfg := config.Default().Docker
		for _, c := range lintParsed(cfg, "Dockerfile", df) {
			if !c.Failed || len(c.Issues) == 0 {
				t.Fatalf("bad check %+v", c)
			}
		}
		cfg.RequireDigestPins = false
		_ = lintParsed(cfg, "Dockerfile", df)
		_ = Expand(s, df.Args)
	})
}
