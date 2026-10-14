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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/memo"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"
)

type countingPlanCoster struct {
	memo.Coster
	calls int
	fail  bool
}

func (c *countingPlanCoster) EstimateCost(ctx *sql.Context, rel memo.RelExpr, stats sql.StatsProvider) (float64, error) {
	c.calls++
	if c.fail {
		return 0, errors.New("unexpected memo costing")
	}
	return c.Coster.EstimateCost(ctx, rel, stats)
}

type countingPlanStats struct {
	sql.StatsProvider
	calls int
	fail  bool
}

func (s *countingPlanStats) GetTableStats(ctx *sql.Context, schema, db string, table sql.Table) ([]sql.Statistic, error) {
	s.calls++
	if s.fail {
		return nil, errors.New("unexpected index costing")
	}
	return s.StatsProvider.GetTableStats(ctx, schema, db, table)
}

func TestDisableCostBasedPlans(t *testing.T) {
	for _, test := range []struct {
		name, query, verify string
		expected            []sql.Row
	}{
		{"simple select", "SELECT id FROM t WHERE v >= 20 ORDER BY id", "", []sql.Row{{int32(2)}, {int32(3)}}},
		{"update", "UPDATE t SET v = v + 1 WHERE v >= 20", "SELECT v FROM t ORDER BY id", []sql.Row{{int32(10)}, {int32(21)}, {int32(31)}}},
		{"delete", "DELETE FROM t WHERE v >= 20", "SELECT id FROM t ORDER BY id", []sql.Row{{int32(1)}}},
		{"inner join", "SELECT t.id, u.v FROM t JOIN u ON t.id = u.id WHERE t.v >= 10 ORDER BY t.id", "", []sql.Row{{int32(1), int32(100)}, {int32(2), int32(200)}}},
		{"left join", "SELECT t.id, u.v FROM t LEFT JOIN u ON t.id = u.id ORDER BY t.id", "", []sql.Row{{int32(1), int32(100)}, {int32(2), int32(200)}, {int32(3), nil}}},
		{"right join", "SELECT u.id, t.v FROM t RIGHT JOIN u ON t.id = u.id ORDER BY u.id", "", []sql.Row{{int32(1), int32(10)}, {int32(2), int32(20)}, {int32(4), nil}}},
		{"using join", "SELECT id FROM t JOIN u USING (id) ORDER BY id", "", []sql.Row{{int32(1)}, {int32(2)}}},
		{"in subquery", "SELECT id FROM t WHERE id IN (SELECT id FROM u WHERE v >= 200) ORDER BY id", "", []sql.Row{{int32(2)}}},
		{"not in subquery", "SELECT id FROM t WHERE id NOT IN (SELECT id FROM u) ORDER BY id", "", []sql.Row{{int32(3)}}},
		{"correlated subquery", "SELECT id, (SELECT u.v FROM u WHERE u.id = t.id) FROM t ORDER BY id", "", []sql.Row{{int32(1), int32(100)}, {int32(2), int32(200)}, {int32(3), nil}}},
		{"exists subquery", "SELECT id FROM t WHERE EXISTS (SELECT 1 FROM u WHERE u.id = t.id) ORDER BY id", "", []sql.Row{{int32(1)}, {int32(2)}}},
		{"insert select", "INSERT INTO dst SELECT id, v FROM t WHERE v >= 20", "SELECT id, v FROM dst ORDER BY id", []sql.Row{{int32(2), int32(20)}, {int32(3), int32(30)}}},
		{"insert join", "INSERT INTO dst SELECT t.id, u.v FROM t JOIN u ON t.id = u.id", "SELECT id, v FROM dst ORDER BY id", []sql.Row{{int32(1), int32(100)}, {int32(2), int32(200)}}},
		{"update join", "UPDATE t JOIN u ON t.id = u.id SET t.v = u.v", "SELECT v FROM t ORDER BY id", []sql.Row{{int32(100)}, {int32(200)}, {int32(30)}}},
		{"delete join", "DELETE t FROM t JOIN u ON t.id = u.id", "SELECT id FROM t ORDER BY id", []sql.Row{{int32(3)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := memory.NewDatabase("db")
			provider := memory.NewDBProvider(db)
			engine := NewDefault(provider)
			defer engine.Close()
			ctx := sql.NewContext(context.Background(), sql.WithSession(memory.NewSession(sql.NewBaseSession(), provider)))
			ctx.SetCurrentDatabase("db")
			query := func(q string) []sql.Row {
				_, iter, _, err := engine.Query(ctx, q)
				require.NoError(t, err, q)
				rows, err := sql.RowIterToRows(ctx, iter)
				require.NoError(t, err, q)
				return rows
			}
			for _, q := range []string{
				"CREATE TABLE t (id INT PRIMARY KEY, v INT, INDEX (v))",
				"CREATE TABLE u (id INT PRIMARY KEY, v INT, INDEX (v))",
				"CREATE TABLE dst (id INT PRIMARY KEY, v INT)",
				"INSERT INTO t VALUES (1,10),(2,20),(3,30)",
				"INSERT INTO u VALUES (1,100),(2,200),(4,400)",
			} {
				query(q)
			}
			coster := &countingPlanCoster{Coster: engine.Analyzer.Coster, fail: true}
			stats := &countingPlanStats{StatsProvider: engine.Analyzer.Catalog.StatsProvider, fail: true}
			engine.Analyzer.Coster = coster
			engine.Analyzer.Catalog.StatsProvider = stats
			ctx.SetDisableCostBasedPlans(true)
			node, err := engine.AnalyzeQuery(ctx, test.query)
			require.NoError(t, err)
			transform.Inspect(node, func(n sql.Node) bool {
				if join, ok := n.(*plan.JoinNode); ok {
					require.True(t, !join.Op.IsPhysical() || join.Op.IsLookup())
				}
				return true
			})
			result := query(test.query)
			if test.verify != "" {
				result = query(test.verify)
			}
			require.Equal(t, test.expected, result)
			require.Zero(t, coster.calls)
			require.Zero(t, stats.calls)
			// Controls prove the hooks observe standalone index and memo planning.
			ctx.SetDisableCostBasedPlans(false)
			coster.fail, stats.fail = false, false
			_, err = engine.AnalyzeQuery(ctx, "SELECT id FROM t WHERE v >= 20")
			require.NoError(t, err)
			require.Positive(t, stats.calls)
			_, err = engine.AnalyzeQuery(ctx, "SELECT t.id FROM t JOIN u ON t.id = u.id")
			require.NoError(t, err)
			require.Positive(t, coster.calls)
		})
	}
}

func TestCostBasedPlanFlagIsNotSQLSettable(t *testing.T) {
	provider := memory.NewDBProvider(memory.NewDatabase("db"))
	engine := NewDefault(provider)
	defer engine.Close()
	ctx := sql.NewContext(context.Background(), sql.WithSession(memory.NewSession(sql.NewBaseSession(), provider)))
	ctx.SetDisableCostBasedPlans(true)
	_, _, _, err := engine.Query(ctx, "SET @@disable_cost_based_plans = 0")
	require.Error(t, err)
	require.True(t, ctx.DisableCostBasedPlans())
	_, iter, _, err := engine.Query(ctx, "SET @disable_cost_based_plans = 0")
	require.NoError(t, err)
	_, err = sql.RowIterToRows(ctx, iter)
	require.NoError(t, err)
	require.True(t, ctx.DisableCostBasedPlans())
	other := sql.NewContext(context.Background(), sql.WithSession(ctx.Session))
	require.False(t, other.DisableCostBasedPlans())
}
