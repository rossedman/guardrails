package terraform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/rossedman/guardrails/internal/feedback"
)

// builtinProviders need no required_providers entry.
var builtinProviders = map[string]bool{"terraform": true}

// VersionConstraints parses every .tf file in dir and reports providers
// that lack a version constraint: entries in required_providers without a
// version, and providers used (by provider blocks, resources or data
// sources) that have no required_providers entry at all. Issue files are
// absolute paths.
func VersionConstraints(dir string) ([]feedback.Issue, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var (
		issues   []feedback.Issue
		declared = map[string]bool{}
		used     = map[string]use{}
		errs     []string
	)
	parser := hclparse.NewParser()
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		f, diags := parser.ParseHCL(src, p)
		if diags.HasErrors() {
			errs = append(errs, diags.Error())
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, b := range body.Blocks {
			switch b.Type {
			case "terraform":
				for _, inner := range b.Body.Blocks {
					if inner.Type != "required_providers" {
						continue
					}
					for name, attr := range inner.Body.Attributes {
						declared[name] = true
						if !hasVersion(attr.Expr) {
							issues = append(issues, feedback.Issue{
								File: p, Line: attr.SrcRange.Start.Line, Rule: "provider-version",
								Message: fmt.Sprintf("provider %q in required_providers has no version constraint", name),
							})
						}
					}
				}
			case "provider":
				if len(b.Labels) == 1 {
					note(used, b.Labels[0], p, b.DefRange().Start.Line)
				}
			case "resource", "data", "ephemeral":
				if len(b.Labels) >= 1 {
					name := b.Labels[0]
					if i := strings.Index(name, "_"); i > 0 {
						name = name[:i]
					}
					if attr, ok := b.Body.Attributes["provider"]; ok {
						if trav, diags := hcl.AbsTraversalForExpr(attr.Expr); !diags.HasErrors() && len(trav) > 0 {
							name = trav.RootName()
						}
					}
					note(used, name, p, b.DefRange().Start.Line)
				}
			}
		}
	}
	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if declared[n] || builtinProviders[n] {
			continue
		}
		u := used[n]
		issues = append(issues, feedback.Issue{
			File: u.file, Line: u.line, Rule: "provider-version",
			Message: fmt.Sprintf("provider %q is used but not declared in required_providers", n),
		})
	}
	if len(errs) > 0 {
		return issues, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return issues, nil
}

// use is where a provider is first used.
type use struct {
	file string
	line int
}

// note records the first use of a provider.
func note(m map[string]use, name, file string, line int) {
	if _, ok := m[name]; !ok {
		m[name] = use{file: file, line: line}
	}
}

// hasVersion reports whether a required_providers entry constrains the
// version: the legacy string form, or an object with a version key.
func hasVersion(expr hclsyntax.Expression) bool {
	if obj, ok := expr.(*hclsyntax.ObjectConsExpr); ok {
		for _, item := range obj.Items {
			if hcl.ExprAsKeyword(item.KeyExpr) == "version" {
				return true
			}
			if v, diags := item.KeyExpr.Value(nil); !diags.HasErrors() && v.Type() == cty.String && v.AsString() == "version" {
				return true
			}
		}
		return false
	}
	v, diags := expr.Value(nil)
	return !diags.HasErrors() && v.Type() == cty.String && v.AsString() != ""
}
