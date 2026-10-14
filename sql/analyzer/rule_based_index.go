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
	"strings"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/sets"
	"github.com/dolthub/go-mysql-server/sql/transform"
)

// ruleBasedIndexPlans lowers accesses without enumerating joins or reading statistics.
func ruleBasedIndexPlans(ctx *sql.Context, a *Analyzer, n sql.Node, _ *plan.Scope, _ RuleSelector, _ *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
	joins, joinSame, err := transform.Node(ctx, n, func(ctx *sql.Context, n sql.Node) (sql.Node, transform.TreeIdentity, error) {
		if join, ok := n.(*plan.JoinNode); ok {
			return ruleBasedLookupJoin(ctx, a.Catalog, join)
		}
		return n, transform.SameTree, nil
	})
	if err != nil {
		return n, transform.SameTree, err
	}
	ret, scanSame, err := transform.Node(ctx, joins, func(ctx *sql.Context, n sql.Node) (sql.Node, transform.TreeIdentity, error) {
		switch n := n.(type) {
		case *plan.Filter:
			tbl, _, ok := ruleBasedTable(n.Child)
			if !ok {
				return n, transform.SameTree, nil
			}
			// A previously lowered access already includes its static restrictions.
			if _, ok := tbl.(*plan.IndexedTableAccess); ok {
				return n, transform.SameTree, nil
			}
			idxTbl, ok := tbl.UnderlyingTable().(sql.IndexAddressableTable)
			if !ok {
				return n, transform.SameTree, nil
			}
			indexes, err := idxTbl.GetIndexes(ctx)
			if err != nil {
				return n, transform.SameTree, err
			}
			filters := expression.SplitConjunction(ctx, n.Expression)
			c := newIndexCoster(tbl.Name())
			c.indexedExprs, err = buildIndexedExprToColumnNameMap(ctx, a.Catalog, indexes, tbl)
			if err != nil {
				return n, transform.SameTree, err
			}
			root, leftover, imprecise := c.buildRoot(ctx, n.Expression, &defaultLogicTreeWalker{})
			if c.validationErr != nil {
				return n, transform.SameTree, c.validationErr
			}
			var leaves []*iScanLeaf
			switch root := root.(type) {
			case *iScanAnd:
				leaves = root.leaves()
			case *iScanLeaf:
				leaves = []*iScanLeaf{root}
			}
			idx := sql.ChooseIndex(indexes, func(idx sql.Index) (int, bool) {
				if !canUsePartialIndex(idx, filters) {
					return -1, false
				}
				prefix := 0
				for _, col := range idx.ColumnExpressionTypes(ctx) {
					equality, rang := false, false
					for _, leaf := range leaves {
						if !strings.EqualFold(leaf.normString(), col.Expression) {
							continue
						}
						switch leaf.Op() {
						case sql.IndexScanOpEq, sql.IndexScanOpNullSafeEq:
							equality = true
						case sql.IndexScanOpGt, sql.IndexScanOpGte, sql.IndexScanOpLt, sql.IndexScanOpLte:
							rang = true
						}
					}
					if !equality {
						return prefix, rang
					}
					prefix++
				}
				return prefix, false
			})
			if idx == nil {
				return n, transform.SameTree, nil
			}
			var include sets.FastIntSet
			for _, leaf := range leaves {
				for _, col := range idx.ColumnExpressionTypes(ctx) {
					if strings.EqualFold(leaf.normString(), col.Expression) {
						include.Add(int(leaf.Id()))
					}
				}
			}
			ita, _, remaining, err := buildIndexScan(ctx, tbl, idx, root, include, imprecise, c.idToExpr, leftover, filters)
			if err != nil || ita == nil {
				return n, transform.SameTree, err
			}
			child, err := replaceRuleBasedTable(ctx, n.Child, ita)
			if err != nil {
				return n, transform.SameTree, err
			}
			if len(remaining) > 0 {
				child = plan.NewFilter(ctx, expression.JoinAnd(remaining...), child)
			}
			return child, transform.NewTree, nil
		default:
			return n, transform.SameTree, nil
		}
	})
	return ret, joinSame && scanSame, err
}

// ruleBasedTable only descends through nodes that retain a single table's columns.
func ruleBasedTable(n sql.Node) (sql.TableNode, []sql.Expression, bool) {
	switch n := n.(type) {
	case *plan.TableAlias:
		return ruleBasedTable(n.Child)
	case *plan.Filter:
		tbl, filters, ok := ruleBasedTable(n.Child)
		return tbl, append(filters, n.Expression), ok
	case *plan.ResolvedTable:
		return n, nil, true
	case *plan.IndexedTableAccess:
		return n, nil, true
	default:
		return nil, nil, false
	}
}

func replaceRuleBasedTable(ctx *sql.Context, n sql.Node, ita *plan.IndexedTableAccess) (sql.Node, error) {
	switch n := n.(type) {
	case *plan.TableAlias:
		child, err := replaceRuleBasedTable(ctx, n.Child, ita)
		if err != nil {
			return nil, err
		}
		return n.WithChildren(ctx, child)
	case *plan.Filter:
		child, err := replaceRuleBasedTable(ctx, n.Child, ita)
		if err != nil {
			return nil, err
		}
		return n.WithChildren(ctx, child)
	default:
		return ita, nil
	}
}

func ruleBasedLookupJoin(ctx *sql.Context, cat sql.Catalog, join *plan.JoinNode) (sql.Node, transform.TreeIdentity, error) {
	// Right and full outer joins cannot use a right-side lookup without changing
	// which rows are preserved. Null-aware anti joins must observe null candidates.
	if join.Op.IsPhysical() || join.Op.IsRightOuter() || join.Op.IsFullOuter() ||
		join.Op.IsExcludeNulls() || join.Op == plan.JoinTypeAntiIncludeNulls ||
		join.Op.AsLookup() == join.Op {
		return join, transform.SameTree, nil
	}
	tbl, extra, ok := ruleBasedTable(join.Right())
	if !ok {
		return join, transform.SameTree, nil
	}
	if _, ok := tbl.(*plan.IndexedTableAccess); ok {
		// Keep static filters: replacing a static range by a dynamic lookup must
		// not discard restrictions already removed from its Filter parent.
		return join, transform.SameTree, nil
	}
	tabId, ok := tbl.(plan.TableIdNode)
	if !ok {
		return join, transform.SameTree, nil
	}
	idxTbl, ok := tbl.UnderlyingTable().(sql.IndexAddressableTable)
	if !ok {
		return join, transform.SameTree, nil
	}
	indexes, err := idxTbl.GetIndexes(ctx)
	if err != nil {
		return join, transform.SameTree, err
	}
	filters := expression.SplitConjunction(ctx, join.Filter)
	for _, f := range extra {
		filters = append(filters, expression.SplitConjunction(ctx, f)...)
	}
	schema := tbl.Schema(ctx)
	if pkt, ok := tbl.UnderlyingTable().(sql.PrimaryKeyTable); ok {
		schema = pkt.PrimaryKeySchema(ctx).Schema
	}
	fields := expression.SchemaToGetFields(schema, tabId.Columns())
	indexedExprs, err := buildIndexedExprToColumnNameMap(ctx, cat, indexes, tbl)
	if err != nil {
		return join, transform.SameTree, err
	}
	indexedCols := make(map[sql.ColumnId]indexedExprEntry)
	for _, entry := range indexedExprs {
		if ord := schema.IndexOfColName(entry.colName); ord >= 0 {
			indexedCols[fields[ord].(*expression.GetField).Id()] = entry
		}
	}
	keysForIndex := func(idx sql.Index) ([]sql.Expression, []bool) {
		var keys []sql.Expression
		var nulls []bool
		for _, col := range idx.ColumnExpressionTypes(ctx) {
			name := strings.TrimPrefix(strings.ToLower(col.Expression), strings.ToLower(tbl.Name())+".")
			ord := schema.IndexOfColName(name)
			if ord < 0 || ord >= len(fields) {
				break
			}
			gf := fields[ord].(*expression.GetField)
			key, _, nullable := keyForExpr(ctx, gf.Id(), tabId.Id(), filters, indexedCols)
			if key == nil {
				break
			}
			keys, nulls = append(keys, key), append(nulls, nullable)
		}
		return keys, nulls
	}
	idx := sql.ChooseIndex(indexes, func(idx sql.Index) (int, bool) {
		if !canUsePartialIndex(idx, filters) {
			return -1, false
		}
		keys, _ := keysForIndex(idx)
		return len(keys), false
	})
	if idx == nil {
		return join, transform.SameTree, nil
	}
	keys, nulls := keysForIndex(idx)
	ita, err := plan.NewIndexedAccessForTableNode(ctx, tbl, plan.NewLookupBuilder(ctx, idx, keys, nulls))
	if err != nil {
		return join, transform.SameTree, err
	}
	right, err := replaceRuleBasedTable(ctx, join.Right(), ita)
	if err != nil {
		return join, transform.SameTree, err
	}
	n, err := join.WithChildren(ctx, join.Left(), right)
	if err != nil {
		return join, transform.SameTree, err
	}
	ret := n.(*plan.JoinNode)
	ret.Op = join.Op.AsLookup()
	return ret, transform.NewTree, nil
}
