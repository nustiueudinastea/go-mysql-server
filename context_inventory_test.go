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

package sqle

import (
	"github.com/stretchr/testify/require"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review every fresh SQL context. A caller's execution policies must survive;
// independent entry points and helpers that cannot execute SQL are explicit exemptions.
func TestFreshContextInventory(t *testing.T) {
	root := "."
	const inherits = "propagates caller policies"
	want := map[string][]string{
		"engine.go:New":                                          {"engine initialization; no caller"},
		"driver/conn.go:newContextWithQuery":                     {inherits},
		"driver/context.go:NewContext":                           {inherits},
		"server/handler.go:ComRegisterReplica":                   {"replication registration; no SQL caller"},
		"server/handler.go:ComBinlogDumpGTID":                    {"replication stream; no SQL caller"},
		"sql/session.go:NewEmptyContext":                         {"independent context API; no caller"},
		"sql/procedures/interpreter_logic.go:Call":               {inherits},
		"sql/information_schema/information_schema.go:<package>": {"type metadata only"},
		"sql/variables/system_variables.go:AssignValues":         {"system variable initialization; no caller"},
		"sql/plan/dual.go:IsDualTable":                           {"fixed dual schema metadata only"},
		"sql/plan/dual.go:<package>":                             {"fixed dual table construction only"},
		"sql/range_tree.go:String":                               {"debug formatting only"},
	}
	found := make(map[string][]string)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		require.NoError(t, err)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "enginetest/") || strings.Contains(rel, "_example/") || strings.Contains(rel, "/jsontests/") || strings.HasPrefix(rel, "server/golden/") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			name := "<package>"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				name = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				fresh := false
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					fresh = fn.Name == "NewContext" || fn.Name == "NewEmptyContext"
				case *ast.SelectorExpr:
					fresh = fn.Sel.Name == "NewContext" || fn.Sel.Name == "NewEmptyContext"
				}
				if !fresh {
					return true
				}
				key := rel + ":" + name
				entries := want[key]
				classification := "UNCLASSIFIED"
				if len(found[key]) < len(entries) {
					classification = entries[len(found[key])]
				}
				if classification == inherits {
					require.NotEmpty(t, call.Args, key)
					arg, ok := call.Args[0].(*ast.Ident)
					require.True(t, ok, "%s must pass the caller itself, not its embedded context", key)
					if ok {
						require.Contains(t, []string{"ctx", "subCtx"}, arg.Name, key)
					}
				}
				found[key] = append(found[key], classification)
				return true
			})
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, want, found, "classify every new context creation site")
}
