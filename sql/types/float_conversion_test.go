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

package types

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/vitess/go/sqltypes"
	"github.com/dolthub/vitess/go/vt/proto/query"
	"github.com/stretchr/testify/require"
)

func TestFloatIntegerBoundaries(t *testing.T) {
	ctx := sql.NewEmptyContext()
	for _, test := range []struct {
		f        float64
		signed   int64
		sr       sql.ConvertInRange
		unsigned uint64
		ur       sql.ConvertInRange
	}{
		{0x1p31 - 1, 2147483647, sql.InRange, 2147483647, sql.InRange},
		{0x1p31, 2147483648, sql.InRange, 2147483648, sql.InRange},
		{0x1p31 + 1, 2147483649, sql.InRange, 2147483649, sql.InRange},
		{0x1p53 - 1, 9007199254740991, sql.InRange, 9007199254740991, sql.InRange},
		{0x1p53, 9007199254740992, sql.InRange, 9007199254740992, sql.InRange},
		{0x1p53 + 2, 9007199254740994, sql.InRange, 9007199254740994, sql.InRange},
		{0x1p63, math.MaxInt64, sql.Overflow, 1 << 63, sql.InRange},
		{-0x1p63, math.MinInt64, sql.InRange, math.MaxInt64, sql.Underflow},
		{0x1p64, math.MaxInt64, sql.Overflow, math.MaxUint64, sql.Overflow},
		{-0x1p64, math.MinInt64, sql.Underflow, 0, sql.Underflow},
		{1e100, math.MaxInt64, sql.Overflow, math.MaxUint64, sql.Overflow},
		{-1e100, math.MinInt64, sql.Underflow, 0, sql.Underflow},
		{math.Inf(1), math.MaxInt64, sql.Overflow, math.MaxUint64, sql.Overflow},
		{math.Inf(-1), math.MinInt64, sql.Underflow, 0, sql.Underflow},
		{math.Nextafter(0x1p63, 0), 9223372036854774784, sql.InRange, 9223372036854774784, sql.InRange},
		{math.Nextafter(0x1p64, 0), math.MaxInt64, sql.Overflow, 18446744073709549568, sql.InRange},
	} {
		t.Run(fmt.Sprint(test.f), func(t *testing.T) {
			s, sr, err := Int64.Convert(ctx, test.f)
			require.NoError(t, err)
			require.Equal(t, test.signed, s)
			require.Equal(t, test.sr, sr)
			u, ur, err := Uint64.Convert(ctx, test.f)
			require.NoError(t, err)
			require.Equal(t, test.unsigned, u)
			require.Equal(t, test.ur, ur)
			v := sql.Value{Typ: sqltypes.Float64, Val: binary.LittleEndian.AppendUint64(nil, math.Float64bits(test.f))}
			si, sr, err := convertValueToInt64(ctx, v)
			require.NoError(t, err)
			require.Equal(t, test.signed, si)
			require.Equal(t, test.sr, sr)
			ui, ur, err := convertValueToUint64(ctx, v)
			require.NoError(t, err)
			require.Equal(t, test.unsigned, ui)
			require.Equal(t, test.ur, ur)
		})
	}
	for _, f := range []any{float32(math.NaN()), math.NaN()} {
		_, _, err := Int64.Convert(ctx, f)
		require.Error(t, err)
		_, _, err = Uint64.Convert(ctx, f)
		require.Error(t, err)
	}
	for _, typ := range []query.Type{sqltypes.Float32, sqltypes.Float64} {
		var bytes []byte
		if typ == sqltypes.Float32 {
			bytes = binary.LittleEndian.AppendUint32(nil, math.Float32bits(float32(math.NaN())))
		} else {
			bytes = binary.LittleEndian.AppendUint64(nil, math.Float64bits(math.NaN()))
		}
		v := sql.Value{Typ: typ, Val: bytes}
		_, _, err := convertValueToInt64(ctx, v)
		require.Error(t, err)
		_, _, err = convertValueToUint64(ctx, v)
		require.Error(t, err)
	}
	u, ur, err := Uint64.Convert(ctx, float32(0x1p63))
	require.NoError(t, err)
	require.Equal(t, uint64(1<<63), u)
	require.Equal(t, sql.InRange, ur)
}

func TestMixedFloatIntegerComparisonBoundaries(t *testing.T) {
	for _, test := range []struct {
		integer any
		f       float64
		cmp     int
	}{
		{int64(math.MaxInt64), 0x1p63, -1},
		{int64(math.MinInt64), -0x1p63, 0},
		{int64(math.MinInt64), -0x1p64, 1},
		{uint64(math.MaxUint64), 0x1p64, -1},
		{uint64(0), -0x1p64, 1},
		{int64(0), 1e100, -1}, {int64(0), -1e100, 1},
		{int64(0), math.Inf(1), -1}, {int64(0), math.Inf(-1), 1},
		{int64(0), math.NaN(), 1}, {uint64(0), math.NaN(), 1},
	} {
		t.Run(fmt.Sprintf("%v/%v", test.integer, test.f), func(t *testing.T) {
			require.Equal(t, test.cmp, compareNumbers(test.integer, test.f))
			require.Equal(t, -test.cmp, compareNumbers(test.f, test.integer))
			cmp, err := CompareJSON(sql.NewEmptyContext(), test.integer, test.f)
			require.NoError(t, err)
			require.Equal(t, test.cmp, cmp)
			contains, err := ContainsJSON(test.integer, test.f)
			require.NoError(t, err)
			require.Equal(t, test.cmp == 0, contains)
		})
	}
}

func TestOtherFloatIntegerBoundaries(t *testing.T) {
	ctx := sql.NewEmptyContext()
	enum := MustCreateEnumType([]string{"a", "b"}, sql.Collation_Default)
	set := MustCreateSetType([]string{"a", "b"}, sql.Collation_Default)
	bit := MustCreateBitType(64)
	sys := NewSystemIntType("test", math.MinInt64, math.MaxInt64, false)
	for _, f := range []float64{0x1p63, -0x1p63, 0x1p64, -0x1p64, 1e100, -1e100, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(fmt.Sprint(f), func(t *testing.T) {
			enumValue, _, err := enum.Convert(ctx, f)
			if math.IsNaN(f) {
				require.NoError(t, err)
				require.Equal(t, uint16(0), enumValue)
			} else {
				require.Error(t, err)
			}
			_, _, err = bit.Convert(ctx, f)
			if f >= 0 && f < 0x1p64 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			value, _, err := set.Convert(ctx, f)
			if f <= 0 || math.IsNaN(f) {
				require.NoError(t, err)
				require.Equal(t, uint64(0), value)
			} else {
				require.Error(t, err)
			}
			_, _, err = sys.Convert(ctx, f)
			if f == -0x1p63 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	// The old integer fast path serialized the rounded signed maximum at 2^63
	// on arm64, and the signed minimum on amd64.
	encoded, err := marshalToMySqlString(float64(0x1p63))
	require.NoError(t, err)
	require.Equal(t, "9223372036854776000", encoded)
}
