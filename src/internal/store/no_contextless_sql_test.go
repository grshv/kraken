package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestNoContextlessSQLCalls verifies package store (excluding _test.go) never
// calls *sql.DB/*sql.Tx/*sql.Stmt methods without a context: Exec, Query,
// QueryRow, Begin, Prepare. Every SQL call must carry a context so the
// OpTimeout ceiling can interrupt lock acquisition.
func TestNoContextlessSQLCalls(t *testing.T) {
	banned := map[string]bool{
		"Exec": true, "Query": true, "QueryRow": true,
		"Begin": true, "Prepare": true,
	}
	fset := token.NewFileSet()
	pkgs, e := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		// Skip _test.go files: the requirement applies to production code.
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, pkg := range pkgs {
		ast.Inspect(pkg, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if !banned[name] {
				return true
			}
			// Allow the Context variants (ExecContext, QueryContext, ...).
			// The base name without the Context suffix is what we ban.
			if strings.HasSuffix(name, "Context") {
				return true
			}
			// BeginTx is the context-aware begin; only bare Begin is banned.
			if name == "Begin" {
				t.Errorf("%s: bare Begin() call; use BeginTx(ctx)", fset.Position(call.Pos()))
			} else {
				t.Errorf("%s: bare %s() call; use %sContext", fset.Position(call.Pos()), name, name)
			}
			return true
		})
	}
}
