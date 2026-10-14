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

package rowexec

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func TestJoinSchema(t *testing.T) {
	ctx := sql.NewEmptyContext()
	db := memory.NewDatabase("test")
	t1 := plan.NewResolvedTable(memory.NewTable(ctx, db.BaseDatabase, "foo", sql.NewPrimaryKeySchema(sql.Schema{
		{Name: "a", Source: "foo", Type: types.Int64},
	}), nil), nil, nil)

	t2 := plan.NewResolvedTable(memory.NewTable(ctx, db.BaseDatabase, "bar", sql.NewPrimaryKeySchema(sql.Schema{
		{Name: "b", Source: "bar", Type: types.Int64},
	}), nil), nil, nil)

	t.Run("inner", func(t *testing.T) {
		j := plan.NewInnerJoin(ctx, t1, t2, nil)
		result := j.Schema(ctx)

		require.Equal(t, sql.Schema{
			{Name: "a", Source: "foo", Type: types.Int64},
			{Name: "b", Source: "bar", Type: types.Int64},
		}, result)
	})

	t.Run("left", func(t *testing.T) {
		j := plan.NewLeftOuterJoin(ctx, t1, t2, nil)
		result := j.Schema(ctx)

		require.Equal(t, sql.Schema{
			{Name: "a", Source: "foo", Type: types.Int64},
			{Name: "b", Source: "bar", Type: types.Int64, Nullable: true},
		}, result)
	})

	t.Run("right", func(t *testing.T) {
		j := plan.NewRightOuterJoin(ctx, t1, t2, nil)
		result := j.Schema(ctx)

		require.Equal(t, sql.Schema{
			{Name: "a", Source: "foo", Type: types.Int64, Nullable: true},
			{Name: "b", Source: "bar", Type: types.Int64},
		}, result)
	})
}

func TestInnerJoin(t *testing.T) {
	db := memory.NewDatabase("test")
	pro := memory.NewDBProvider(db)
	ctx := newContext(pro)
	testInnerJoin(t, db, ctx)
}

func TestMultiPassInnerJoin(t *testing.T) {
	db := memory.NewDatabase("test")
	pro := memory.NewDBProvider(db)
	ctx := sql.NewContext(context.TODO(), sql.WithMemoryManager(
		sql.NewMemoryManager(mockReporter{2, 1}),
	), sql.WithSession(memory.NewSession(sql.NewBaseSession(), pro)))
	testInnerJoin(t, db, ctx)
}

func testInnerJoin(t *testing.T, db *memory.Database, ctx *sql.Context) {
	t.Helper()

	require := require.New(t)

	ltable := memory.NewTable(ctx, db.BaseDatabase, "left", lSchema, nil)
	rtable := memory.NewTable(ctx, db.BaseDatabase, "right", rSchema, nil)
	insertData(t, ctx, ltable)
	insertData(t, ctx, rtable)

	j := plan.NewInnerJoin(
		ctx,
		plan.NewResolvedTable(ltable, nil, nil),
		plan.NewResolvedTable(rtable, nil, nil),
		expression.NewEquals(
			expression.NewGetField(0, types.Text, "lcol1", false),
			expression.NewGetField(4, types.Text, "rcol1", false),
		))

	rows := collectRows(t, ctx, j)
	require.Len(rows, 2)

	require.Equal([]sql.Row{
		{"col1_1", "col2_1", int32(1), int64(2), "col1_1", "col2_1", int32(1), int64(2)},
		{"col1_2", "col2_2", int32(3), int64(4), "col1_2", "col2_2", int32(3), int64(4)},
	}, rows)
}

func TestInnerJoinEmpty(t *testing.T) {
	require := require.New(t)

	db := memory.NewDatabase("test")
	pro := memory.NewDBProvider(db)
	ctx := newContext(pro)

	ltable := memory.NewTable(ctx, db.BaseDatabase, "left", lSchema, nil)
	rtable := memory.NewTable(ctx, db.BaseDatabase, "right", rSchema, nil)

	j := plan.NewInnerJoin(
		ctx,
		plan.NewResolvedTable(ltable, nil, nil),
		plan.NewResolvedTable(rtable, nil, nil),
		expression.NewEquals(
			expression.NewGetField(0, types.Text, "lcol1", false),
			expression.NewGetField(4, types.Text, "rcol1", false),
		))

	iter, err := DefaultBuilder.Build(ctx, j, nil)
	require.NoError(err)

	assertRows(t, ctx, iter, 0)
}

func BenchmarkInnerJoin(b *testing.B) {
	db := memory.NewDatabase("test")
	pro := memory.NewDBProvider(db)
	ctx := newContext(pro)

	t1 := memory.NewTable(sql.NewEmptyContext(), db.BaseDatabase, "foo", sql.NewPrimaryKeySchema(sql.Schema{
		{Name: "a", Source: "foo", Type: types.Int64},
		{Name: "b", Source: "foo", Type: types.Text},
	}), nil)

	t2 := memory.NewTable(sql.NewEmptyContext(), db.BaseDatabase, "bar", sql.NewPrimaryKeySchema(sql.Schema{
		{Name: "a", Source: "bar", Type: types.Int64},
		{Name: "b", Source: "bar", Type: types.Text},
	}), nil)

	for i := 0; i < 5; i++ {
		t1.Insert(sql.NewEmptyContext(), sql.NewRow(int64(i), fmt.Sprintf("t1_%d", i)))
		t2.Insert(sql.NewEmptyContext(), sql.NewRow(int64(i), fmt.Sprintf("t2_%d", i)))
	}

	n1 := plan.NewInnerJoin(
		ctx,
		plan.NewResolvedTable(t1, nil, nil),
		plan.NewResolvedTable(t2, nil, nil),
		expression.NewEquals(
			expression.NewGetField(0, types.Int64, "a", false),
			expression.NewGetField(2, types.Int64, "a", false),
		),
	)

	n2 := plan.NewFilter(
		ctx,
		expression.NewEquals(
			expression.NewGetField(0, types.Int64, "a", false),
			expression.NewGetField(2, types.Int64, "a", false),
		),
		plan.NewCrossJoin(
			ctx,
			plan.NewResolvedTable(t1, nil, nil),
			plan.NewResolvedTable(t2, nil, nil),
		),
	)

	expected := []sql.Row{
		{int64(0), "t1_0", int64(0), "t2_0"},
		{int64(1), "t1_1", int64(1), "t2_1"},
		{int64(2), "t1_2", int64(2), "t2_2"},
		{int64(3), "t1_3", int64(3), "t2_3"},
		{int64(4), "t1_4", int64(4), "t2_4"},
	}

	ctx = sql.NewContext(context.Background(), sql.WithMemoryManager(
		sql.NewMemoryManager(mockReporter{1, 5}),
	), sql.WithSession(memory.NewSession(sql.NewBaseSession(), pro)))

	b.Run("inner join", func(b *testing.B) {
		require := require.New(b)

		for i := 0; i < b.N; i++ {
			iter, err := DefaultBuilder.Build(ctx, n1, nil)
			require.NoError(err)

			rows, err := sql.RowIterToRows(ctx, iter)
			require.NoError(err)

			require.Equal(expected, rows)
		}
	})

	b.Run("within memory threshold", func(b *testing.B) {
		require := require.New(b)

		for i := 0; i < b.N; i++ {
			iter, err := DefaultBuilder.Build(ctx, n1, nil)
			require.NoError(err)

			rows, err := sql.RowIterToRows(ctx, iter)
			require.NoError(err)

			require.Equal(expected, rows)
		}
	})

	b.Run("cross join with filter", func(b *testing.B) {
		require := require.New(b)

		for i := 0; i < b.N; i++ {
			iter, err := DefaultBuilder.Build(ctx, n2, nil)
			require.NoError(err)

			rows, err := sql.RowIterToRows(ctx, iter)
			require.NoError(err)

			require.Equal(expected, rows)
		}
	})
}

func TestLeftJoin(t *testing.T) {
	require := require.New(t)

	db := memory.NewDatabase("test")
	pro := memory.NewDBProvider(db)
	ctx := newContext(pro)

	ltable := memory.NewTable(ctx, db.BaseDatabase, "left", lSchema, nil)
	rtable := memory.NewTable(ctx, db.BaseDatabase, "right", rSchema, nil)

	insertData(t, newContext(pro), ltable)
	insertData(t, newContext(pro), rtable)

	j := plan.NewLeftOuterJoin(
		ctx,
		plan.NewResolvedTable(ltable, nil, nil),
		plan.NewResolvedTable(rtable, nil, nil),
		expression.NewEquals(
			expression.NewPlus(
				expression.NewGetField(2, types.Text, "lcol3", false),
				expression.NewLiteral(int32(2), types.Int32),
			),
			expression.NewGetField(6, types.Text, "rcol3", false),
		))

	iter, err := DefaultBuilder.Build(ctx, j, nil)
	require.NoError(err)
	rows, err := sql.RowIterToRows(ctx, iter)
	require.NoError(err)
	require.ElementsMatch([]sql.Row{
		{"col1_1", "col2_1", int32(1), int64(2), "col1_2", "col2_2", int32(3), int64(4)},
		{"col1_2", "col2_2", int32(3), int64(4), nil, nil, nil, nil},
	}, rows)
}

type mockReporter struct {
	val uint64
	max uint64
}

func (m mockReporter) UsedMemory() uint64 { return m.val }
func (m mockReporter) MaxMemory() uint64  { return m.max }

func TestAntiHashNotInCachedMiss(t *testing.T) {
	// Execute WHERE (a,b) NOT IN (SELECT a,b FROM rhs) with an explicit
	// AntiHash plan. The first LHS row exhausts the RHS and primes its cache.
	for _, test := range []struct {
		name                  string
		left, right, expected []sql.Row
	}{
		{
			name:     "NULL in RHS",
			left:     []sql.Row{{int64(3), int64(3)}, {int64(1), int64(3)}},
			right:    []sql.Row{{int64(1), nil}, {int64(2), int64(2)}},
			expected: []sql.Row{{int64(3), int64(3)}},
		},
		{
			name:     "NULL in LHS",
			left:     []sql.Row{{int64(3), int64(3)}, {nil, int64(2)}},
			right:    []sql.Row{{int64(1), int64(1)}, {int64(2), int64(2)}},
			expected: []sql.Row{{int64(3), int64(3)}},
		},
		{
			name:     "unrelated NULL",
			left:     []sql.Row{{int64(3), int64(3)}, {int64(1), int64(3)}},
			right:    []sql.Row{{int64(1), int64(2)}, {int64(2), nil}},
			expected: []sql.Row{{int64(3), int64(3)}, {int64(1), int64(3)}},
		},
		{
			name:     "cached hit",
			left:     []sql.Row{{int64(3), int64(3)}, {int64(2), int64(2)}},
			right:    []sql.Row{{int64(1), nil}, {int64(2), int64(2)}},
			expected: []sql.Row{{int64(3), int64(3)}},
		},
		{
			name:     "empty RHS",
			left:     []sql.Row{{int64(3), int64(3)}, {nil, int64(2)}},
			expected: []sql.Row{{int64(3), int64(3)}, {nil, int64(2)}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for iteration := 0; iteration < 100; iteration++ {
				ctx := sql.NewEmptyContext()
				values := func(rows []sql.Row) *plan.Values {
					tuples := make([][]sql.Expression, len(rows))
					for i, row := range rows {
						tuples[i] = []sql.Expression{
							expression.NewLiteral(row[0], types.Int64),
							expression.NewLiteral(row[1], types.Int64),
						}
					}
					return plan.NewValues(tuples)
				}
				key := func(offset int) sql.Expression {
					return expression.NewTuple(
						expression.NewGetField(offset, types.Int64, "a", true),
						expression.NewGetField(offset+1, types.Int64, "b", true),
					)
				}
				cached := plan.NewCachedResults(values(test.right))
				lookup := plan.NewHashLookup(ctx, cached, key(0), key(0), plan.JoinTypeAntiHash)
				join := plan.NewJoin(ctx, values(test.left), lookup, plan.JoinTypeAntiHash,
					expression.NewEquals(key(0), key(2)))
				iter, err := DefaultBuilder.Build(ctx, join, nil)
				require.NoError(t, err)
				rows, err := sql.RowIterToRows(ctx, iter)
				require.NoError(t, err)
				require.NotNil(t, lookup.Lookup, "first probe must populate the hash cache")
				require.True(t, cached.IsFinalized())
				require.Equal(t, test.expected, rows, "iteration %d", iteration)
			}
		})
	}
}

func TestAntiHashNotInCachedMissReplay(t *testing.T) {
	const rightSize, probes = 10000, 128
	for _, tuple := range []bool{false, true} {
		for _, withNulls := range []bool{false, true} {
			t.Run(fmt.Sprintf("tuple=%t/NULLs=%t", tuple, withNulls), func(t *testing.T) {
				ctx := sql.NewEmptyContext()
				var right, nullRows []sql.Row
				var tuples [][]sql.Expression
				for i := 0; i < rightSize; i++ {
					row := sql.Row{int64(i), int64(i)}
					if i == 43 {
						row = sql.Row{int64(42), int64(42)} // A hit must retain duplicates.
					}
					if withNulls && (i == 0 || i == 2500 || i == 5000 || i == rightSize-1) {
						if tuple && i%2 == 1 {
							row[1] = nil
						} else {
							row[0] = nil
						}
						nullRows = append(nullRows, row)
					}
					right = append(right, row)
					tuples = append(tuples, []sql.Expression{
						expression.NewLiteral(row[0], types.Int64),
						expression.NewLiteral(row[1], types.Int64),
					})
				}
				var key sql.Expression = expression.NewGetField(0, types.Int64, "a", true)
				if tuple {
					key = expression.NewTuple(key, expression.NewGetField(1, types.Int64, "b", true))
				}
				cached := plan.NewCachedResults(plan.NewValues(tuples))
				lookup := plan.NewHashLookup(ctx, cached, key, key, plan.JoinTypeAntiHash)
				// Drain each lookup iterator to count all candidates made available
				// per LHS probe, independently of the join's early rejection on NULL.
				probe := func(t *testing.T, left sql.Row) []sql.Row {
					t.Helper()
					iter, err := DefaultBuilder.Build(ctx, lookup, left)
					require.NoError(t, err)
					rows, err := sql.RowIterToRows(ctx, iter)
					require.NoError(t, err)
					return rows
				}
				// An abandoned first probe must not publish a partial NULL bucket.
				iter, err := DefaultBuilder.Build(ctx, lookup, sql.Row{int64(rightSize), int64(rightSize)})
				require.NoError(t, err)
				_, err = iter.Next(ctx)
				require.NoError(t, err)
				require.NoError(t, iter.Close(ctx))
				require.Nil(t, lookup.Lookup)
				require.Equal(t, right, probe(t, sql.Row{int64(rightSize), int64(rightSize)}))
				require.NotNil(t, lookup.Lookup)
				require.True(t, cached.IsFinalized())

				t.Run("misses", func(t *testing.T) {
					replayed := 0
					for i := 0; i < probes; i++ {
						rows := probe(t, sql.Row{int64(rightSize + i), int64(rightSize + i)})
						replayed += len(rows)
						require.LessOrEqual(t, len(rows), len(nullRows), "probe %d replayed too many RHS rows", i)
						require.Len(t, rows, len(nullRows), "probe %d omitted NULL-key rows", i)
						require.Equal(t, nullRows, rows, "probe %d must replay every NULL-key row in order", i)
					}
					t.Logf("%d cached misses replayed %d rows total; %d per probe (RHS=%d)", probes, replayed, len(nullRows), rightSize)
				})
				t.Run("hit", func(t *testing.T) {
					require.Equal(t, []sql.Row{right[42], right[43]}, probe(t, sql.Row{int64(42), int64(42)}))
				})
				t.Run("NULL probe", func(t *testing.T) {
					left := []sql.Row{{nil, int64(rightSize)}, {nil, int64(0)}}
					if tuple {
						left = append(left, sql.Row{int64(rightSize), nil})
					}
					for _, row := range left {
						rows := probe(t, row)
						require.Len(t, rows, rightSize)
						require.Equal(t, right, rows)
						t.Logf("NULL probe %v replayed %d rows", row, len(rows))
					}
				})
			})
		}
	}
}
