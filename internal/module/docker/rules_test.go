package docker

import (
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
)

const pinned = "alpine:3.20@sha256:0000000000000000000000000000000000000000000000000000000000000000"

// want is an expected issue: rule id, line and a message substring.
type want struct {
	rule string
	line int
	msg  string
}

func issuesOf(checks []feedback.Check) []feedback.Issue {
	var out []feedback.Issue
	for _, c := range checks {
		out = append(out, c.Issues...)
	}
	return out
}

func TestLint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		content   string
		want      []want
		configure func(*config.Docker)
	}{
		{
			name:    "clean pinned non-root image",
			content: "FROM " + pinned + "\nRUN apk add --no-cache curl\nCOPY app /app\nUSER app\n",
		},
		{
			name:    "unpinned base and missing USER",
			content: "FROM alpine:3.20\nRUN echo\n",
			want: []want{
				{"GR-DOCKER-001", 1, "FROM alpine:3.20 is not pinned by digest"},
				{"GR-DOCKER-002", 1, "sets no USER"},
			},
		},
		{
			name:    "root user",
			content: "FROM " + pinned + "\nUSER app\nUSER root\n",
			want:    []want{{"GR-DOCKER-002", 3, "runs as USER root"}},
		},
		{
			name:    "uid 0 with group",
			content: "FROM " + pinned + "\nUSER 0:0\n",
			want:    []want{{"GR-DOCKER-002", 2, "runs as USER 0:0"}},
		},
		{
			name: "multi-stage: scratch and stage refs exempt, final user wins",
			content: "ARG GO=golang:1.26@sha256:" + strings.Repeat("a", 64) + "\n" +
				"FROM ${GO} AS build\nUSER root\nRUN go build\n" +
				"FROM scratch AS final\nCOPY --from=build /app /app\nUSER 65532:65532\n",
		},
		{
			name:    "build arg default is expanded",
			content: "ARG BASE=debian:12\nFROM $BASE\nUSER app\n",
			want:    []want{{"GR-DOCKER-001", 2, "FROM debian:12 is not pinned"}},
		},
		{
			name:    "build arg without default",
			content: "ARG BASE\nFROM ${BASE}\nUSER app\n",
			want:    []want{{"GR-DOCKER-001", 2, "uses a build arg without a default"}},
		},
		{
			name:    "final stage inherits USER from an earlier stage",
			content: "FROM " + pinned + " AS base\nUSER app\nFROM base\nRUN true\n",
		},
		{
			name:    "inherited root user",
			content: "FROM " + pinned + " AS base\nUSER root\nFROM base\n",
			want:    []want{{"GR-DOCKER-002", 2, "runs as USER root"}},
		},
		{
			name:    "distroless nonroot base needs no USER",
			content: "FROM gcr.io/distroless/static:nonroot@sha256:" + strings.Repeat("b", 64) + "\n",
		},
		{
			name:    "case-insensitive stage reference",
			content: "FROM " + pinned + " AS Builder\nFROM builder\n",
			want:    []want{{"GR-DOCKER-002", 2, "sets no USER"}},
		},
		{
			name:    "unresolved USER variable is trusted",
			content: "FROM " + pinned + "\nUSER ${UID}\n",
		},
		{
			name:      "tags are checked when digests are not required",
			content:   "FROM alpine\nFROM node:latest AS n\nFROM registry:5000/team/app\nFROM ghcr.io/x/y:1.2\nFROM $UNSET\nFROM a@sha256:" + strings.Repeat("c", 64) + "\nUSER app\n",
			configure: func(d *config.Docker) { d.RequireDigestPins = false },
			want: []want{
				{"GR-DOCKER-003", 1, "FROM alpine has no tag"},
				{"GR-DOCKER-003", 2, "FROM node:latest uses the latest tag"},
				{"GR-DOCKER-003", 3, "FROM registry:5000/team/app has no tag"},
			},
		},
		{
			name: "ADD of local files",
			content: "FROM " + pinned + "\nADD . /src\nADD --chown=app a.txt b.txt /dst/\n" +
				"ADD https://example.com/x.tgz /x\nADD app.tar.gz /opt\nADD git@github.com:a/b.git /b\n" +
				"ADD [\"a b\", \"/c\"]\nADD <<EOF /f\nhi\nEOF\nADD onlyone\nUSER app\n",
			want: []want{
				{"GR-DOCKER-004", 2, "ADD . /src copies local files"},
				{"GR-DOCKER-004", 3, "ADD a.txt b.txt /dst/"},
			},
		},
		{
			name: "apt hygiene",
			content: "FROM " + pinned + "\n" +
				"RUN apt-get update && apt-get install -y curl\n" +
				"RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*\n" +
				"RUN --mount=type=cache,target=/var/cache/apt apt-get -y install --no-install-recommends git\n" +
				"RUN apt install -y vim\nUSER app\n",
			want: []want{
				{"GR-DOCKER-005", 2, "--no-install-recommends"},
				{"GR-DOCKER-005", 5, "--no-install-recommends"},
				{"GR-DOCKER-006", 2, "/var/lib/apt/lists"},
				{"GR-DOCKER-006", 5, "/var/lib/apt/lists"},
			},
		},
		{
			name: "apk cache",
			content: "FROM " + pinned + "\nRUN apk add curl\nRUN apk --update add git\nRUN apk add --no-cache jq\n" +
				"RUN --mount=type=cache,target=/var/cache/apk apk add make\nUSER app\n",
			want: []want{
				{"GR-DOCKER-007", 2, "--no-cache"},
				{"GR-DOCKER-007", 3, "--no-cache"},
			},
		},
		{
			name: "secrets in ARG and ENV",
			content: "ARG GITHUB_TOKEN\nFROM " + pinned + "\nARG NPM_TOKEN=x VERSION=1\nENV DB_PASSWORD=hunter2\n" +
				"ENV API_KEY abc\nENV PASSWORD_FILE=/run/secrets/pw TOKEN_PATH=/t\nENV MSG=\"a b\" app_secret=1\nARG\nENV\nUSER app\n",
			want: []want{
				{"GR-DOCKER-008", 1, "ARG GITHUB_TOKEN looks like a secret"},
				{"GR-DOCKER-008", 3, "ARG NPM_TOKEN"},
				{"GR-DOCKER-008", 4, "ENV DB_PASSWORD"},
				{"GR-DOCKER-008", 5, "ENV API_KEY"},
				{"GR-DOCKER-008", 7, "ENV app_secret"},
			},
		},
		{
			name:    "pins and non-root can be turned off",
			content: "FROM alpine:3.20\n",
			configure: func(d *config.Docker) {
				d.RequireDigestPins, d.RequireNonRoot = false, false
			},
		},
		{
			name:    "no FROM at all",
			content: "# just a comment\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default().Docker
			if tc.configure != nil {
				tc.configure(&cfg)
			}
			checks := Lint(cfg, "svc/Dockerfile", tc.content)
			got := issuesOf(checks)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d issues, want %d:\n%+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				is := got[i]
				if is.Rule != w.rule || is.Line != w.line || !strings.Contains(is.Message, w.msg) || is.File != "svc/Dockerfile" {
					t.Errorf("issue %d = %+v, want %+v", i, is, w)
				}
			}
			for _, c := range checks {
				if !c.Failed || c.Fix == "" || c.Summary == "" {
					t.Errorf("malformed check %+v", c)
				}
			}
		})
	}
}

func TestRulesAreWellFormed(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i, r := range Rules {
		wantID := "GR-DOCKER-00" + string(rune('1'+i))
		if r.ID != wantID {
			t.Errorf("rule %d id = %s, want %s", i, r.ID, wantID)
		}
		if seen[r.Check] || r.Check == "" || r.Fix == "" || r.Enabled == nil || r.Find == nil {
			t.Errorf("malformed rule %+v", r)
		}
		seen[r.Check] = true
	}
}

func TestSecretName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"GITHUB_TOKEN": true, "db_password": true, "PASSWD": true, "AWS_SECRET_ACCESS_KEY": true,
		"APIKEY": true, "PRIVATE_KEY": true, "GOOGLE_CREDENTIALS": true,
		"TOKEN_FILE": false, "SECRET_PATH": false, "SECRETS_DIR": false, "VERSION": false, "PORT": false,
	} {
		if got := secretName(name); got != want {
			t.Errorf("secretName(%q) = %v, want %v", name, got, want)
		}
	}
}
