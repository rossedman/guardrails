package deps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/hook"
)

var now = time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)

func daysAgo(n int) string { return now.Add(-time.Duration(n) * 24 * time.Hour).Format(time.RFC3339) }

// fakeAPI serves deps.dev and OSV responses from maps keyed by
// "SYSTEM/name" and "SYSTEM/name/version".
type fakeAPI struct {
	mu       sync.Mutex
	packages map[string]string
	versions map[string]string
	osv      map[string]string // name → response body
	status   int               // non-zero: every response has this status
	osvDown  bool
	delay    time.Duration
	hits     int
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits++
	status, delay, osvDown := f.status, f.delay, f.osvDown
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if r.URL.Path == "/v1/query" {
		if osvDown {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var q struct {
			Package struct{ Name string } `json:"package"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		body := f.osv[q.Package.Name]
		if body == "" {
			body = `{}`
		}
		_, _ = w.Write([]byte(body))
		return
	}
	// /v3/systems/{SYS}/packages/{name}[/versions/{v}]
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/v3/systems/"), "/")
	for i := range parts {
		parts[i], _ = url.PathUnescape(parts[i])
	}
	key := parts[0] + "/" + parts[2]
	var body string
	if len(parts) == 5 {
		body = f.versions[key+"/"+parts[4]]
	} else {
		body = f.packages[key]
	}
	if body == "" {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte(body))
}

func pkgJSON(versions ...string) string {
	return `{"versions":[` + strings.Join(versions, ",") + `]}`
}

func ver(v string, age int, def bool) string {
	return `{"versionKey":{"version":"` + v + `"},"publishedAt":"` + daysAgo(age) + `","isDefault":` + boolStr(def) + `}`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func newFake() *fakeAPI {
	return &fakeAPI{
		packages: map[string]string{
			"NPM/lodash":                pkgJSON(ver("4.17.20", 2000, false), ver("4.17.21", 1500, true)),
			"NPM/lodahs":                pkgJSON(ver("0.0.1", 10, true)),
			"NPM/lodashh":               pkgJSON(ver("1.0.0", 900, true)),
			"NPM/fresh-pkg":             pkgJSON(ver("0.1.0", 3, true)),
			"NPM/old-lib":               pkgJSON(ver("1.0.0", 900, false), `{"versionKey":{"version":"2.0.0"},"publishedAt":"`+daysAgo(1)+`","isDeprecated":true}`),
			"NPM/gpl-lib":               pkgJSON(ver("1.0.0", 900, true)),
			"NPM/dual-lib":              pkgJSON(ver("1.0.0", 900, true)),
			"NPM/nolicense":             pkgJSON(ver("1.0.0", 900, true)),
			"NPM/vuln-lib":              pkgJSON(ver("1.0.0", 900, true)),
			"NPM/many-vulns":            pkgJSON(ver("1.0.0", 900, true)),
			"NPM/mal-version":           pkgJSON(ver("1.0.0", 900, true)),
			"NPM/noversiondoc":          pkgJSON(ver("1.0.0", 900, true)),
			"NPM/empty":                 `{"versions":[]}`,
			"GO/github.com/spf13/cobra": pkgJSON(ver("v1.10.2", 300, true)),
			"PYPI/requests":             pkgJSON(ver("2.19.0", 3000, false), ver("2.32.0", 100, true)),
			"CARGO/serde":               pkgJSON(ver("1.0.0", 3000, true)),
		},
		versions: map[string]string{
			"NPM/lodash/4.17.21":                `{"publishedAt":"` + daysAgo(1500) + `","licenses":["MIT"]}`,
			"NPM/lodash/4.17.20":                `{"publishedAt":"` + daysAgo(2000) + `","licenses":["MIT"]}`,
			"NPM/lodashh/1.0.0":                 `{"licenses":["MIT"]}`,
			"NPM/fresh-pkg/0.1.0":               `{"publishedAt":"` + daysAgo(3) + `","licenses":["MIT"]}`,
			"NPM/old-lib/2.0.0":                 `{"publishedAt":"` + daysAgo(1) + `","licenses":["MIT"]}`,
			"NPM/gpl-lib/1.0.0":                 `{"licenses":["GPL-3.0-only"]}`,
			"NPM/dual-lib/1.0.0":                `{"licenses":["(GPL-3.0-only OR MIT)"]}`,
			"NPM/nolicense/1.0.0":               `{}`,
			"NPM/vuln-lib/1.0.0":                `{"licenses":["MIT"],"advisoryKeys":[{"id":"GHSA-1"}]}`,
			"NPM/many-vulns/1.0.0":              `{"advisoryKeys":[{"id":"GHSA-1"},{"id":"GHSA-2"},{"id":"GHSA-3"},{"id":"GHSA-4"}]}`,
			"NPM/mal-version/1.0.0":             `{"advisoryKeys":[{"id":"MAL-2026-1"}]}`,
			"NPM/empty/1.0.0":                   `{}`,
			"GO/github.com/spf13/cobra/v1.10.2": `{"licenses":["Apache-2.0"]}`,
			"PYPI/requests/2.19.0":              `{"licenses":["Apache-2.0"],"advisoryKeys":[{"id":"GHSA-r"}]}`,
			"PYPI/requests/2.32.0":              `{"licenses":["Apache-2.0"]}`,
			"CARGO/serde/1.0.0":                 `{"licenses":["MIT OR Apache-2.0"]}`,
		},
		osv: map[string]string{
			"lodahs": `{"vulns":[{"id":"MAL-2025-1"},{"id":"GHSA-x"}]}`,
		},
	}
}

func newTestChecker(t *testing.T, f *fakeAPI) *Checker {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := NewChecker(t.TempDir())
	c.DepsDevURL, c.OSVURL = srv.URL, srv.URL
	c.Now = func() time.Time { return now }
	c.Getenv = func(k string) string {
		if k == "GOPRIVATE" {
			return " ,go.acme.dev/*"
		}
		return ""
	}
	return c
}

func check(t *testing.T, c *Checker, cfg config.Deps, r Request) Finding {
	t.Helper()
	fs := c.Check(context.Background(), cfg, []Request{r})
	if len(fs) != 1 || fs[0].Request != r {
		t.Fatalf("Check = %+v", fs)
	}
	return fs[0]
}

func TestCheck(t *testing.T) {
	t.Parallel()
	c := newTestChecker(t, newFake())
	def := config.Default().Deps
	withCfg := func(fn func(*config.Deps)) config.Deps {
		cfg := def
		fn(&cfg)
		return cfg
	}
	npm := func(name, version string) Request { return Request{System: NPM, Name: name, Version: version} }
	tests := []struct {
		name   string
		cfg    config.Deps
		req    Request
		action string
		reason string
	}{
		{"popular, latest", def, npm("lodash", ""), hook.Allow, ""},
		{"range resolves", def, npm("lodash", "^4.17.20"), hook.Allow, ""},
		{"wide range uses default", def, npm("lodash", ">=4 <5"), hook.Allow, ""},
		{"missing range uses default", def, npm("lodash", "^4.99.0"), hook.Allow, ""},
		{"missing exact version", def, npm("lodash", "4.99.0"), hook.Ask, `lodash has no version "4.99.0" on npm`},
		{"missing pinned pypi version", def, Request{System: PyPI, Name: "requests", Version: "==9.9.9"}, hook.Ask, "has no version"},
		{"doesn't exist", def, npm("lodasj", ""), hook.Deny, `"lodasj" doesn't exist on npm — did you mean "lodash"?`},
		{"doesn't exist, no suggestion", def, npm("zz-no-such-thing-at-all", ""), hook.Deny, `doesn't exist on npm. Agents invent`},
		{"doesn't exist, private registry", def, Request{System: NPM, Name: "internal-thing", Private: true}, hook.Allow, ""},
		{"doesn't exist on go, GOPRIVATE", def, Request{System: Go, Name: "go.acme.dev/platform/log"}, hook.Allow, ""},
		{"doesn't exist on go", def, Request{System: Go, Name: "github.com/spf13/cobar"}, hook.Deny, "the Go module proxy"},
		{"doesn't exist on crates", def, Request{System: Cargo, Name: "serdee"}, hook.Deny, "crates.io"},
		{"no versions", def, npm("empty", ""), hook.Deny, "doesn't exist"},
		{"malicious", def, npm("lodahs", ""), hook.Deny, `"lodahs" is flagged as malicious (MAL-2025-1) — it imitates "lodash"`},
		{"malicious, allow-listed", withCfg(func(d *config.Deps) { d.Allow = []string{"loda*"} }), npm("lodahs", ""), hook.Allow, ""},
		{"malicious version", def, npm("mal-version", ""), hook.Deny, "mal-version@1.0.0 is flagged as malicious (MAL-2026-1)"},
		{"typosquat", def, npm("lodashh", ""), hook.Ask, `"lodashh" is a near miss of the popular "lodash"`},
		{"typosquat, private", def, Request{System: NPM, Name: "lodashh", Private: true}, hook.Allow, ""},
		{"young package", def, npm("fresh-pkg", ""), hook.Ask, `"fresh-pkg" was first published 3 days ago, under the 30-day minimum`},
		{"young package, check off", withCfg(func(d *config.Deps) { d.MinAgeDays = 0 }), npm("fresh-pkg", ""), hook.Allow, ""},
		{"young version", withCfg(func(d *config.Deps) { d.MinVersionAgeDays = 7 }), npm("fresh-pkg", ""), hook.Ask, "fresh-pkg@0.1.0 was published 3 days ago, under the 7-day minimum"},
		{"deprecated, one day old", withCfg(func(d *config.Deps) { d.MinVersionAgeDays = 2 }), npm("old-lib", "2.0.0"), hook.Ask, "old-lib@2.0.0 was published 1 day ago, under the 2-day minimum (deps.min_version_age_days); new releases are how hijacked packages spread. old-lib@2.0.0 is deprecated."},
		{"latest by date when no default", def, npm("old-lib", ""), hook.Ask, "old-lib@2.0.0 is deprecated"},
		{"license denied", withCfg(func(d *config.Deps) { d.Licenses.Deny = []string{"gpl-3.0-only"} }), npm("gpl-lib", ""), hook.Deny, "gpl-lib@1.0.0 is licensed GPL-3.0-only, which deps.licenses.deny forbids."},
		{"license not allowed", withCfg(func(d *config.Deps) { d.Licenses.Allow = []string{"MIT"} }), npm("gpl-lib", ""), hook.Ask, "which isn't in deps.licenses.allow"},
		{"dual license, one allowed", withCfg(func(d *config.Deps) { d.Licenses.Allow = []string{"MIT"} }), npm("dual-lib", ""), hook.Allow, ""},
		{"dual license, one denied", withCfg(func(d *config.Deps) { d.Licenses.Deny = []string{"GPL-3.0-only"} }), npm("dual-lib", ""), hook.Allow, ""},
		{"unknown license, allow list", withCfg(func(d *config.Deps) { d.Licenses.Allow = []string{"MIT"} }), npm("nolicense", ""), hook.Ask, "has no license deps.dev recognizes"},
		{"unknown license, deny list", withCfg(func(d *config.Deps) { d.Licenses.Deny = []string{"GPL-3.0-only"} }), npm("nolicense", ""), hook.Allow, ""},
		{"vulnerable, ask", def, npm("vuln-lib", ""), hook.Ask, "vuln-lib@1.0.0 has known vulnerabilities (GHSA-1); prefer a fixed version."},
		{"vulnerable, deny", withCfg(func(d *config.Deps) { d.Vulnerabilities = "deny" }), npm("vuln-lib", ""), hook.Deny, "GHSA-1"},
		{"vulnerable, allow", withCfg(func(d *config.Deps) { d.Vulnerabilities = "allow" }), npm("vuln-lib", ""), hook.Allow, ""},
		{"many vulnerabilities", def, npm("many-vulns", ""), hook.Ask, "(GHSA-1, GHSA-2, GHSA-3 and 1 more)"},
		{"go package within module", def, Request{System: Go, Name: "github.com/spf13/cobra/doc", Version: "v1.10.2"}, hook.Allow, ""},
		{"pypi pinned vulnerable", def, Request{System: PyPI, Name: "requests", Version: "2.19.0"}, hook.Ask, "requests@2.19.0 has known vulnerabilities (GHSA-r)"},
		{"cargo dual license allowed", withCfg(func(d *config.Deps) { d.Licenses.Allow = []string{"Apache-2.0"} }), Request{System: Cargo, Name: "serde"}, hook.Allow, ""},
		{"version doc missing", def, npm("noversiondoc", ""), hook.Ask, "couldn't reach deps.dev to check"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := check(t, c, tc.cfg, tc.req)
			if f.Action != tc.action || !strings.Contains(f.Reason, tc.reason) {
				t.Errorf("got %s %q\nwant %s containing %q", f.Action, f.Reason, tc.action, tc.reason)
			}
			if tc.action == hook.Allow && f.Reason != "" {
				t.Errorf("allowed with a reason: %q", f.Reason)
			}
		})
	}
}

func TestCheckAllowListSkipsLookups(t *testing.T) {
	t.Parallel()
	f := newFake()
	c := newTestChecker(t, f)
	cfg := config.Default().Deps
	cfg.Allow = []string{"@acme/*", "github.com/acme/**"}
	for _, r := range []Request{{System: NPM, Name: "@acme/ui"}, {System: Go, Name: "github.com/acme/x/y"}} {
		if got := check(t, c, cfg, r); got.Action != hook.Allow {
			t.Errorf("%s: %+v", r.Name, got)
		}
	}
	if f.hits != 0 {
		t.Errorf("allow-listed packages made %d requests", f.hits)
	}
}

func TestCheckUnreachable(t *testing.T) {
	t.Parallel()
	req := Request{System: NPM, Name: "lodash"}
	ask := config.Default().Deps
	allow := ask
	allow.Unreachable = "allow"

	f := newFake()
	f.status = http.StatusInternalServerError
	c := newTestChecker(t, f)
	c.CacheDir = ""
	if got := check(t, c, ask, req); got.Action != hook.Ask || !strings.Contains(got.Reason, `couldn't reach deps.dev to check "lodash" (GET 500 Internal Server Error)`) {
		t.Errorf("500: %+v", got)
	}
	if got := check(t, c, allow, req); got.Action != hook.Allow {
		t.Errorf("unreachable allow: %+v", got)
	}

	f2 := newFake()
	f2.osvDown = true
	c2 := newTestChecker(t, f2)
	if got := check(t, c2, ask, req); got.Action != hook.Ask || !strings.Contains(got.Reason, "couldn't reach OSV") {
		t.Errorf("OSV down: %+v", got)
	}

	f3 := newFake()
	f3.delay = 300 * time.Millisecond
	c3 := newTestChecker(t, f3)
	c3.Budget = 50 * time.Millisecond
	if got := check(t, c3, ask, req); got.Action != hook.Ask || !strings.Contains(got.Reason, "timed out") {
		t.Errorf("timeout: %+v", got)
	}

	c4 := newTestChecker(t, newFake())
	c4.DepsDevURL = "http://127.0.0.1:1"
	if got := check(t, c4, ask, req); got.Action != hook.Ask || !strings.Contains(got.Reason, "couldn't reach deps.dev") {
		t.Errorf("connection refused: %+v", got)
	}
	c4.DepsDevURL = "://bad"
	if got := check(t, c4, ask, req); got.Action != hook.Ask {
		t.Errorf("bad URL: %+v", got)
	}
}

func TestCheckBadJSON(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.packages["NPM/broken"] = `{"versions":`
	c := newTestChecker(t, f)
	if got := check(t, c, config.Default().Deps, Request{System: NPM, Name: "broken"}); got.Action != hook.Ask || !strings.Contains(got.Reason, "decode response") {
		t.Errorf("bad JSON: %+v", got)
	}
}

func TestCheckCache(t *testing.T) {
	t.Parallel()
	f := newFake()
	c := newTestChecker(t, f)
	cfg := config.Default().Deps
	req := Request{System: NPM, Name: "lodash"}
	check(t, c, cfg, req)
	first := f.hits
	if first == 0 {
		t.Fatal("no lookups")
	}
	check(t, c, cfg, req)
	if f.hits != first {
		t.Errorf("cached lookups hit the network: %d → %d", first, f.hits)
	}
	// Expired entries are fetched again.
	c.Now = func() time.Time { return now.Add(25 * time.Hour) }
	check(t, c, cfg, req)
	if f.hits == first {
		t.Error("expired cache was reused")
	}
	// A corrupt entry is ignored.
	entries, _ := filepath.Glob(filepath.Join(c.CacheDir, "*.json"))
	for _, e := range entries {
		if err := os.WriteFile(e, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := check(t, c, cfg, req); got.Action != hook.Allow {
		t.Errorf("corrupt cache: %+v", got)
	}
}

func TestCheckManyConcurrently(t *testing.T) {
	t.Parallel()
	c := newTestChecker(t, newFake())
	c.Budget = 0 // the default
	reqs := []Request{{System: NPM, Name: "lodash"}, {System: NPM, Name: "lodasj"}, {System: Cargo, Name: "serde"}}
	got := c.Check(context.Background(), config.Default().Deps, reqs)
	if len(got) != 3 || got[0].Action != hook.Allow || got[1].Action != hook.Deny || got[2].Action != hook.Allow {
		t.Errorf("Check = %+v", got)
	}
}

func TestNewCheckerDefaults(t *testing.T) {
	t.Parallel()
	c := NewChecker("")
	if c.DepsDevURL != "https://api.deps.dev" || c.OSVURL != "https://api.osv.dev" || c.Budget != DefaultBudget || c.Getenv == nil || c.HTTP == nil {
		t.Errorf("NewChecker = %+v", c)
	}
	c.Now = nil
	if c.ageDays(time.Now().Add(-49*time.Hour)) != 2 || c.ageDays(time.Time{}) != -1 {
		t.Error("ageDays")
	}
	c.Getenv = nil
	if c.goPrivate("go.acme.dev/x") {
		t.Error("no env means nothing is private")
	}
}

func TestGoModuleCandidates(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"github.com/a/b/c/d/e/f":    {"github.com/a/b/c/d/e/f", "github.com/a/b/c/d/e", "github.com/a/b/c/d", "github.com/a/b/c"},
		"github.com/a/b":            {"github.com/a/b"},
		"golang.org/x/tools/cmd/gi": {"golang.org/x/tools/cmd/gi", "golang.org/x/tools/cmd", "golang.org/x/tools", "golang.org/x"},
		"honnef.co/go/tools":        {"honnef.co/go/tools", "honnef.co/go", "honnef.co"},
	}
	for in, want := range tests {
		if got := goModuleCandidates(in); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("goModuleCandidates(%q) = %q", in, got)
		}
	}
}

func TestLicenseVerdict(t *testing.T) {
	t.Parallel()
	mit := config.Licenses{Allow: []string{"MIT", "Apache-2.0"}}
	tests := []struct {
		policy   config.Licenses
		licenses []string
		want     string
	}{
		{config.Licenses{}, []string{"GPL-3.0-only"}, hook.Allow},
		{mit, []string{"MIT", "Apache-2.0"}, hook.Allow},
		{mit, []string{"MIT", "BSD-3-Clause"}, hook.Ask},
		{mit, []string{"Apache-2.0 AND MIT"}, hook.Allow},
		{mit, []string{"Apache-2.0 WITH LLVM-exception"}, hook.Ask},
		{config.Licenses{Deny: []string{"AGPL-3.0-only"}}, []string{"MIT OR AGPL-3.0-only"}, hook.Allow},
		{config.Licenses{Deny: []string{"AGPL-3.0-only"}}, []string{"MIT AND AGPL-3.0-only"}, hook.Deny},
		{config.Licenses{Deny: []string{"AGPL-3.0-only"}}, []string{"()"}, hook.Allow},
	}
	for _, tc := range tests {
		if got, _ := licenseVerdict(tc.policy, tc.licenses); got != tc.want {
			t.Errorf("licenseVerdict(%+v, %q) = %s, want %s", tc.policy, tc.licenses, got, tc.want)
		}
	}
}

func TestNames(t *testing.T) {
	t.Parallel()
	for _, s := range []string{NPM, PyPI, Go, Cargo} {
		if depsDevSystem(s) == "" || osvEcosystem(s) == "" || registryName(s) == "" {
			t.Errorf("%s has no names", s)
		}
	}
	if dayCount(1) != "1 day" || dayCount(2) != "2 days" {
		t.Error("dayCount")
	}
	if escape("@types/node") != "%40types%2Fnode" || escape("a b") != "a%20b" {
		t.Errorf("escape = %q", escape("@types/node"))
	}
}
