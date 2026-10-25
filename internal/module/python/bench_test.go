package python

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkParseMypy(b *testing.B) {
	var sb strings.Builder
	for i := range 500 {
		fmt.Fprintf(&sb, "src/pkg/mod%d.py:%d:%d: error: Incompatible return value type (got \"int\", expected \"str\")  [return-value]\n", i%25, i+1, i%40+1)
		if i%5 == 0 {
			fmt.Fprintf(&sb, "src/pkg/mod%d.py:%d: note: Revealed type is \"builtins.int\"\n", i%25, i+1)
		}
	}
	sb.WriteString("Found 500 errors in 25 files (checked 80 source files)\n")
	in := sb.String()
	b.ReportAllocs()
	for b.Loop() {
		if got := parseMypy(in); len(got) != 500 {
			b.Fatalf("parsed %d issues", len(got))
		}
	}
}
