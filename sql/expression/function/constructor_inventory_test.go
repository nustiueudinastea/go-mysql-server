// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package function

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every argument evaluation in a function constructor must be reviewed.
// UNIX_TIMESTAMP reads literal metadata through Value; UTC_TIMESTAMP must guard Eval.
// Counts distinguish repeated calls in a function. New calls (including method
// values) fail this inventory until their validation or exemption is classified.
func TestConstructorEvalInventory(t *testing.T) {
	const validated = "validated immediately before evaluation"
	want := map[string][]string{
		"utc_timestamp.go:NewUTCTimestamp:args[0].Eval": {validated},
	}
	// Follow constructor calls to package-local helpers, so moving an Eval
	// into a helper cannot evade the inventory.
	functions := make(map[string]*ast.FuncDecl)
	reachable := make(map[string]bool)
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "jsontests") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				functions[filepath.Dir(path)+":"+fn.Name.Name] = fn
			}
		}
		return nil
	})
	require.NoError(t, err)
	var visit func(string)
	visit = func(key string) {
		if reachable[key] {
			return
		}
		fn := functions[key]
		if fn == nil {
			return
		}
		reachable[key] = true
		dir := key[:strings.LastIndex(key, ":")]
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.Ident); ok {
					visit(dir + ":" + fn.Name)
				}
			}
			return true
		})
	}
	for key, fn := range functions {
		if strings.HasPrefix(fn.Name.Name, "New") || strings.HasPrefix(fn.Name.Name, "new") {
			visit(key)
		}
	}
	found := make(map[string][]string)
	fset := token.NewFileSet()
	for _, root := range []string{"."} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			require.NoError(t, err)
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "jsontests") {
				return nil
			}
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			file, err := parser.ParseFile(fset, path, src, 0)
			require.NoError(t, err)
			base, err := filepath.Abs(".")
			require.NoError(t, err)
			abs, err := filepath.Abs(path)
			require.NoError(t, err)
			rel, err := filepath.Rel(base, abs)
			require.NoError(t, err)
			for _, decl := range file.Decls {
				name := "<package>"
				var body ast.Node = decl
				if fn, ok := decl.(*ast.FuncDecl); ok {
					if fn.Body == nil {
						continue
					}
					if fn.Recv != nil || !reachable[filepath.Dir(path)+":"+fn.Name.Name] {
						continue
					}
					name, body = fn.Name.Name, fn.Body
				}
				// Check the immediate preceding statement as well as recording the call.
				guarded := make(map[token.Pos]bool)
				ast.Inspect(body, func(n ast.Node) bool {
					var stmts []ast.Stmt
					switch n := n.(type) {
					case *ast.BlockStmt:
						stmts = n.List
					case *ast.CaseClause:
						stmts = n.Body
					case *ast.CommClause:
						stmts = n.Body
					}
					for i, stmt := range stmts {
						if i == 0 {
							continue
						}
						previous := stmts[i-1]
						text := string(src[fset.Position(previous.Pos()).Offset:fset.Position(previous.End()).Offset])
						if strings.Contains(text, "ValidateExpression(") {
							ast.Inspect(stmt, func(n ast.Node) bool {
								if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Eval" {
									guarded[sel.Pos()] = true
								}
								return true
							})
						}
					}
					return true
				})
				ast.Inspect(body, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Eval" {
						return true
					}
					var buf bytes.Buffer
					require.NoError(t, format.Node(&buf, fset, sel))
					key := filepath.ToSlash(rel) + ":" + name + ":" + buf.String()
					classification := "UNCLASSIFIED"
					if entries := want[key]; len(found[key]) < len(entries) {
						classification = entries[len(found[key])]
					}
					if classification == validated {
						require.True(t, guarded[sel.Pos()], "%s needs an immediate validation guard", key)
					}
					found[key] = append(found[key], classification)
					return true
				})
			}
			return nil
		})
		require.NoError(t, err)
	}
	require.Equal(t, want, found, "classify every new Eval reference and guard planning-time expression evaluations")
}
