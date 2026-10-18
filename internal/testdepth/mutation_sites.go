package testdepth

import (
	"go/ast"
	"go/token"
	"sort"
)

// mutSite is one mutation: replace src[Off:End] (Old) with New.
type mutSite struct {
	Func Func
	Line int
	Col  int
	Off  int
	End  int
	Old  string
	New  string
	// Remove marks an operator deletion rather than a replacement.
	Remove bool
}

// Desc describes the mutation for the report.
func (s mutSite) Desc() string {
	if s.Remove {
		return "`" + s.Old + "` removed"
	}
	return "`" + s.Old + "` changed to `" + s.New + "`"
}

// mutBinary maps binary operators to their mutations.
var mutBinary = map[token.Token][]token.Token{
	token.LSS:  {token.LEQ, token.GEQ},
	token.LEQ:  {token.LSS, token.GTR},
	token.GTR:  {token.GEQ, token.LEQ},
	token.GEQ:  {token.GTR, token.LSS},
	token.EQL:  {token.NEQ},
	token.NEQ:  {token.EQL},
	token.ADD:  {token.SUB},
	token.SUB:  {token.ADD},
	token.MUL:  {token.QUO},
	token.QUO:  {token.MUL},
	token.REM:  {token.MUL},
	token.LAND: {token.LOR},
	token.LOR:  {token.LAND},
}

// mutSites finds every mutation on the changed lines of f.
func mutSites(f Func) []mutSite {
	decl, file, fset := f.Decl()
	if decl == nil || decl.Body == nil || (file != nil && ast.IsGenerated(file)) {
		return nil
	}
	changed := map[int]bool{}
	for _, l := range f.Changed {
		changed[l] = true
	}
	var out []mutSite
	add := func(pos token.Pos, old, repl string, remove bool) {
		p := fset.Position(pos)
		if !changed[p.Line] {
			return
		}
		out = append(out, mutSite{
			Func: f, Line: p.Line, Col: p.Column, Off: p.Offset, End: p.Offset + len(old),
			Old: old, New: repl, Remove: remove,
		})
	}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.ADD && (mutIsString(x.X) || mutIsString(x.Y)) {
				return true
			}
			for _, to := range mutBinary[x.Op] {
				add(x.OpPos, x.Op.String(), to.String(), false)
			}
		case *ast.IncDecStmt:
			to := token.DEC
			if x.Tok == token.DEC {
				to = token.INC
			}
			add(x.TokPos, x.Tok.String(), to.String(), false)
		case *ast.AssignStmt:
			switch x.Tok {
			case token.ADD_ASSIGN:
				if len(x.Rhs) == 1 && mutIsString(x.Rhs[0]) {
					return true
				}
				add(x.TokPos, "+=", "-=", false)
			case token.SUB_ASSIGN:
				add(x.TokPos, "-=", "+=", false)
			}
		case *ast.UnaryExpr:
			if x.Op == token.SUB || x.Op == token.NOT {
				add(x.OpPos, x.Op.String(), "", true)
			}
		case *ast.Ident:
			switch x.Name {
			case "true":
				add(x.Pos(), "true", "false", false)
			case "false":
				add(x.Pos(), "false", "true", false)
			}
		}
		return true
	})
	return out
}

// mutIsString reports a string literal operand.
func mutIsString(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// mutApply splices the mutation into src.
func mutApply(src []byte, s mutSite) []byte {
	out := make([]byte, 0, len(src)+len(s.New))
	out = append(out, src[:s.Off]...)
	out = append(out, s.New...)
	return append(out, src[s.End:]...)
}

// mutSort orders sites deterministically by file, line, column, mutation.
func mutSort(sites []mutSite) {
	sort.SliceStable(sites, func(i, j int) bool {
		a, b := sites[i], sites[j]
		if a.Func.File != b.Func.File {
			return a.Func.File < b.Func.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.New < b.New
	})
}

// mutSample keeps every k-th site so that at most n remain.
func mutSample(sites []mutSite, n int) []mutSite {
	if n <= 0 || len(sites) <= n {
		return sites
	}
	k := (len(sites) + n - 1) / n
	var out []mutSite
	for i := 0; i < len(sites); i += k {
		out = append(out, sites[i])
	}
	return out
}
