package testdepth

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rossedman/guardrails/internal/coverage"
	"github.com/rossedman/guardrails/internal/glob"
)

// Func is a function or method changed this session.
type Func struct {
	// File is the absolute path; Dir its package directory.
	File string
	Dir  string
	// Name renders like go tool cover, e.g. "(*Queue).Drain"; Ident is the
	// bare identifier, e.g. "Drain".
	Name  string
	Ident string
	// Start and End are the declaration's line span.
	Start, End int
	// Changed lists the changed lines inside the span.
	Changed []int
	// Annotations maps guardrails directives found in the doc comment or
	// the line above (hot, nobench, nofuzz) to their text.
	Annotations map[string]string

	decl *ast.FuncDecl
	file *ast.File
	fset *token.FileSet
}

// Key identifies the function across runs, e.g. "internal/api.decodeRequest".
func (f Func) Key(root string) string {
	rel, err := filepath.Rel(root, f.Dir)
	if err != nil {
		rel = f.Dir
	}
	return filepath.ToSlash(rel) + "." + f.Name
}

// Decl returns the parsed declaration, with its file and file set.
func (f Func) Decl() (*ast.FuncDecl, *ast.File, *token.FileSet) {
	return f.decl, f.file, f.fset
}

var directive = regexp.MustCompile(`//\s*guardrails:(hot|nobench|nofuzz)\b\s*(.*)`)

// ChangedFuncs parses the non-test, non-generated Go files in changed and
// returns the functions whose bodies or signatures contain changed lines.
func ChangedFuncs(changed map[string]coverage.LineSet) ([]Func, error) {
	files := make([]string, 0, len(changed))
	for f := range changed {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	var out []Func
	for _, path := range files {
		fset := token.NewFileSet()
		src, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if ast.IsGenerated(file) {
			continue
		}
		lines := changed[path]
		comments := lineComments(fset, file)
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			start, end := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
			var hit []int
			for l := start; l <= end; l++ {
				if lines[l] {
					hit = append(hit, l)
				}
			}
			if len(hit) == 0 {
				continue
			}
			out = append(out, Func{
				File: path, Dir: filepath.Dir(path),
				Name: coverage.FuncName(fn), Ident: fn.Name.Name,
				Start: start, End: end, Changed: hit,
				Annotations: annotations(fn, comments[start-1]),
				decl:        fn, file: file, fset: fset,
			})
		}
	}
	return out, nil
}

// lineComments maps a line number to the comment text on it.
func lineComments(fset *token.FileSet, file *ast.File) map[int]string {
	out := map[int]string{}
	for _, g := range file.Comments {
		for _, c := range g.List {
			out[fset.Position(c.Pos()).Line] = c.Text
		}
	}
	return out
}

func annotations(fn *ast.FuncDecl, lineAbove string) map[string]string {
	out := map[string]string{}
	var texts []string
	if fn.Doc != nil {
		for _, c := range fn.Doc.List {
			texts = append(texts, c.Text)
		}
	}
	texts = append(texts, lineAbove)
	for _, t := range texts {
		if m := directive.FindStringSubmatch(t); m != nil {
			out[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return out
}

// Signals explains why a function is a hot path or a parser.
type Signals struct {
	// Strong signals make a requirement block until met; weak ones are
	// asked once per session.
	Strong  bool
	Reasons []string
}

// Any reports whether there is any signal.
func (s Signals) Any() bool { return len(s.Reasons) > 0 }

func (s *Signals) add(strong bool, reason string) {
	s.Reasons = append(s.Reasons, reason)
	s.Strong = s.Strong || strong
}

var (
	// Verbs are matched as whole camelCase words anywhere in the name:
	// decodeRequest, benchParse and ParseConfig match; Parser, Scanner and
	// Sorter (nouns) don't.
	hotVerbs = wordSet("encode", "decode", "marshal", "unmarshal", "parse", "hash",
		"compress", "decompress", "sort", "serialize", "deserialize", "scan", "tokenize", "tokenise", "lex")
	// parseVerbs turn text or bytes into structure; a string parameter
	// counts as input only for these.
	parseVerbs = wordSet("parse", "decode", "unmarshal", "deserialize", "scan", "lex", "tokenize", "tokenise")
	// readVerbs read structured input, but typically take a path when the
	// parameter is a string, so they need byte or reader input.
	readVerbs   = wordSet("read", "load", "from")
	hotImports  = []string{"encoding/", "compress/", "hash", "crypto/", "sort", "slices", "bufio", "strconv"}
	inputImport = []string{"encoding/", "bufio", "net/http", "net/url", "mime", "archive/", "text/template", "html/template"}
)

func wordSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// nameWords splits a Go identifier into lower-case camelCase words, e.g.
// "decodeHTTPRequest" → decode, http, request.
func nameWords(name string) []string {
	var (
		words []string
		cur   []rune
	)
	rs := []rune(name)
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	for i, r := range rs {
		switch {
		case r == '_' || (r >= '0' && r <= '9'):
			flush()
			continue
		case i > 0 && r >= 'A' && r <= 'Z':
			prevLower := rs[i-1] >= 'a' && rs[i-1] <= 'z'
			nextLower := i+1 < len(rs) && rs[i+1] >= 'a' && rs[i+1] <= 'z'
			prevUpper := rs[i-1] >= 'A' && rs[i-1] <= 'Z'
			if prevLower || (prevUpper && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// thirdPerson conjugates a verb: sort → sorts, hash → hashes.
func thirdPerson(v string) string {
	for _, suf := range []string{"sh", "ss", "x"} {
		if strings.HasSuffix(v, suf) {
			return v + "es"
		}
	}
	return v + "s"
}

// verb returns the first word of name in set, or "".
func verb(name string, set map[string]bool) string {
	for _, w := range nameWords(name) {
		if set[w] {
			return w
		}
	}
	return ""
}

// HotPath reports benchmark signals for f. perfPackages are module-relative
// patterns from go.bench.packages.
func HotPath(f Func, root string, perfPackages []string) Signals {
	var s Signals
	if _, ok := f.Annotations["hot"]; ok {
		s.add(true, "marked // guardrails:hot")
	}
	if rel, err := filepath.Rel(root, f.Dir); err == nil {
		rel = filepath.ToSlash(rel)
		for _, p := range perfPackages {
			pat := strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/...")
			if rel == pat || glob.Match(pat, rel) || (strings.HasSuffix(p, "/...") && strings.HasPrefix(rel+"/", pat+"/")) {
				s.add(true, "package is listed in go.bench.packages")
				break
			}
		}
	}
	// Imports and loops only support a signal; on their own they flag far
	// too much ordinary code.
	if v := verb(f.Ident, hotVerbs); v != "" {
		s.add(false, fmt.Sprintf("%s %s", f.Ident, thirdPerson(v)))
	}
	if !s.Any() {
		return s
	}
	if imp := usedImports(f, hotImports); len(imp) > 0 {
		s.add(false, "uses "+strings.Join(imp, ", "))
	}
	if loopsOverInput(f.decl) {
		s.add(false, "loops over caller-sized input")
	}
	return s
}

// Parser reports fuzzing signals for f: its name says it parses or reads,
// and it takes bytes, a reader, or (for parse verbs) a string.
func Parser(f Func) Signals {
	var s Signals
	in := inputParams(f.decl)
	if len(in) == 0 {
		return s
	}
	switch {
	case verb(f.Ident, parseVerbs) != "":
	case verb(f.Ident, readVerbs) != "":
		// Read/Load with a string usually means a path, not content.
		in = withoutString(in)
	default:
		return s
	}
	if len(in) == 0 {
		return s
	}
	if returnsError(f.decl) {
		s.add(true, fmt.Sprintf("%s parses %s input and can fail", f.Ident, strings.Join(in, ", ")))
	} else {
		s.add(false, fmt.Sprintf("%s parses %s input", f.Ident, strings.Join(in, ", ")))
	}
	if imp := usedImports(f, inputImport); len(imp) > 0 && s.Any() {
		s.add(false, "uses "+strings.Join(imp, ", "))
	}
	return s
}

func withoutString(in []string) []string {
	var out []string
	for _, t := range in {
		if t != "string" {
			out = append(out, t)
		}
	}
	return out
}

// inputParams lists parameter types that carry loosely structured input.
func inputParams(fn *ast.FuncDecl) []string {
	var out []string
	if fn.Type.Params == nil {
		return nil
	}
	for _, p := range fn.Type.Params.List {
		switch t := typeString(p.Type); t {
		case "[]byte", "string", "io.Reader", "*bufio.Reader", "*bufio.Scanner", "*http.Request", "*os.File", "fs.File":
			out = append(out, t)
		}
	}
	return out
}

func returnsError(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		if id, ok := r.Type.(*ast.Ident); ok && id.Name == "error" {
			return true
		}
	}
	return false
}

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + typeString(t.Elt)
		}
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.Ellipsis:
		return "..." + typeString(t.Elt)
	}
	return ""
}

// usedImports returns imports matching prefixes that f's body refers to.
func usedImports(f Func, prefixes []string) []string {
	names := map[string]string{}
	for _, imp := range f.file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		matched := false
		for _, p := range prefixes {
			if path == strings.TrimSuffix(p, "/") || strings.HasPrefix(path, p) {
				matched = true
			}
		}
		if !matched {
			continue
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = path
	}
	seen := map[string]bool{}
	ast.Inspect(f.decl.Body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				if path, ok := names[id.Name]; ok {
					seen[path] = true
				}
			}
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// loopsOverInput reports a range loop over a slice, map or string
// parameter, i.e. work proportional to caller-sized input.
func loopsOverInput(fn *ast.FuncDecl) bool {
	params := map[string]bool{}
	if fn.Type.Params != nil {
		for _, p := range fn.Type.Params.List {
			switch p.Type.(type) {
			case *ast.ArrayType, *ast.MapType, *ast.Ellipsis:
			default:
				if typeString(p.Type) != "string" {
					continue
				}
			}
			for _, n := range p.Names {
				params[n.Name] = true
			}
		}
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok {
			if id, ok := r.X.(*ast.Ident); ok && params[id.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

// Tests indexes the Benchmark and Fuzz functions of a package directory
// and which package-level identifiers each one calls.
type Tests struct {
	// Benchmarks and Fuzz map a test function name to the identifiers it
	// calls directly or through a closure.
	Benchmarks map[string]map[string]bool
	Fuzz       map[string]map[string]bool
	// Files maps a test function name to its file.
	Files map[string]string
}

// IndexTests parses dir's _test.go files.
func IndexTests(dir string) (*Tests, error) {
	t := &Tests{Benchmarks: map[string]map[string]bool{}, Fuzz: map[string]map[string]bool{}, Files: map[string]string{}}
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	for _, path := range matches {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			var into map[string]map[string]bool
			switch name := fn.Name.Name; {
			case strings.HasPrefix(name, "Benchmark") && len(fn.Type.Params.List) == 1:
				into = t.Benchmarks
			case strings.HasPrefix(name, "Fuzz") && len(fn.Type.Params.List) == 1:
				into = t.Fuzz
			default:
				continue
			}
			into[fn.Name.Name] = calledIdents(fn.Body)
			t.Files[fn.Name.Name] = path
		}
	}
	return t, nil
}

func calledIdents(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			out[fn.Name] = true
		case *ast.SelectorExpr:
			out[fn.Sel.Name] = true
		case *ast.IndexExpr:
			if id, ok := fn.X.(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// Covering returns the test functions in set that call f, sorted.
func Covering(set map[string]map[string]bool, f Func) []string {
	var out []string
	for name, calls := range set {
		if calls[f.Ident] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
