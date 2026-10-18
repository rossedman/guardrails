package deps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/glob"
	"github.com/rossedman/guardrails/internal/hook"
)

// Finding is the verdict on one Request.
type Finding struct {
	Request Request
	// Action is hook.Allow, hook.Ask or hook.Deny.
	Action string
	// Reason is shown to Claude; empty when allowed.
	Reason string
}

// Checker looks packages up on deps.dev and OSV.
type Checker struct {
	HTTP *http.Client
	// DepsDevURL and OSVURL are the API roots, replaceable in tests.
	DepsDevURL string
	OSVURL     string
	// CacheDir holds lookups for a day; "" disables the cache.
	CacheDir string
	Now      func() time.Time
	// Budget bounds all lookups together; past it, deps.unreachable
	// decides.
	Budget time.Duration
	// Getenv reads GOPRIVATE and GONOSUMDB.
	Getenv func(string) string
}

// DefaultBudget leaves headroom in the PreToolUse hook's timeout.
const DefaultBudget = 4 * time.Second

// cacheTTL is how long lookups are reused.
const cacheTTL = 24 * time.Hour

// NewChecker returns a Checker for the public APIs, caching in cacheDir.
func NewChecker(cacheDir string) *Checker {
	return &Checker{
		HTTP:       &http.Client{},
		DepsDevURL: "https://api.deps.dev",
		OSVURL:     "https://api.osv.dev",
		CacheDir:   cacheDir,
		Now:        time.Now,
		Budget:     DefaultBudget,
		Getenv:     os.Getenv,
	}
}

// Check returns one finding per request, in order. Requests are checked
// concurrently within the budget.
func (c *Checker) Check(ctx context.Context, cfg config.Deps, reqs []Request) []Finding {
	budget := c.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	out := make([]Finding, len(reqs))
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = c.check(ctx, cfg, r)
		}()
	}
	wg.Wait()
	return out
}

// verdict collects reasons and keeps the most severe action.
type verdict struct {
	action  string
	reasons []string
}

// add records an ask or deny.
func (v *verdict) add(action, reason string) {
	if v.action != hook.Deny {
		v.action = action
	}
	v.reasons = append(v.reasons, reason)
}

func (c *Checker) check(ctx context.Context, cfg config.Deps, r Request) Finding {
	allow := Finding{Request: r, Action: hook.Allow}
	if glob.Any(cfg.Allow, r.Name) {
		return allow
	}
	private := r.Private || (r.System == Go && c.goPrivate(r.Name))
	unreachable := func(service string, err error) Finding {
		action := hook.Ask
		if cfg.Unreachable == "allow" {
			action = hook.Allow
		}
		return Finding{Request: r, Action: action, Reason: fmt.Sprintf("couldn't reach %s to check %s (%v); confirm it's the package you mean.", service, quote(r), err)}
	}

	name, pkg, err := c.lookupPackage(ctx, r)
	if err != nil {
		return unreachable("deps.dev", err)
	}
	if pkg == nil {
		if private {
			return allow
		}
		reason := fmt.Sprintf("%q doesn't exist on %s.", r.Name, registryName(r.System))
		if s := Suggest(r.System, r.Name); s != "" {
			reason = fmt.Sprintf("%q doesn't exist on %s — did you mean %q?", r.Name, registryName(r.System), s)
		}
		return Finding{Request: r, Action: hook.Deny, Reason: reason + " Agents invent plausible package names and attackers register them; check the name."}
	}

	malicious, err := c.malicious(ctx, r.System, name)
	if err != nil {
		return unreachable("OSV", err)
	}
	if len(malicious) > 0 {
		reason := fmt.Sprintf("%q is flagged as malicious (%s); don't install it.", r.Name, strings.Join(first(malicious, 3), ", "))
		if s := Suggest(r.System, r.Name); s != "" {
			reason = fmt.Sprintf("%q is flagged as malicious (%s) — it imitates %q. Don't install it.", r.Name, strings.Join(first(malicious, 3), ", "), s)
		}
		return Finding{Request: r, Action: hook.Deny, Reason: reason}
	}
	var v verdict
	if !private {
		if s := Suggest(r.System, r.Name); s != "" {
			v.add(hook.Ask, fmt.Sprintf("%q is a near miss of the popular %q — make sure it's the package you meant, not a typosquat.", r.Name, s))
		}
	}
	if days := c.ageDays(pkg.firstPublished()); cfg.MinAgeDays > 0 && days >= 0 && days < cfg.MinAgeDays {
		v.add(hook.Ask, fmt.Sprintf("%q was first published %s ago, under the %d-day minimum (deps.min_age_days).", r.Name, dayCount(days), cfg.MinAgeDays))
	}

	ver, ok := pkg.resolve(r.Version)
	if !ok {
		v.add(hook.Ask, fmt.Sprintf("%s has no version %q on %s.", r.Name, r.Version, registryName(r.System)))
		return v.finding(r)
	}
	info, err := c.lookupVersion(ctx, r.System, name, ver.VersionKey.Version)
	if err != nil {
		return unreachable("deps.dev", err)
	}
	label := r.Name + "@" + ver.VersionKey.Version
	if days := c.ageDays(info.PublishedAt); cfg.MinVersionAgeDays > 0 && days >= 0 && days < cfg.MinVersionAgeDays {
		v.add(hook.Ask, fmt.Sprintf("%s was published %s ago, under the %d-day minimum (deps.min_version_age_days); new releases are how hijacked packages spread.", label, dayCount(days), cfg.MinVersionAgeDays))
	}
	if ver.IsDeprecated || info.IsDeprecated {
		v.add(hook.Ask, fmt.Sprintf("%s is deprecated.", label))
	}
	if action, reason := licenseVerdict(cfg.Licenses, info.Licenses); action != hook.Allow {
		v.add(action, label+" "+reason)
	}
	var advisories []string
	for _, a := range info.AdvisoryKeys {
		if strings.HasPrefix(a.ID, "MAL-") {
			v.add(hook.Deny, fmt.Sprintf("%s is flagged as malicious (%s); don't install it.", label, a.ID))
			continue
		}
		advisories = append(advisories, a.ID)
	}
	if len(advisories) > 0 && cfg.Vulnerabilities != "allow" {
		action := hook.Ask
		if cfg.Vulnerabilities == "deny" {
			action = hook.Deny
		}
		more := ""
		if len(advisories) > 3 {
			more = fmt.Sprintf(" and %d more", len(advisories)-3)
		}
		v.add(action, fmt.Sprintf("%s has known vulnerabilities (%s%s); prefer a fixed version.", label, strings.Join(first(advisories, 3), ", "), more))
	}
	return v.finding(r)
}

func (v verdict) finding(r Request) Finding {
	if v.action == "" {
		return Finding{Request: r, Action: hook.Allow}
	}
	return Finding{Request: r, Action: v.action, Reason: strings.Join(v.reasons, " ")}
}

// packageDoc is deps.dev's GetPackage response.
type packageDoc struct {
	Versions []versionRef `json:"versions"`
}

type versionRef struct {
	VersionKey struct {
		Version string `json:"version"`
	} `json:"versionKey"`
	PublishedAt  time.Time `json:"publishedAt"`
	IsDefault    bool      `json:"isDefault"`
	IsDeprecated bool      `json:"isDeprecated"`
}

// versionDoc is deps.dev's GetVersion response.
type versionDoc struct {
	PublishedAt  time.Time `json:"publishedAt"`
	IsDeprecated bool      `json:"isDeprecated"`
	Licenses     []string  `json:"licenses"`
	AdvisoryKeys []struct {
		ID string `json:"id"`
	} `json:"advisoryKeys"`
}

func (p *packageDoc) firstPublished() time.Time {
	var t time.Time
	for _, v := range p.Versions {
		if !v.PublishedAt.IsZero() && (t.IsZero() || v.PublishedAt.Before(t)) {
			t = v.PublishedAt
		}
	}
	return t
}

var rangeOps = regexp.MustCompile(`^[\^~=<>v ]+`)

// resolve picks the version a request gets: the requested one, the
// version a simple range starts at, or the registry's default. ok is false
// only when an exact version was asked for and doesn't exist.
func (p *packageDoc) resolve(want string) (versionRef, bool) {
	if want != "" && !strings.ContainsAny(want, " |,*xX") {
		bare := rangeOps.ReplaceAllString(want, "")
		for _, v := range p.Versions {
			if got := v.VersionKey.Version; got == want || got == bare || strings.TrimPrefix(got, "v") == bare {
				return v, true
			}
		}
		if bare == strings.TrimPrefix(want, "v") || strings.HasPrefix(want, "==") {
			return versionRef{}, false // exact, and missing
		}
	}
	var latest versionRef
	for _, v := range p.Versions {
		if v.IsDefault {
			return v, true
		}
		if latest.VersionKey.Version == "" || v.PublishedAt.After(latest.PublishedAt) {
			latest = v
		}
	}
	return latest, latest.VersionKey.Version != ""
}

// lookupPackage fetches a package from deps.dev. A nil doc with a nil
// error means it doesn't exist. Go package paths are tried as modules,
// then by their parent paths, since go get takes packages within modules.
func (c *Checker) lookupPackage(ctx context.Context, r Request) (string, *packageDoc, error) {
	names := []string{r.Name}
	if r.System == Go {
		names = goModuleCandidates(r.Name)
	}
	for _, name := range names {
		var doc packageDoc
		u := fmt.Sprintf("%s/v3/systems/%s/packages/%s", c.DepsDevURL, depsDevSystem(r.System), escape(name))
		found, err := c.getJSON(ctx, http.MethodGet, u, nil, &doc)
		if err != nil {
			return "", nil, err
		}
		if found && len(doc.Versions) > 0 {
			return name, &doc, nil
		}
	}
	return r.Name, nil, nil
}

func (c *Checker) lookupVersion(ctx context.Context, system, name, version string) (versionDoc, error) {
	var doc versionDoc
	u := fmt.Sprintf("%s/v3/systems/%s/packages/%s/versions/%s", c.DepsDevURL, depsDevSystem(system), escape(name), escape(version))
	found, err := c.getJSON(ctx, http.MethodGet, u, nil, &doc)
	if err == nil && !found {
		err = fmt.Errorf("deps.dev has no record of %s@%s", name, version)
	}
	return doc, err
}

// malicious returns the OSV malicious-package advisories (MAL-*) for a
// package, any version.
func (c *Checker) malicious(ctx context.Context, system, name string) ([]string, error) {
	body, err := json.Marshal(map[string]any{"package": map[string]string{"name": name, "ecosystem": osvEcosystem(system)}})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Vulns []struct {
			ID string `json:"id"`
		} `json:"vulns"`
	}
	if _, err := c.getJSON(ctx, http.MethodPost, c.OSVURL+"/v1/query", body, &resp); err != nil {
		return nil, err
	}
	var out []string
	for _, v := range resp.Vulns {
		if strings.HasPrefix(v.ID, "MAL-") {
			out = append(out, v.ID)
		}
	}
	return out, nil
}

// cached is a stored response.
type cached struct {
	Time time.Time       `json:"time"`
	Body json.RawMessage `json:"body"`
}

// getJSON fetches url into v, through the cache. found is false on 404,
// which isn't cached: a missing package may be published at any time.
func (c *Checker) getJSON(ctx context.Context, method, u string, body []byte, v any) (found bool, err error) {
	key := ""
	if c.CacheDir != "" {
		sum := sha256.Sum256(append([]byte(method+" "+u+"\n"), body...))
		key = filepath.Join(c.CacheDir, hex.EncodeToString(sum[:16])+".json")
		var hit cached
		if data, err := os.ReadFile(key); err == nil && json.Unmarshal(data, &hit) == nil && c.now().Sub(hit.Time) < cacheTTL {
			return true, json.Unmarshal(hit.Body, v)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return false, errors.New("timed out")
		}
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s %s", method, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	if key != "" {
		if entry, err := json.Marshal(cached{Time: c.now(), Body: data}); err == nil && os.MkdirAll(c.CacheDir, 0o700) == nil {
			_ = os.WriteFile(key, entry, 0o600)
		}
	}
	return true, nil
}

func (c *Checker) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// ageDays is the whole days since t, or -1 when t is unknown.
func (c *Checker) ageDays(t time.Time) int {
	if t.IsZero() {
		return -1
	}
	return int(c.now().Sub(t).Hours() / 24)
}

// goPrivate reports whether a module matches GOPRIVATE or GONOSUMDB, so
// it isn't expected on the public proxy.
func (c *Checker) goPrivate(name string) bool {
	if c.Getenv == nil {
		return false
	}
	for _, key := range []string{"GOPRIVATE", "GONOSUMDB", "GONOPROXY"} {
		for _, pat := range strings.Split(c.Getenv(key), ",") {
			pat = strings.TrimSpace(pat)
			if pat == "" {
				continue
			}
			// Patterns match path prefixes, element by element.
			elems := strings.Split(name, "/")
			for i := len(elems); i > 0; i-- {
				if glob.Match(pat, strings.Join(elems[:i], "/")) {
					return true
				}
			}
		}
	}
	return false
}

// goModuleCandidates lists a Go path and its parents, longest first, down
// to the shortest plausible module path.
func goModuleCandidates(p string) []string {
	elems := strings.Split(p, "/")
	minLen := 1
	switch elems[0] {
	case "github.com", "gitlab.com", "bitbucket.org":
		minLen = 3
	case "golang.org", "gopkg.in", "go.opentelemetry.io":
		minLen = 2
	}
	var out []string
	for i := len(elems); i >= minLen && i >= 1 && len(out) < 4; i-- {
		out = append(out, strings.Join(elems[:i], "/"))
	}
	return out
}

// licenseVerdict applies deps.licenses to a version's licenses. Each
// entry may be an SPDX expression; an OR is satisfied by any allowed
// alternative, an AND (or WITH) needs every term.
func licenseVerdict(policy config.Licenses, licenses []string) (action, reason string) {
	if len(policy.Allow) == 0 && len(policy.Deny) == 0 {
		return hook.Allow, ""
	}
	if len(licenses) == 0 {
		if len(policy.Allow) > 0 {
			return hook.Ask, "has no license deps.dev recognizes; deps.licenses.allow is set."
		}
		return hook.Allow, ""
	}
	in := func(list []string, term string) bool {
		for _, l := range list {
			if strings.EqualFold(l, term) {
				return true
			}
		}
		return false
	}
	allowed := true
	for _, expr := range licenses {
		alts := alternatives(expr)
		if len(alts) == 0 {
			continue
		}
		denied, ok := true, false
		for _, terms := range alts {
			altDenied, altAllowed := false, true
			for _, t := range terms {
				if in(policy.Deny, t) {
					altDenied = true
				}
				if len(policy.Allow) > 0 && !in(policy.Allow, t) {
					altAllowed = false
				}
			}
			if !altDenied {
				denied = false
				if altAllowed {
					ok = true
				}
			}
		}
		if denied {
			return hook.Deny, fmt.Sprintf("is licensed %s, which deps.licenses.deny forbids.", expr)
		}
		if !ok {
			allowed = false
		}
	}
	if !allowed {
		return hook.Ask, fmt.Sprintf("is licensed %s, which isn't in deps.licenses.allow.", strings.Join(licenses, ", "))
	}
	return hook.Allow, ""
}

var (
	orSplit  = regexp.MustCompile(`(?i)\s+OR\s+`)
	andSplit = regexp.MustCompile(`(?i)\s+(?:AND|WITH)\s+`)
)

// alternatives splits an SPDX expression into OR alternatives, each a list
// of terms that must all hold. Parentheses are flattened: exact for the
// common forms (A OR B, A AND B), an approximation for nested ones.
func alternatives(expr string) [][]string {
	expr = strings.NewReplacer("(", " ", ")", " ").Replace(expr)
	var out [][]string
	for _, alt := range orSplit.Split(strings.TrimSpace(expr), -1) {
		var terms []string
		for _, t := range andSplit.Split(strings.TrimSpace(alt), -1) {
			if t = strings.TrimSpace(t); t != "" {
				terms = append(terms, t)
			}
		}
		if len(terms) > 0 {
			out = append(out, terms)
		}
	}
	return out
}

func depsDevSystem(system string) string {
	switch system {
	case NPM:
		return "NPM"
	case PyPI:
		return "PYPI"
	case Go:
		return "GO"
	default:
		return "CARGO"
	}
}

func osvEcosystem(system string) string {
	switch system {
	case NPM:
		return "npm"
	case PyPI:
		return "PyPI"
	case Go:
		return "Go"
	default:
		return "crates.io"
	}
}

// escape encodes a path segment, slashes and @ included.
func escape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func quote(r Request) string { return fmt.Sprintf("%q", r.String()) }

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// dayCount renders a number of days.
func dayCount(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}
