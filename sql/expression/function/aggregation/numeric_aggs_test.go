// Copyright 2020-2021 Dolthub, Inc.
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

package aggregation

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func TestNumericAggregateTypes(t *testing.T) {
	ctx := sql.NewEmptyContext()
	for _, test := range []struct {
		typ                                            sql.Type
		sumPrecision, avgPrecision, sumScale, avgScale uint8
	}{
		{types.Int8, 25, 7, 0, 4},
		{types.Uint8, 25, 7, 0, 4},
		{types.Int16, 27, 9, 0, 4},
		{types.Uint16, 27, 9, 0, 4},
		{types.Int24, 29, 11, 0, 4},
		{types.Uint24, 30, 12, 0, 4},
		{types.Int32, 32, 14, 0, 4},
		{types.Uint32, 32, 14, 0, 4},
		{types.Int64, 41, 23, 0, 4},
		{types.Uint64, 42, 24, 0, 4},
		{types.MustCreateDecimalType(10, 3), 32, 14, 3, 7},
		{types.MustCreateDecimalType(65, 30), 65, 65, 30, 30},
	} {
		t.Run(test.typ.String(), func(t *testing.T) {
			field := expression.NewGetField(0, test.typ, "v", true)
			for _, child := range []sql.Expression{field, expression.NewDistinctExpression(field)} {
				sum, ok := NewSum(child).Type(ctx).(sql.DecimalType)
				require.True(t, ok)
				avg, ok := NewAvg(child).Type(ctx).(sql.DecimalType)
				require.True(t, ok)
				require.Equal(t, test.sumPrecision, sum.Precision())
				require.Equal(t, test.avgPrecision, avg.Precision())
				require.Equal(t, test.sumScale, sum.Scale())
				require.Equal(t, test.avgScale, avg.Scale())
			}
		})
	}
	for _, typ := range []sql.Type{types.Float32, types.Float64, types.Text} {
		t.Run(typ.String(), func(t *testing.T) {
			child := expression.NewGetField(0, typ, "v", true)
			for _, agg := range []sql.Aggregation{NewSum(child), NewAvg(child)} {
				require.Equal(t, types.Float64, agg.Type(ctx))
				buffer, err := agg.NewBuffer(ctx)
				require.NoError(t, err)
				for _, v := range []interface{}{float32(1.5), float64(2.5), "3.5"} {
					require.NoError(t, buffer.Update(ctx, sql.Row{v}))
				}
				result, err := buffer.Eval(ctx)
				require.NoError(t, err)
				require.IsType(t, float64(0), result)
			}
		})
	}
	require.Equal(t, "decimal(24,0)", NewSum(expression.NewLiteral(int64(71), types.Int64)).Type(ctx).String())
}

func TestExactAveragePrecisionIncrement(t *testing.T) {
	ctx := sql.NewEmptyContext()
	require.NoError(t, ctx.SetSessionVariable(ctx, "div_precision_increment", int64(6)))
	child := expression.NewGetField(0, types.MustCreateDecimalType(10, 3), "v", true)
	avg := NewAvg(child)
	require.Equal(t, "decimal(16,9)", avg.Type(ctx).String())
	buffer, err := avg.NewBuffer(ctx)
	require.NoError(t, err)
	for _, value := range []interface{}{"1.000", "0.000", "0.000"} {
		require.NoError(t, buffer.Update(ctx, sql.Row{value}))
	}
	result, err := buffer.Eval(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.333333333", result.(*apd.Decimal).Text('f'))
}
