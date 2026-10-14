// Copyright 2023 Dolthub, Inc.
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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/transform"
)

func TestDisableCostBasedPlanEntryPoints(t *testing.T) {
	ctx := sql.NewEmptyContext()
	ctx.SetDisableCostBasedPlans(true)
	node := plan.NewEmptyTableWithSchema(nil)
	// Nil dependencies must never be consulted by any costing entry point.
	got, same, err := optimizeJoins(ctx, nil, node, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, transform.SameTree, same)
	require.Same(t, node, got)
	got, same, err = inOrderReplanJoin(ctx, nil, nil, nil, node, false, nil)
	require.NoError(t, err)
	require.Equal(t, transform.SameTree, same)
	require.Same(t, node, got)
	join := plan.NewInnerJoin(ctx, node, node, nil)
	got, err = replanJoin(ctx, join, nil, nil, nil)
	require.NoError(t, err)
	require.Same(t, join, got)
	got, same, err = costedIndexScans(ctx, nil, node, nil)
	require.NoError(t, err)
	require.Equal(t, transform.SameTree, same)
	require.Same(t, node, got)
	scan, stats, filters, err := getCostedIndexScan(ctx, nil, nil, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, scan)
	require.Nil(t, stats)
	require.Nil(t, filters)
	require.NoError(t, addIndexScans(ctx, nil, nil))
	require.NoError(t, newIndexCoster("t").cost(ctx, nil, nil, nil, false))
}

func TestDisableCostBasedPlansInEveryBatch(t *testing.T) {
	ctx := sql.NewEmptyContext()
	ctx.SetDisableCostBasedPlans(true)
	node := plan.NewEmptyTableWithSchema(nil)
	lowered := false
	batch := &Batch{Rules: []Rule{
		{Id: optimizeJoinsId, Apply: func(ctx *sql.Context, a *Analyzer, n sql.Node, scope *plan.Scope, sel RuleSelector, flags *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
			t.Fatal("cost-based rule was called")
			return n, transform.SameTree, nil
		}},
		{Id: assignExecIndexesId, Apply: func(ctx *sql.Context, a *Analyzer, n sql.Node, scope *plan.Scope, sel RuleSelector, flags *sql.QueryFlags) (sql.Node, transform.TreeIdentity, error) {
			lowered = true
			return n, transform.SameTree, nil
		}},
	}}
	_, _, err := batch.evalOnce(ctx, &Analyzer{}, node, nil, DefaultRuleSelector, nil)
	require.NoError(t, err)
	require.True(t, lowered)
}
