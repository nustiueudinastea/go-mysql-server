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
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func rulePlanEngine(t *testing.T) (*Engine, *sql.Context, *memory.Database) {
	t.Helper()
	db := memory.NewDatabase("db")
	provider := memory.NewDBProvider(db)
	engine := NewDefault(provider)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	ctx := sql.NewContext(context.Background(), sql.WithSession(memory.NewSession(sql.NewBaseSession(), provider)))
	ctx.SetCurrentDatabase("db")
	ctx.SetDisableCostBasedPlans(true)
	engine.Analyzer.Coster = &countingPlanCoster{Coster: engine.Analyzer.Coster, fail: true}
	engine.Analyzer.Catalog.StatsProvider = &countingPlanStats{StatsProvider: engine.Analyzer.Catalog.StatsProvider, fail: true}
	return engine, ctx, db
}

func ruleQuery(t *testing.T, engine *Engine, ctx *sql.Context, q string) ([]sql.Row, error) {
	t.Helper()
	_, iter, _, err := engine.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return sql.RowIterToRows(ctx, iter)
}

func rulePlanSchema(t *testing.T, engine *Engine, ctx *sql.Context) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, u INT, a INT, b INT, c INT, v INT, UNIQUE KEY z_unique (u), UNIQUE KEY a_unique (u), INDEX z_ab (a,b), INDEX a_ab (a,b), INDEX ac (a,c), INDEX b_idx (b))",
		"CREATE TABLE r (id INT PRIMARY KEY, u INT, a INT, b INT, c INT, v INT, UNIQUE KEY z_unique (u), UNIQUE KEY a_unique (u), INDEX z_ab (a,b), INDEX a_ab (a,b), INDEX ac (a,c), INDEX b_idx (b))",
		"CREATE TABLE no_idx (a INT, b INT)",
		"INSERT INTO t VALUES (1,11,2,3,4,10),(2,12,2,5,6,20),(3,13,7,8,9,30)",
		"INSERT INTO r SELECT * FROM t",
		"INSERT INTO no_idx VALUES (2,3),(7,8)",
	} {
		_, err := ruleQuery(t, engine, ctx, q)
		require.NoError(t, err, q)
	}
}

func TestRuleBasedIndexChoice(t *testing.T) {
	engine, ctx, _ := rulePlanEngine(t)
	rulePlanSchema(t, engine, ctx)
	for _, tt := range []struct {
		query   string
		indexes []string
		lookup  bool
	}{
		{"SELECT * FROM t WHERE id=2 AND u=12", []string{"t.PRIMARY"}, false},
		{"SELECT * FROM t WHERE u=12 AND a=2 AND b=5", []string{"t.a_unique"}, false},
		{"SELECT * FROM t WHERE a=2", []string{"t.a_ab"}, false},
		{"SELECT * FROM t WHERE a=2 AND b=5", []string{"t.a_ab"}, false},
		{"SELECT * FROM t WHERE a=2 AND c=6", []string{"t.ac"}, false},
		{"SELECT * FROM t WHERE a=2 AND b>3", []string{"t.a_ab"}, false},
		{"SELECT * FROM t WHERE a=2 AND c>3", []string{"t.ac"}, false},
		{"SELECT * FROM t WHERE b>3", []string{"t.b_idx"}, false},
		{"SELECT * FROM t WHERE v=20", nil, false},
		{"SELECT * FROM t WHERE v=20 ORDER BY id", nil, false},
		{"SELECT * FROM t AS x WHERE x.a=2 AND 3<x.b", []string{"t.a_ab"}, false},
		{"UPDATE t SET v=21 WHERE u=12", []string{"t.a_unique"}, false},
		{"DELETE FROM t WHERE id=2", []string{"t.PRIMARY"}, false},
		{"SELECT t.id FROM t JOIN r ON t.id=r.id AND t.u=r.u", []string{"r.PRIMARY"}, true},
		{"SELECT t.id FROM t JOIN r ON t.u=r.u AND t.a=r.a", []string{"r.a_unique"}, true},
		{"SELECT t.id FROM t JOIN r ON t.a=r.a", []string{"r.a_ab"}, true},
		{"SELECT t.id FROM t JOIN r ON t.a=r.a AND t.c=r.c", []string{"r.ac"}, true},
		{"SELECT t.id FROM t LEFT JOIN r ON t.a=r.a AND t.b=r.b", []string{"r.a_ab"}, true},
		{"SELECT t.id FROM t JOIN no_idx ON t.a=no_idx.a", nil, false},
		{"SELECT t.id FROM t JOIN r ON t.a=r.a AND r.b=3", []string{"r.a_ab"}, true},
	} {
		t.Run(tt.query, func(t *testing.T) {
			var first string
			for run := 0; run < 10; run++ {
				node, err := engine.AnalyzeQuery(ctx, tt.query)
				require.NoError(t, err)
				var indexes []string
				lookup := false
				transform.Inspect(node, func(n sql.Node) bool {
					if ita, ok := n.(*plan.IndexedTableAccess); ok {
						indexes = append(indexes, ita.Index().Table()+"."+ita.Index().ID())
					}
					if join, ok := n.(*plan.JoinNode); ok {
						lookup = lookup || join.Op.IsLookup()
						require.True(t, !join.Op.IsPhysical() || join.Op.IsLookup())
						require.Equal(t, "t", join.Left().(sql.Nameable).Name())
					}
					return true
				})
				require.Equal(t, tt.indexes, indexes)
				require.Equal(t, tt.lookup, lookup)
				if run == 0 {
					first = node.String()
				}
				require.Equal(t, first, node.String())
			}
		})
	}
	// Correlated lookups share the rule, including when extra equalities do
	// not belong to the chosen unique key.
	for run := 0; run < 10; run++ {
		node, err := engine.AnalyzeQuery(ctx, "SELECT id,(SELECT r.v FROM r WHERE r.u=t.u AND r.a=t.a) FROM t ORDER BY id")
		require.NoError(t, err)
		found := false
		transform.InspectExpressions(ctx, node, func(_ *sql.Context, e sql.Expression) bool {
			if subquery, ok := e.(*plan.Subquery); ok {
				transform.Inspect(subquery.Query, func(n sql.Node) bool {
					if ita, ok := n.(*plan.IndexedTableAccess); ok {
						require.Equal(t, "a_unique", ita.Index().ID())
						found = true
					}
					return true
				})
			}
			return true
		})
		require.True(t, found)
	}
	// Check residual predicates, aliases, range bounds and join execution too.
	for _, tt := range []struct {
		query string
		rows  []sql.Row
	}{
		{"SELECT id,(SELECT r.v FROM r WHERE r.u=t.u AND r.a=t.a) FROM t ORDER BY id", []sql.Row{{int32(1), int32(10)}, {int32(2), int32(20)}, {int32(3), int32(30)}}},
		{"SELECT id FROM t WHERE a=2 AND b>3 AND v=20", []sql.Row{{int32(2)}}},
		{"SELECT x.id FROM t x WHERE x.a=2 AND 3<x.b", []sql.Row{{int32(2)}}},
		{"SELECT t.id,r.id FROM t JOIN r ON t.a=r.a AND r.b=3 ORDER BY t.id", []sql.Row{{int32(1), int32(1)}, {int32(2), int32(1)}}},
		{"SELECT t.id,r.id FROM t LEFT JOIN r ON t.a=r.a AND t.b=r.b ORDER BY t.id", []sql.Row{{int32(1), int32(1)}, {int32(2), int32(2)}, {int32(3), int32(3)}}},
	} {
		rows, err := ruleQuery(t, engine, ctx, tt.query)
		require.NoError(t, err, tt.query)
		require.Equal(t, tt.rows, rows, tt.query)
	}
}

func TestRuleBasedPlanCorpus(t *testing.T) {
	engine, ctx, _ := rulePlanEngine(t)
	rulePlanSchema(t, engine, ctx)
	queries := []string{
		"SELECT * FROM t WHERE id=%d", "SELECT * FROM t WHERE u=%d", "SELECT * FROM t WHERE a=%d",
		"SELECT * FROM t WHERE a=2 AND b>%d", "SELECT * FROM t WHERE a=2 AND c>%d", "SELECT * FROM t WHERE b<%d",
		"SELECT * FROM t WHERE v=%d", "SELECT * FROM t WHERE id=%d AND u=11", "SELECT * FROM t WHERE a=2 AND b=%d",
		"SELECT * FROM t WHERE a=2 AND c=%d", "SELECT * FROM t WHERE a=2 AND b BETWEEN %d AND 8",
		"SELECT * FROM t WHERE a=2 AND b>%d AND v=20", "SELECT * FROM t x WHERE x.a=2 AND x.b>%d",
		"SELECT id FROM t WHERE u=%d ORDER BY id", "SELECT id FROM t WHERE b>%d LIMIT 2",
		"SELECT t.id FROM t JOIN r ON t.id=r.id WHERE t.id=%d",
		"SELECT t.id FROM t JOIN r ON t.u=r.u WHERE t.a=%d",
		"SELECT t.id FROM t JOIN r ON t.a=r.a WHERE t.b>%d",
		"SELECT t.id FROM t LEFT JOIN r ON t.a=r.a AND t.b=r.b WHERE t.id>%d",
		"SELECT t.id FROM t JOIN no_idx ON t.a=no_idx.a WHERE t.id>%d",
		"SELECT t.id FROM t JOIN r ON t.a=r.a AND r.b=%d",
		"SELECT id,(SELECT r.v FROM r WHERE r.id=t.id) FROM t WHERE id=%d",
		"UPDATE t SET v=21 WHERE id=%d", "UPDATE t SET v=21 WHERE u=%d", "DELETE FROM t WHERE id=%d",
	}
	var out strings.Builder
	count := 0
	for _, template := range queries {
		for _, value := range []int{1, 2} {
			q := fmt.Sprintf(template, value)
			node, err := engine.AnalyzeQuery(ctx, q)
			require.NoError(t, err, q)
			fmt.Fprintf(&out, "%s\n%s\n\n", q, node.String())
			count++
		}
	}
	require.Equal(t, 50, count)
	require.Contains(t, out.String(), "IndexedTableAccess")
	require.Contains(t, out.String(), "LookupJoin")
	if path := os.Getenv("GMS_RULE_PLAN_FILE"); path != "" {
		require.NoError(t, os.WriteFile(path, []byte(out.String()), 0644))
	}
}

// probeTable has sorted, monotone integer keys. It counts every row examined by
// binary searches as well as rows returned by scans. Counting only output rows
// from memory.Table's primary-key scan would conceal its internal full scan.
type probeTable struct {
	*memory.Table
	rows    []sql.Row
	reads   int
	lookups []string
}

func (t *probeTable) IgnoreSessionData() bool                                   { return true }
func (t *probeTable) WithProjections(*sql.Context, []string) (sql.Table, error) { return t, nil }
func (t *probeTable) Partitions(*sql.Context) (sql.PartitionIter, error) {
	return sql.PartitionsToPartitionIter(probePartition{0, len(t.rows)}), nil
}
func (t *probeTable) PartitionRows(_ *sql.Context, p sql.Partition) (sql.RowIter, error) {
	r := p.(probePartition)
	return &probeIter{table: t, pos: r.start, end: r.end}, nil
}
func (t *probeTable) IndexedAccess(_ *sql.Context, l sql.IndexLookup) sql.IndexedTable {
	return &probeIndexedTable{probeTable: t}
}
func (t *probeTable) Inserter(*sql.Context) sql.RowInserter                 { return &probeEditor{t} }
func (t *probeTable) Updater(*sql.Context) sql.RowUpdater                   { return &probeEditor{t} }
func (t *probeTable) Deleter(*sql.Context) sql.RowDeleter                   { return &probeEditor{t} }
func (t *probeTable) GetForeignKeyEditor(*sql.Context) sql.ForeignKeyEditor { return &probeEditor{t} }

type probePartition struct{ start, end int }

func (probePartition) Key() []byte { return []byte("probe") }

type probeIter struct {
	table    *probeTable
	pos, end int
}

func (i *probeIter) Next(*sql.Context) (sql.Row, error) {
	for i.pos < i.end {
		row := i.table.rows[i.pos]
		i.pos++
		i.table.reads++
		if row != nil {
			return append(sql.Row(nil), row...), nil
		}
	}
	return nil, io.EOF
}
func (*probeIter) Close(*sql.Context) error { return nil }

type probeIndexedTable struct{ *probeTable }

func (t *probeIndexedTable) LookupPartitions(ctx *sql.Context, l sql.IndexLookup) (sql.PartitionIter, error) {
	t.lookups = append(t.lookups, l.Index.ID())
	ranges := l.Ranges.(sql.MySQLRangeCollection)
	requireRange := ranges[0][0]
	key, _, err := types.Int32.Convert(ctx, sql.GetMySQLRangeCutKey(requireRange.LowerBound))
	if err != nil {
		return nil, err
	}
	col := l.Index.(*memory.Index).Exprs[0].(*expression.GetField).Index()
	// Test lookups are closed point ranges over unique, intact sorted keys.
	start := sort.Search(len(t.rows), func(i int) bool { t.reads++; return t.rows[i][col].(int32) >= key.(int32) })
	end := start
	if start < len(t.rows) {
		t.reads++
		if t.rows[start][col].(int32) == key.(int32) {
			end++
		}
	}
	return sql.PartitionsToPartitionIter(probePartition{start, end}), nil
}

type probeEditor struct{ *probeTable }

func (*probeEditor) StatementBegin(*sql.Context)              {}
func (*probeEditor) StatementComplete(*sql.Context) error     { return nil }
func (*probeEditor) DiscardChanges(*sql.Context, error) error { return nil }
func (*probeEditor) Close(*sql.Context) error                 { return nil }
func (e *probeEditor) Insert(_ *sql.Context, r sql.Row) error {
	e.rows = append(e.rows, append(sql.Row(nil), r...))
	return nil
}
func (e *probeEditor) Update(_ *sql.Context, old, new sql.Row) error {
	e.rows[int(old[0].(int32))-1] = append(sql.Row(nil), new...)
	return nil
}
func (e *probeEditor) Delete(_ *sql.Context, r sql.Row) error {
	e.rows[int(r[0].(int32))-1] = nil
	return nil
}

func TestRuleBasedIndexRowsRead(t *testing.T) {
	for _, tt := range []struct {
		name, query string
		fk, fails   bool
	}{
		{"primary update", "UPDATE parent SET v=1 WHERE id=50000", false, false},
		{"unique update", "UPDATE parent SET v=1 WHERE u=50000", false, false},
		{"primary delete", "DELETE FROM parent WHERE id=50000", false, false},
		{"unique delete", "DELETE FROM parent WHERE u=50000", false, false},
		{"child insert", "INSERT INTO child VALUES (100001,50000,0)", true, false},
		{"parent delete", "DELETE FROM parent WHERE id=50000", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			engine, ctx, db := rulePlanEngine(t)
			makeTable := func(name, key string) *probeTable {
				sch := sql.NewPrimaryKeySchema(sql.Schema{
					{Name: "id", Source: name, Type: types.Int32, PrimaryKey: true},
					{Name: key, Source: name, Type: types.Int32},
					{Name: "v", Source: name, Type: types.Int32},
				})
				base := memory.NewTable(ctx, db, name, sch, db.GetForeignKeyCollection())
				require.NoError(t, base.CreateIndex(ctx, sql.IndexDef{Name: "key_idx", Columns: []sql.IndexColumn{{Name: key}}, Constraint: sql.IndexConstraint_Unique}))
				tbl := &probeTable{Table: base, rows: make([]sql.Row, 100000)}
				for i := range tbl.rows {
					tbl.rows[i] = sql.Row{int32(i + 1), int32(i + 1), int32(0)}
				}
				db.AddTable(name, tbl)
				return tbl
			}
			parent, child := makeTable("parent", "u"), makeTable("child", "pid")
			if tt.fk {
				require.NoError(t, parent.CreateIndex(ctx, sql.IndexDef{Name: "A_id", Columns: []sql.IndexColumn{{Name: "id"}}, Constraint: sql.IndexConstraint_Unique}))
				require.NoError(t, child.AddForeignKey(ctx, sql.ForeignKeyConstraint{Name: "fk", Database: "db", Table: "child", Columns: []string{"pid"}, ParentDatabase: "db", ParentTable: "parent", ParentColumns: []string{"id"}, IsResolved: true}))
			}
			_, err := ruleQuery(t, engine, ctx, tt.query)
			if tt.fails {
				require.True(t, sql.ErrForeignKeyParentViolation.Is(err), "%v", err)
			} else {
				require.NoError(t, err)
			}
			require.Positive(t, parent.reads)
			require.LessOrEqual(t, parent.reads, 64)
			require.LessOrEqual(t, child.reads, 64)
			require.LessOrEqual(t, parent.reads+child.reads, 64)
			if tt.name == "parent delete" {
				require.Positive(t, child.reads)
			}
			if strings.Contains(tt.name, "update") {
				require.Equal(t, int32(1), parent.rows[49999][2])
			}
			if !tt.fk && strings.Contains(tt.name, "delete") {
				require.Nil(t, parent.rows[49999])
			}
			if tt.name == "child insert" {
				require.Len(t, child.rows, 100001)
				require.Equal(t, []string{"PRIMARY"}, parent.lookups)
			}
			t.Logf("rows examined: parent=%d child=%d", parent.reads, child.reads)
		})
	}
}
