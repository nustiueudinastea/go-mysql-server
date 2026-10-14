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

package analyzer

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

// Every production Eval reference in analyzer and planbuilder must be reviewed.
// Include CheckType, which evaluates a default indirectly from the analyzer.
// Counts distinguish repeated calls in a function. New calls (including method
// values) fail this inventory until their validation or exemption is classified.
func TestPlanningEvalInventory(t *testing.T) {
	const validated = "validated immediately before evaluation"
	want := map[string][]string{
		"analyzer/analyzer.go:analyzeWithSelector:batch.Eval":                                 {"batch runner, not expression evaluation"},
		"analyzer/costed_index_scan.go:buildLeafFromParts:lit.Eval":                           {validated},
		"analyzer/costed_index_scan.go:buildLeafFromParts:litExpr.Eval":                       {validated},
		"analyzer/optimization_rules.go:simplifyExpression:e.Eval":                            {validated},
		"analyzer/resolve_column_defaults.go:validateEnumLiteralDefault:colDefault.Expr.Eval": {validated},
		"analyzer/resolve_column_defaults.go:normalizeDefault:colDefault.Eval":                {validated},
		"analyzer/validation_rules.go:validateOffsetAndLimit:e.Eval":                          {validated, validated},
		"planbuilder/process.go:getInt64Value:ie.Eval":                                        {validated},
		"planbuilder/proc.go:BuildProcedureHelper:asOf.Eval":                                  {validated},
		"planbuilder/scalar.go:buildUnaryScalar:expr.Eval":                                    {validated},
		"planbuilder/select.go:buildOffset:lit.Eval":                                          {validated},
		"planbuilder/set.go:simplifySetExpr:e.Eval":                                           {validated, validated, validated},
		"planbuilder/show.go:buildAsOfLit:expr.Eval":                                          {validated},
		"columndefault.go:CheckType:e.Expr.Eval":                                              {validated},
		"columndefault.go:Eval:e.Expr.Eval":                                                   {"runtime default evaluation; planning validates before calling ColumnDefaultValue.Eval"},
	}
	found := make(map[string][]string)
	fset := token.NewFileSet()
	for _, root := range []string{".", "../planbuilder", "../columndefault.go"} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			require.NoError(t, err)
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			file, err := parser.ParseFile(fset, path, src, 0)
			require.NoError(t, err)
			base, err := filepath.Abs("..")
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
