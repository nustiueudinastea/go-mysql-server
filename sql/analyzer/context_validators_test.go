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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/types"
)

type validationEvalCounter struct {
	*expression.Literal
	calls *int
}

func (e *validationEvalCounter) Eval(*sql.Context, sql.Row) (interface{}, error) {
	*e.calls++
	return int64(1), nil
}

func TestContextValidatorsBeforeDefaultEvaluation(t *testing.T) {
	for _, target := range []string{"normalize", "type", "enum"} {
		t.Run(target, func(t *testing.T) {
			ctx := sql.NewEmptyContext()
			calls := 0
			e := &validationEvalCounter{expression.NewLiteral(int64(1), types.Int64), &calls}
			def := &sql.ColumnDefaultValue{Expr: e, OutType: types.Int64, Literal: true}
			rejected := errors.New("forbidden default")
			ctx.SetValidators(nil, func(*sql.Context, sql.Expression) error { return rejected })
			var err error
			switch target {
			case "normalize":
				_, _, err = normalizeDefault(ctx, def)
			case "type":
				err = def.CheckType(ctx)
			case "enum":
				err = validateEnumLiteralDefault(types.MustCreateEnumType([]string{"one"}, sql.Collation_Default), def, "v", ctx)
			}
			require.Zero(t, calls)
			require.ErrorIs(t, err, rejected)
		})
	}
}

func TestContextValidatorsBeforeIndexEvaluation(t *testing.T) {
	for _, op := range []sql.IndexScanOp{sql.IndexScanOpEq, sql.IndexScanOpInSet} {
		t.Run(op.String(), func(t *testing.T) {
			ctx := sql.NewEmptyContext()
			calls := 0
			var e sql.Expression = &validationEvalCounter{expression.NewLiteral(int64(1), types.Int64), &calls}
			if op == sql.IndexScanOpInSet {
				e = expression.Tuple{e, e}
			}
			rejected := errors.New("forbidden index bound")
			ctx.SetValidators(nil, func(*sql.Context, sql.Expression) error { return rejected })
			c := newIndexCoster("t")
			_, ok := c.buildLeafFromParts(ctx, 0, "id", types.Int64, op, e)
			require.Zero(t, calls)
			require.False(t, ok)
		})
	}
}

func TestContextValidatorsBeforeLimitAndOffsetEvaluation(t *testing.T) {
	ctx := sql.NewEmptyContext()
	rejected := errors.New("forbidden limit or offset")
	ctx.SetValidators(nil, func(*sql.Context, sql.Expression) error { return rejected })
	lit := expression.NewLiteral(int64(1), types.Int64)
	for _, n := range []sql.Node{plan.NewLimit(lit, plan.NewEmptyTableWithSchema(nil)), plan.NewOffset(lit, plan.NewEmptyTableWithSchema(nil))} {
		_, _, err := validateOffsetAndLimit(ctx, nil, n, nil, nil, nil)
		require.ErrorIs(t, err, rejected)
	}
}
