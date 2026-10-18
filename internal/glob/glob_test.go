package glob

import "testing"

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"**/mocks/**", "internal/mocks/m.go", true},
		{"**/mocks/**", "mocks/m.go", true},
		{"**/mocks/**", "mocks", true},
		{"**/mocks/**", "internal/mock/m.go", false},
		{"**/*.pb.go", "api/v1/svc.pb.go", true},
		{"**/*.pb.go", "svc.pb.go", true},
		{"**/*.pb.go", "svc.go", false},
		{"cmd/**/main.go", "cmd/app/main.go", true},
		{"cmd/**/main.go", "cmd/main.go", true},
		{"cmd/**/main.go", "cmd/a/b/main.go", true},
		{"cmd/**/main.go", "internal/cmd/app/main.go", false},
		{"kind-*", "kind-local", true},
		{"*-dev", "eu-dev", true},
		{"*-dev", "eu-prod", false},
		{"a/b", "a/b/c", false},
		{"a/b/c", "a/b", false},
		{"/a/b/", "a/b", true},
		{"", "", true},
		{"**", "", true},
		{"**", "any/thing", true},
		{"[", "x", false},
	}
	for _, tc := range tests {
		if got := Match(tc.pattern, tc.name); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestAny(t *testing.T) {
	t.Parallel()
	ps := []string{"**/*.pb.go", "cmd/**/main.go"}
	if !Any(ps, "cmd/x/main.go") || !Any(ps, "a.pb.go") || Any(ps, "a.go") || Any(nil, "a.go") {
		t.Error("Any returned wrong result")
	}
}
