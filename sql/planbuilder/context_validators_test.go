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

package planbuilder

import (
	"errors"
	"testing"

	ast "github.com/dolthub/vitess/go/vt/sqlparser"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func TestContextValidatorAfterBindTypeFixup(t *testing.T) {
	ctx := sql.NewEmptyContext()
	b := New(ctx, nil, nil)
	s := b.newScope()
	s.newColumn(scopeColumn{table: "t", col: "v", typ: types.Int64})
	rejected := errors.New("forbidden typed comparison")
	seen := false
	ctx.SetValidators(nil, func(ctx *sql.Context, e sql.Expression) error {
		if eq, ok := e.(*expression.Equals); ok {
			require.True(t, eq.Right().Type(ctx).Equals(types.Int64))
			seen = true
			return rejected
		}
		return nil
	})
	func() {
		defer func() {
			r := recover()
			require.IsType(t, parseErr{}, r)
			require.ErrorIs(t, r.(parseErr).err, rejected)
		}()
		b.buildScalar(s, &ast.ComparisonExpr{Operator: ast.EqualStr, Left: &ast.ColName{Name: ast.NewColIdent("v")}, Right: ast.NewValArg([]byte(":v"))})
	}()
	require.True(t, seen)
}
