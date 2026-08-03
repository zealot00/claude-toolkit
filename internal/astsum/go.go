// Package astsum produces compressed structural summaries of source files —
// package/type/function signatures without bodies — so Claude can read a
// file's shape at a fraction of the token cost. Go is parsed with the
// standard library's go/parser; Python with a pure-Go top-level scanner (no
// Python runtime).
package astsum

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
)

// GoFunc is one function or method signature.
type GoFunc struct {
	Name    string `json:"name"`
	Recv    string `json:"recv,omitempty"`
	Params  string `json:"params"`
	Results string `json:"results,omitempty"`
}

// GoType is one top-level type declaration.
type GoType struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"` // struct / interface / type alias / other
	Methods []GoFunc `json:"methods,omitempty"`
}

// GoSummary is the structural shape of a Go file.
type GoSummary struct {
	Package string   `json:"package"`
	Imports []string `json:"imports,omitempty"`
	Types   []GoType `json:"types,omitempty"`
	Funcs   []GoFunc `json:"funcs,omitempty"`
}

// SummarizeGo reads and summarizes a Go source file.
func SummarizeGo(path string) (*GoSummary, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("asts sum: parse %s: %w", path, err)
	}

	s := &GoSummary{Package: f.Name.Name}
	for _, imp := range f.Imports {
		if imp.Path != nil {
			s.Imports = append(s.Imports, strings.Trim(imp.Path.Value, `"`))
		}
	}

	// First pass: types.
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !ts.Name.IsExported() {
				continue
			}
			s.Types = append(s.Types, GoType{Name: ts.Name.Name, Kind: goTypeKind(ts)})
		}
	}

	// Second pass: package-level funcs; methods are attached in a third pass.
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Recv == nil {
			if fd.Name.IsExported() {
				s.Funcs = append(s.Funcs, goFuncSig(fset, fd))
			}
			continue
		}
		recv := receiverName(fd.Recv)
		for i := range s.Types {
			if s.Types[i].Name == recv {
				s.Types[i].Methods = append(s.Types[i].Methods, goFuncSig(fset, fd))
			}
		}
	}
	return s, nil
}

func goTypeKind(ts *ast.TypeSpec) string {
	switch t := ts.Type.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.Ident:
		return "alias"
	case *ast.ArrayType, *ast.MapType, *ast.ChanType:
		return "type"
	default:
		_ = t
		return "type"
	}
}

func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	// *T, T, or generic T[X] -- peel pointer and index.
	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.IndexExpr:
			expr = t.X
		case *ast.IndexListExpr:
			expr = t.X
		default:
			if id, ok := expr.(*ast.Ident); ok {
				return id.Name
			}
			return ""
		}
	}
}

func goFuncSig(fset *token.FileSet, fd *ast.FuncDecl) GoFunc {
	g := GoFunc{Name: fd.Name.Name}
	if fd.Recv != nil {
		g.Recv = receiverName(fd.Recv)
	}
	if fd.Type.Params != nil {
		g.Params = fieldList(fset, fd.Type.Params)
	}
	if fd.Type.Results != nil && len(fd.Type.Results.List) > 0 {
		g.Results = fieldList(fset, fd.Type.Results)
	}
	return g
}

func fieldList(fset *token.FileSet, fl *ast.FieldList) string {
	var parts []string
	for _, f := range fl.List {
		names := ""
		if len(f.Names) > 0 {
			var ns []string
			for _, n := range f.Names {
				ns = append(ns, n.Name)
			}
			names = strings.Join(ns, ", ") + " "
		}
		parts = append(parts, names+exprString(fset, f.Type))
	}
	return strings.Join(parts, ", ")
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	switch t := e.(type) {
	case *ast.FuncType:
		// func(...) ... -- collapse to fn(...)
		return "func(" + fieldList(fset, t.Params) + ")" + resultsString(fset, t.Results)
	case *ast.InterfaceType:
		return "interface{...}"
	case *ast.StructType:
		return "struct{...}"
	default:
		return strings.TrimSpace(printNode(fset, e))
	}
}

func resultsString(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	if len(fl.List) == 1 && len(fl.List[0].Names) == 0 {
		return " " + exprString(fset, fl.List[0].Type)
	}
	return " (" + fieldList(fset, fl) + ")"
}

// printNode renders an AST expression with the standard printer.
func printNode(fset *token.FileSet, e ast.Expr) string {
	var sb strings.Builder
	if err := printer.Fprint(&sb, fset, e); err != nil {
		return ""
	}
	return sb.String()
}
