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
		precision = min(precision+22, types.DecimalTypeMaxPrecision)
	}
	return types.CreateAggregateDecimalType(uint8(precision), uint8(min(scale, types.DecimalTypeMaxScale)))
}

func aggregatePrecisionIncrement(ctx *sql.Context) int {
	if v, err := ctx.GetSessionVariable(ctx, "div_precision_increment"); err == nil {
		return int(v.(int64))
	}
	return 4
}

func decimalAggregateResult(ctx *sql.Context, child sql.Expression, sum *apd.Decimal, count int64, average bool) (interface{}, error) {
	if !average {
		return new(apd.Decimal).Set(sum), nil
	}
	// MySQL decimal division keeps fractional capacity in nine-digit groups,
	// independently of the result's display scale (capped at 30). Keep at least
	// guard digits so boundary rounding never rounds an already-rounded
	// quotient. Column conversion and SQL serialization are the result boundaries.
	scale := int32(numericAggregateType(ctx, child, true).(sql.DecimalType).Scale())
	if d, ok := child.Type(ctx).(sql.DecimalType); ok {
		scale = max(scale, int32(d.Scale())+int32(aggregatePrecisionIncrement(ctx)))
	}
	// Also retain enough places for a nonzero input divided by any row count.
	scale = max(scale+1, -sum.Exponent+int32(types.DecimalFromInt64(count).NumDigits()))
	scale = ((scale + 8) / 9) * 9
	var numerator, denominator, power, quotient, remainder apd.BigInt
	power.Exp(apd.NewBigInt(10), apd.NewBigInt(int64(scale+sum.Exponent)), nil)
	numerator.Mul(&sum.Coeff, &power)
	denominator.SetInt64(count)
	quotient.QuoRem(&numerator, &denominator, &remainder)
	// Derive one more actual quotient digit from a nonzero remainder. Do not round the
	// quotient here: DecimalRound at the result boundary must round only once.
	if remainder.Sign() != 0 {
		quotient.Mul(&quotient, apd.NewBigInt(10))
		remainder.Mul(&remainder, apd.NewBigInt(10))
		var digit apd.BigInt
		digit.Quo(&remainder, &denominator)
		quotient.Add(&quotient, &digit)
		scale++
	}
	result := apd.New(0, -scale)
	result.Coeff.Set(&quotient)
	result.Negative = sum.Negative && result.Coeff.Sign() != 0
	return result, nil
}
