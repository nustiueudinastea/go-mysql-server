// Copyright 2023-2024 Dolthub, Inc.
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
	"context"
	"fmt"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
)

func TestExactNumericAggregates(t *testing.T) {
	for _, test := range []struct {
		name, typ, values, sum, avg, distinctSum, distinctAvg string
		sumPrecision, avgPrecision, scale                     uint8
	}{
		{"int", "INT", "1),(1),(2", "4", "1.3333", "3", "1.5000", 32, 14, 0},
		{"unsigned bigint", "BIGINT UNSIGNED", "18446744073709551614),(18446744073709551614),(18446744073709551615", "55340232221128654843", "18446744073709551614.3333", "36893488147419103229", "18446744073709551614.5000", 42, 24, 0},
		{"decimal", "DECIMAL(10,3)", "123.001),(123.001),(0.002", "246.004", "82.0013333", "123.003", "61.5015000", 32, 14, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := memory.NewDatabase("db")
			provider := memory.NewDBProvider(db)
			engine := NewDefault(provider)
			defer engine.Close()
			ctx := sql.NewContext(context.Background(), sql.WithSession(memory.NewSession(sql.NewBaseSession(), provider)))
			ctx.SetCurrentDatabase("db")
			query := func(t *testing.T, q string) (sql.Schema, []sql.Row) {
				schema, iter, _, err := engine.Query(ctx, q)
				require.NoError(t, err, q)
				rows, err := sql.RowIterToRows(ctx, iter)
				require.NoError(t, err, q)
				return schema, rows
			}
			query(t, "CREATE TABLE numbers (v "+test.typ+")")
			query(t, "INSERT INTO numbers VALUES ("+test.values+"),(NULL)")
			for _, agg := range []struct {
				name, expected   string
				precision, scale uint8
			}{
				{"SUM(v)", test.sum, test.sumPrecision, test.scale},
				{"AVG(v)", test.avg, test.avgPrecision, test.scale + 4},
				{"SUM(DISTINCT v)", test.distinctSum, test.sumPrecision, test.scale},
				{"AVG(DISTINCT v)", test.distinctAvg, test.avgPrecision, test.scale + 4},
			} {
				t.Run(agg.name, func(t *testing.T) {
					for _, suffix := range []string{"", " WHERE v IS NOT NULL GROUP BY v IS NULL"} {
						schema, rows := query(t, "SELECT "+agg.name+" FROM numbers"+suffix)
						require.Len(t, rows, 1)
						assertNumericAggregate(t, schema[0], rows[0][0], agg.precision, agg.scale, agg.expected)
					}
					_, rows := query(t, "SELECT "+agg.name+" FROM numbers WHERE v IS NULL")
					require.Len(t, rows, 1)
					require.Nil(t, rows[0][0])
				})
			}
			for _, agg := range []struct {
				name, expected   string
				precision, scale uint8
			}{
				{"SUM(v)", test.sum, test.sumPrecision, test.scale},
				{"AVG(v)", test.avg, test.avgPrecision, test.scale + 4},
			} {
				t.Run(agg.name+" OVER", func(t *testing.T) {
					schema, rows := query(t, "SELECT "+agg.name+" OVER () FROM numbers")
					require.Len(t, rows, 4)
					for _, row := range rows {
						assertNumericAggregate(t, schema[0], row[0], agg.precision, agg.scale, agg.expected)
					}
					// Exercise prefix subtraction, a nonzero partition offset, and a NULL-only frame.
					schema, rows = query(t, "SELECT "+agg.name+" OVER (PARTITION BY v IS NULL ORDER BY v ROWS BETWEEN CURRENT ROW AND CURRENT ROW), v FROM numbers ORDER BY v")
					require.Len(t, rows, 4)
					require.Nil(t, rows[0][0])
					for _, row := range rows[1:] {
						expected, _, err := apd.NewFromString(fmt.Sprint(row[1]))
						require.NoError(t, err)
						result, ok := row[0].(*apd.Decimal)
						require.True(t, ok)
						require.Zero(t, result.Cmp(expected))
					}
				})
			}
		})
	}
}

func assertNumericAggregate(t *testing.T, col *sql.Column, value interface{}, precision, scale uint8, expected string) {
	t.Helper()
	typ, ok := col.Type.(sql.DecimalType)
	require.True(t, ok, "type: %v", col.Type)
	require.Equal(t, precision, typ.Precision())
	require.Equal(t, scale, typ.Scale())
	result, ok := value.(*apd.Decimal)
	require.True(t, ok, "value: %T %v", value, value)
	want, _, err := apd.NewFromString(expected)
	require.NoError(t, err)
	require.Zero(t, result.Cmp(want), "got %s, want %s", result, want)
}
