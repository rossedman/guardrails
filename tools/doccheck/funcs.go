package main

import (
	"go/ast"

	"github.com/rossedman/guardrails/tools/internal/gofiles"
)

// funcs returns the names of f's functions and methods with no doc comment,
// methods as Type.Method.
func funcs(f gofiles.File) []string {
	var out []string
	for _, decl := range f.AST.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Doc != nil {
			continue
		}
		name := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) == 1 {
			name = recvType(fd.Recv.List[0].Type) + "." + name
		}
		out = append(out, name)
	}
	return out
}

// recvType returns the type name of a method receiver, without pointers or
// type parameters.
func recvType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvType(t.X)
	case *ast.IndexExpr:
		return recvType(t.X)
	case *ast.IndexListExpr:
		return recvType(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}
