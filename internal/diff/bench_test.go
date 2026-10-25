package diff

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkParseUnified(b *testing.B) {
	var sb strings.Builder
	for f := range 20 {
		fmt.Fprintf(&sb, "diff --git a/pkg/f%d.go b/pkg/f%d.go\nindex 1..2 100644\n--- a/pkg/f%d.go\n+++ b/pkg/f%d.go\n", f, f, f, f)
		for h := range 10 {
			fmt.Fprintf(&sb, "@@ -%d,4 +%d,6 @@ func f()\n context\n-old\n+new1\n+new2\n context\n+added\n context\n", h*20+1, h*20+1)
		}
	}
	in := sb.String()
	b.ReportAllocs()
	for b.Loop() {
		if got := ParseUnified(in); len(got) != 20 {
			b.Fatalf("parsed %d files", len(got))
		}
	}
}
