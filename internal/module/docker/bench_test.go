package docker

import (
	"strings"
	"testing"
)

const benchDockerfile = `# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/app ./cmd/app && \
    # strip the binary
    strip /out/app
RUN <<EOF
set -e
echo done
EOF

FROM gcr.io/distroless/static@sha256:0000000000000000000000000000000000000000000000000000000000000000
USER 65532:65532
COPY --from=build /out/app /app
HEALTHCHECK CMD ["/app", "health"]
ENTRYPOINT ["/app"]
`

func BenchmarkParse(b *testing.B) {
	in := strings.Repeat(benchDockerfile, 20)
	b.ReportAllocs()
	for b.Loop() {
		if len(Parse(in)) == 0 {
			b.Fatal("no instructions")
		}
	}
}

func BenchmarkParseFile(b *testing.B) {
	in := strings.Repeat(benchDockerfile, 20)
	b.ReportAllocs()
	for b.Loop() {
		if len(ParseFile(in).Stages) == 0 {
			b.Fatal("no stages")
		}
	}
}
