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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/plan"
	"github.com/dolthub/go-mysql-server/sql/planbuilder"
	"github.com/dolthub/go-mysql-server/sql/transform"
	"github.com/dolthub/go-mysql-server/sql/types"
)

type validatorCounter struct {
	*expression.Literal
	calls *int
}

func (e *validatorCounter) Eval(*sql.Context, sql.Row) (interface{}, error) {
	*e.calls++
	return int64(1), nil
}
func (e *validatorCounter) FunctionName() string { return "validator_counter" }
func (e *validatorCounter) Description() string  { return "count evaluations" }
func (e *validatorCounter) String() string       { return "validator_counter()" }
func (e *validatorCounter) WithChildren(_ *sql.Context, children ...sql.Expression) (sql.Expression, error) {
	if len(children) != 0 {
		return nil, sql.ErrInvalidChildrenNumber.New(e, len(children), 0)
	}
	return e, nil
}

func validatorEngine(t *testing.T) (*Engine, *sql.Context) {
	t.Helper()
	provider := memory.NewDBProvider(memory.NewDatabase("db"))
	engine := NewDefault(provider)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	ctx := sql.NewContext(context.Background(), sql.WithSession(memory.NewSession(sql.NewBaseSession(), provider)))
	ctx.SetCurrentDatabase("db")
	for _, q := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, v INT)",
		"CREATE TABLE u (id INT PRIMARY KEY, v INT)",
		"INSERT INTO t VALUES (1, 10), (2, 20)",
		"INSERT INTO u VALUES (1, 100)",
	} {
		_, iter, _, err := engine.Query(ctx, q)
		require.NoError(t, err)
		_, err = sql.RowIterToRows(ctx, iter)
		require.NoError(t, err)
	}
	return engine, ctx
}

func TestContextValidatorsRejectScalarsBeforeEvaluation(t *testing.T) {
	for _, query := range []string{
		"SELECT validator_counter()",
		"SELECT CASE WHEN TRUE THEN 1 ELSE validator_counter() END",
		"CREATE TABLE d (v INT DEFAULT (validator_counter()))",
		"CREATE TABLE c (v INT CHECK (validator_counter() > 0))",
		"CREATE TABLE g (v INT, w INT AS (v + validator_counter()))",
		"SELECT id FROM t LIMIT :lim",
		"SET @v = validator_counter()",
	} {
		t.Run(query, func(t *testing.T) {
			engine, ctx := validatorEngine(t)
			calls, seen := 0, 0
			engine.Analyzer.Catalog.RegisterFunction(ctx, sql.NewFunction0("validator_counter", func(*sql.Context) sql.Expression {
				return &validatorCounter{expression.NewLiteral(int64(1), types.Int64), &calls}
			}))
			rejected := errors.New("forbidden scalar")
			ctx.SetValidators(nil, func(ctx *sql.Context, e sql.Expression) error {
				require.True(t, e.Resolved())
				if _, ok := e.(*validatorCounter); ok {
					seen++
					return rejected
				}
				return nil
			})
			b := planbuilder.New(ctx, engine.Analyzer.Catalog, nil)
			b.SetBindingsWithExpr(map[string]sql.Expression{"lim": &validatorCounter{expression.NewLiteral(int64(1), types.Int64), &calls}})
			_, _, _, _, err := b.Parse(query, nil, false)
			require.ErrorIs(t, err, rejected)
			require.Positive(t, seen)
			require.Zero(t, calls)
		})
	}
}

func TestContextNodeValidatorsSeeFinalPlans(t *testing.T) {
	for _, query := range []string{
		"SELECT id FROM t WHERE v = 10",
		"UPDATE t SET v = v + 1 WHERE id = 1",
		"DELETE FROM t WHERE id = 1",
		"INSERT INTO u SELECT id, v FROM t WHERE id = 2",
		"SELECT t.id FROM t JOIN u ON t.id = u.id",
		"SELECT id FROM t WHERE id IN (SELECT id FROM u)",
		"CREATE TRIGGER tr BEFORE UPDATE ON t FOR EACH ROW SET NEW.v = NEW.v + 1",
		"COMMIT",
		"EXPLAIN SELECT id FROM t",
	} {
		t.Run(query, func(t *testing.T) {
			engine, ctx := validatorEngine(t)
			var seen []sql.Node
			ctx.SetValidators(func(_ *sql.Context, n sql.Node) error { seen = append(seen, n); return nil }, nil)
			got, err := engine.AnalyzeQuery(ctx, query)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(seen), 2)
			require.Same(t, got, seen[len(seen)-1], "validator must see the returned, lowered plan")
			require.True(t, got.Resolved())
			if tr, ok := got.(*plan.CreateTrigger); ok {
				require.True(t, tr.Body.Resolved())
			}
			// Rejecting the final plan must propagate the original error.
			rejected := errors.New("forbidden final plan")
			calls := 0
			ctx.SetValidators(func(_ *sql.Context, n sql.Node) error {
				calls++
				if calls == len(seen) {
					return rejected
				}
				return nil
			}, nil)
			_, err = engine.AnalyzeQuery(ctx, query)
			require.ErrorIs(t, err, rejected)
		})
	}
}

func TestContextNodeValidatorRunsBeforeRules(t *testing.T) {
	engine, ctx := validatorEngine(t)
	b := planbuilder.New(ctx, engine.Analyzer.Catalog, nil)
	node, _, _, flags, err := b.Parse("SELECT id FROM t", nil, false)
	require.NoError(t, err)
	rejected := errors.New("forbidden input plan")
	ctx.SetValidators(func(_ *sql.Context, n sql.Node) error {
		require.Same(t, node, n)
		return rejected
	}, nil)
	got, err := engine.Analyzer.Analyze(ctx, node, nil, flags)
	require.ErrorIs(t, err, rejected)
	require.Same(t, node, got)
}

func TestContextValidatorsRejectAnalyzerEvaluation(t *testing.T) {
	for _, query := range []string{
		"SELECT id FROM t WHERE validator_counter() = 1",
		"SELECT id FROM t WHERE (CASE WHEN TRUE THEN 1 ELSE validator_counter() END) = 1",
	} {
		t.Run(query, func(t *testing.T) {
			engine, ctx := validatorEngine(t)
			calls := 0
			engine.Analyzer.Catalog.RegisterFunction(ctx, sql.NewFunction0("validator_counter", func(*sql.Context) sql.Expression {
				return &validatorCounter{expression.NewLiteral(int64(1), types.Int64), &calls}
			}))
			b := planbuilder.New(ctx, engine.Analyzer.Catalog, nil)
			node, _, _, flags, err := b.Parse(query, nil, false)
			require.NoError(t, err)
			require.Zero(t, calls)
			rejected := errors.New("forbidden evaluation")
			ctx.SetValidators(nil, func(ctx *sql.Context, e sql.Expression) error {
				var err error
				transform.InspectExpr(ctx, e, func(_ *sql.Context, e sql.Expression) bool {
					if _, ok := e.(*validatorCounter); ok {
						err = rejected
					}
					return true
				})
				return err
			})
			_, err = engine.Analyzer.Analyze(ctx, node, nil, flags)
			require.Zero(t, calls)
			require.ErrorIs(t, err, rejected)
			ctx.SetValidators(nil, nil)
			_, err = engine.Analyzer.Analyze(ctx, node, nil, flags)
			require.NoError(t, err)
			require.Positive(t, calls, "control must exercise analyzer evaluation")
		})
	}
}

func TestContextValidatorOnUpdate(t *testing.T) {
	engine, ctx := validatorEngine(t)
	rejected := errors.New("forbidden ON UPDATE definition")
	ctx.SetValidators(nil, func(_ *sql.Context, e sql.Expression) error {
		if f, ok := e.(sql.FunctionExpression); ok && f.FunctionName() == "now" {
			return rejected
		}
		return nil
	})
	_, err := engine.AnalyzeQuery(ctx, "CREATE TABLE d (v TIMESTAMP ON UPDATE CURRENT_TIMESTAMP)")
	require.ErrorIs(t, err, rejected)
}

func TestContextNodeValidatorSeesTriggerBody(t *testing.T) {
	engine, ctx := validatorEngine(t)
	_, iter, _, err := engine.Query(ctx, "CREATE TRIGGER tr BEFORE UPDATE ON t FOR EACH ROW SET NEW.v = NEW.v + 1")
	require.NoError(t, err)
	_, err = sql.RowIterToRows(ctx, iter)
	require.NoError(t, err)
	var seen []sql.Node
	ctx.SetValidators(func(_ *sql.Context, n sql.Node) error { seen = append(seen, n); return nil }, nil)
	got, err := engine.AnalyzeQuery(ctx, "UPDATE t SET v = 30 WHERE id = 1")
	require.NoError(t, err)
	var body sql.Node
	transform.Inspect(got, func(n sql.Node) bool {
		if tr, ok := n.(*plan.TriggerExecutor); ok {
			body = tr.Right()
		}
		return true
	})
	require.NotNil(t, body)
	require.True(t, body.Resolved())
	found := false
	for _, n := range seen {
		if n == body {
			found = true
		}
	}
	require.True(t, found, "analyzer exit must validate the trigger body")
}

func TestContextValidatorsRejectIndexPlanning(t *testing.T) {
	for _, ruleBased := range []bool{false, true} {
		t.Run(fmt.Sprint(ruleBased), func(t *testing.T) {
			engine, ctx := validatorEngine(t)
			b := planbuilder.New(ctx, engine.Analyzer.Catalog, nil)
			n, _, _, flags, err := b.Parse("SELECT id FROM t WHERE id = 1", nil, false)
			require.NoError(t, err)
			ctx.SetDisableCostBasedPlans(ruleBased)
			rejected := errors.New("forbidden index evaluation")
			ctx.SetValidators(nil, func(*sql.Context, sql.Expression) error { return rejected })
			_, err = engine.Analyzer.Analyze(ctx, n, nil, flags)
			require.ErrorIs(t, err, rejected)
		})
	}
}

func TestContextValidatorsRejectPlanningEvaluation(t *testing.T) {
	for _, query := range []string{
		"SET autocommit = 'off'",
		"SELECT id FROM t LIMIT 1 OFFSET 0",
		"SELECT id FROM t AS OF 'revision'",
		"SELECT _utf8mb4'text'",
	} {
		t.Run(query, func(t *testing.T) {
			engine, ctx := validatorEngine(t)
			rejected := errors.New("forbidden planning evaluation")
			literals := 0
			ctx.SetValidators(nil, func(_ *sql.Context, e sql.Expression) error {
				if _, ok := e.(*expression.Literal); ok {
					literals++
					// SET and AS OF construct their literals directly. The other
					// paths return a scalar first, then evaluate it.
					if strings.HasPrefix(query, "SET") || strings.Contains(query, "AS OF") || literals == 2 {
						return rejected
					}
				}
				return nil
			})
			b := planbuilder.New(ctx, engine.Analyzer.Catalog, nil)
			_, _, _, _, err := b.Parse(query, nil, false)
			require.ErrorIs(t, err, rejected)
		})
	}
}

func TestContextValidatorsNotSQLSettable(t *testing.T) {
	engine, ctx := validatorEngine(t)
	rejected := errors.New("validator still installed")
	ctx.SetValidators(func(_ *sql.Context, n sql.Node) error {
		if n == nil {
			return rejected
		}
		return nil
	}, nil)
	for _, name := range []string{"validators", "node_validator", "expression_validator"} {
		_, _, _, err := engine.Query(ctx, "SET @@session."+name+" = 0")
		require.Error(t, err)
		_, iter, _, err := engine.Query(ctx, "SET @"+name+" = 0")
		require.NoError(t, err)
		_, err = sql.RowIterToRows(ctx, iter)
		require.NoError(t, err)
		require.ErrorIs(t, ctx.ValidateNode(nil), rejected)
	}
}

func TestCallerPoliciesInProcedureAndTriggerBodies(t *testing.T) {
	for _, trigger := range []bool{false, true} {
		for _, reject := range []bool{false, true} {
			t.Run(fmt.Sprintf("trigger=%v/reject=%v", trigger, reject), func(t *testing.T) {
				engine, ctx := validatorEngine(t)
				calls := 0
				engine.Analyzer.Catalog.RegisterFunction(ctx, sql.NewFunction0("validator_counter", func(*sql.Context) sql.Expression {
					return &validatorCounter{expression.NewLiteral(int64(1), types.Int64), &calls}
				}))
				query := func(q string) error {
					_, iter, _, err := engine.Query(ctx, q)
					if err == nil {
						_, err = sql.RowIterToRows(ctx, iter)
					}
					return err
				}
				body := "SELECT t.id FROM t JOIN u ON t.id = u.id"
				if reject {
					body = "SELECT validator_counter()"
				}
				require.NoError(t, query("CREATE PROCEDURE p() "+body))
				q := "CALL p()"
				if trigger {
					require.NoError(t, query("CREATE TRIGGER tr BEFORE UPDATE ON t FOR EACH ROW CALL p()"))
					q = "UPDATE t SET v = 30 WHERE id = 1"
				}
				calls = 0
				coster := &countingPlanCoster{Coster: engine.Analyzer.Coster}
				stats := &countingPlanStats{StatsProvider: engine.Analyzer.Catalog.StatsProvider}
				engine.Analyzer.Coster, engine.Analyzer.Catalog.StatsProvider = coster, stats
				ctx.SetDisableCostBasedPlans(true)
				rejected := errors.New("procedure expression forbidden")
				nodes := 0
				ctx.SetValidators(func(got *sql.Context, _ sql.Node) error {
					require.True(t, got.DisableCostBasedPlans())
					nodes++
					return nil
				}, func(_ *sql.Context, e sql.Expression) error {
					if reject {
						if _, ok := e.(*validatorCounter); ok {
							return rejected
						}
					}
					return nil
				})
				err := query(q)
				if reject {
					require.ErrorIs(t, err, rejected)
					require.Zero(t, calls)
				} else {
					require.NoError(t, err)
				}
				require.Positive(t, nodes)
				require.Zero(t, coster.calls)
				require.Zero(t, stats.calls)
			})
		}
	}
}

func TestValidateFunctionArgumentsBeforeConstruction(t *testing.T) {
	engine, ctx := validatorEngine(t)
	calls, seen := 0, 0
	engine.Analyzer.Catalog.RegisterFunction(ctx, sql.NewFunction0("validator_counter", func(*sql.Context) sql.Expression {
		return &validatorCounter{expression.NewLiteral(int64(1), types.Int64), &calls}
	}))
	rejected := errors.New("argument rejected before constructor")
	ctx.SetValidators(nil, func(_ *sql.Context, e sql.Expression) error {
		if _, ok := e.(*validatorCounter); ok {
			seen++
			if seen > 1 {
				return rejected
			}
		}
		return nil
	})
	_, err := engine.AnalyzeQuery(ctx, "CREATE TABLE d (v DATETIME DEFAULT (`utc_timestamp`(validator_counter())))")
	require.Zero(t, calls)
	require.ErrorIs(t, err, rejected)
}

func TestCreateTableRejectsSleepBeforeConstruction(t *testing.T) {
	engine, ctx := validatorEngine(t)
	rejected := errors.New("sleep forbidden")
	ctx.SetValidators(nil, func(_ *sql.Context, e sql.Expression) error {
		if f, ok := e.(sql.FunctionExpression); ok && f.FunctionName() == "sleep" {
			return rejected
		}
		return nil
	})
	_, err := engine.AnalyzeQuery(ctx, "CREATE TABLE d (v DATETIME DEFAULT (`utc_timestamp`(SLEEP(0))))")
	require.ErrorIs(t, err, rejected)
}
