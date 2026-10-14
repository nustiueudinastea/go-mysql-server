// Copyright 2022 Dolthub, Inc.
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

package memo

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/plan"
)

type disabledPlanCoster struct{ calls int }

func (c *disabledPlanCoster) EstimateCost(*sql.Context, RelExpr, sql.StatsProvider) (float64, error) {
	c.calls++
	return 0, errors.New("coster called")
}

func TestDisableCostBasedMemoPlans(t *testing.T) {
	db := memory.NewDatabase("test")
	ctx := newContext(memory.NewDBProvider(db))
	join := plan.NewInnerJoin(ctx,
		plan.NewInnerJoin(ctx, tableNode(db, "a"), tableNode(db, "b"), newEq("a.x = b.x")),
		tableNode(db, "c"), newEq("b.x = c.x"))
	for _, disabled := range []bool{true, false} {
		ctx.SetDisableCostBasedPlans(disabled)
		coster := &disabledPlanCoster{}
		m := NewMemo(ctx, memory.NewStatsProv(), nil, coster, nil)
		NewJoinOrderBuilder(m).ReorderJoin(ctx, join)
		if disabled {
			// No alternative join orders have been enumerated.
			require.Nil(t, m.Root().First.Next())
			require.NoError(t, m.OptimizeRoot(ctx))
			require.Same(t, m.Root().First, m.Root().Best)
			result, err := m.BestRootPlan(ctx)
			require.NoError(t, err)
			require.Equal(t, plan.JoinTypeInner, result.(*plan.JoinNode).Op)
			require.Zero(t, coster.calls)
		} else {
			require.NotNil(t, m.Root().First.Next())
			require.EqualError(t, m.OptimizeRoot(ctx), "coster called")
			require.Positive(t, coster.calls)
		}
	}
}
