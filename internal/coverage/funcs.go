package coverage

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
)

// Func is a function's coverage.
type Func struct {
	// File is the local path the function was parsed from.
	File string
	Line int
	Name string
	Stats
}

// Uncovered returns the number of uncovered statements.
func (f Func) Uncovered() int { return f.Total - f.Covered }

// Resolver maps an import-qualified profile file name to a local path.
type Resolver func(profileFile string) (string, bool)

// Funcs attributes profile blocks to the functions that contain them.
// Files the resolver cannot map, or that fail to parse, are skipped.
func (p *Profile) Funcs(resolve Resolver) []Func {
	byFile := map[string][]Block{}
	for _, b := range p.Blocks {
		byFile[b.File] = append(byFile[b.File], b)
	}
	var out []Func
	for file, blocks := range byFile {
		local, ok := resolve(file)
		if !ok {
			continue
		}
		decls, err := funcSpans(local)
		if err != nil {
			continue
		}
		for _, d := range decls {
			f := Func{File: local, Line: d.start, Name: d.name}
			for _, b := range blocks {
				if b.StartLine >= d.start && b.StartLine <= d.end {
					f.add(b)
				}
			}
			if f.Total > 0 {
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// WorstFuncs returns up to n functions with the most uncovered statements.
func WorstFuncs(funcs []Func, n int) []Func {
	var out []Func
	for _, f := range funcs {
		if f.Uncovered() > 0 {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Uncovered() > out[j].Uncovered() })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

type span struct {
	name       string
	start, end int
}

// funcSpans parses file and returns the name and line range of each function that has a body.
func funcSpans(file string) ([]span, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []span
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		out = append(out, span{
			name:  FuncName(fn),
			start: fset.Position(fn.Pos()).Line,
			end:   fset.Position(fn.End()).Line,
		})
	}
	return out, nil
}

// FuncName renders a declaration as go tool cover does, e.g. "(*Queue).Drain".
func FuncName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return fmt.Sprintf("(%s).%s", recvType(fn.Recv.List[0].Type), fn.Name.Name)
}

// recvType renders a receiver type expression as a string, with "*" for pointers and type parameters dropped.
func recvType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + recvType(t.X)
	case *ast.IndexExpr:
		return recvType(t.X)
	case *ast.IndexListExpr:
		return recvType(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return "?"
	}
}
