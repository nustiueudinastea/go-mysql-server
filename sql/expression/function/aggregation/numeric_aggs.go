// Copyright 2022 DoltHub, Inc.
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
	"fmt"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/dolthub/vitess/go/sqltypes"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

// numericAggregateType follows MySQL's SUM and AVG decimal metadata rules.
// SUM allows 22 additional digits for the row count; AVG adds the division
// precision increment to both precision and scale. Approximate inputs use DOUBLE.
func numericAggregateType(ctx *sql.Context, child sql.Expression, average bool) sql.Type {
	typ := child.Type(ctx)
	var precision, scale int
	if types.IsDecimal(typ) {
		d := typ.(sql.DecimalType)
		precision, scale = int(d.Precision()), int(d.Scale())
	} else if types.IsInteger(typ) {
		switch typ.Type() {
		case sqltypes.Int8, sqltypes.Uint8:
			precision = 3
		case sqltypes.Int16, sqltypes.Uint16:
			precision = 5
		case sqltypes.Int24:
			precision = 7
		case sqltypes.Uint24:
			precision = 8
		case sqltypes.Int32, sqltypes.Uint32:
			precision = 10
		case sqltypes.Int64:
			precision = 19
		case sqltypes.Uint64:
			precision = 20
		}
		e := child
		if distinct, ok := e.(*expression.DistinctExpression); ok {
			e = distinct.Child
		}
		if literal, ok := e.(*expression.Literal); ok {
			precision = len(strings.TrimPrefix(fmt.Sprint(literal.Value()), "-"))
			if _, ok := literal.Value().(bool); ok {
				precision = 1
			}
		}
	} else {
		return types.Float64
	}
	if average {
		increment := aggregatePrecisionIncrement(ctx)
		precision += increment
		scale += increment
	} else {
		precision += 22
	}
	return types.MustCreateDecimalType(uint8(min(precision, types.DecimalTypeMaxPrecision)), uint8(min(scale, types.DecimalTypeMaxScale)))
}

func aggregatePrecisionIncrement(ctx *sql.Context) int {
	if v, err := ctx.GetSessionVariable(ctx, "div_precision_increment"); err == nil {
		return int(v.(int64))
	}
	return 4
}

func decimalAggregateResult(ctx *sql.Context, child sql.Expression, sum *apd.Decimal, count int64, average bool) (interface{}, error) {
	typ := numericAggregateType(ctx, child, average).(sql.DecimalType)
	if average {
		sum = types.DecimalDiv(sum, types.DecimalFromInt64(count), int32(typ.Scale()), false)
	}
	result, _, err := typ.Convert(ctx, sum)
	return result, err
}
