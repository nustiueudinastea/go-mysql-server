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

package sql_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/dolthub/go-mysql-server/sql"
	"github.com/stretchr/testify/require"
)

func TestClampFloatToIntegerBoundaries(t *testing.T) {
	for _, test := range []struct {
		f        float64
		signed   int64
		unsigned uint64
	}{
		{0, 0, 0}, {1.9, 1, 1}, {-1.9, -1, 0},
		{0x1p31 - 1, 2147483647, 2147483647},
		{0x1p31, 2147483648, 2147483648},
		{0x1p31 + 1, 2147483649, 2147483649},
		{-0x1p31 - 1, -2147483649, 0},
		{-0x1p31, -2147483648, 0},
		{-0x1p31 + 1, -2147483647, 0},
		{0x1p53 - 1, 9007199254740991, 9007199254740991},
		{0x1p53, 9007199254740992, 9007199254740992},
		{0x1p53 + 2, 9007199254740994, 9007199254740994},
		{-0x1p53 - 2, -9007199254740994, 0},
		{-0x1p53, -9007199254740992, 0},
		{-0x1p53 + 1, -9007199254740991, 0},
		{math.Nextafter(0x1p63, 0), 9223372036854774784, 9223372036854774784},
		{0x1p63, math.MaxInt64, 1 << 63},
		{math.Nextafter(0x1p63, math.Inf(1)), math.MaxInt64, 9223372036854777856},
		{math.Nextafter(-0x1p63, 0), -9223372036854774784, 0},
		{-0x1p63, math.MinInt64, 0},
		{math.Nextafter(-0x1p63, math.Inf(-1)), math.MinInt64, 0},
		{math.Nextafter(0x1p64, 0), math.MaxInt64, 18446744073709549568},
		{0x1p64, math.MaxInt64, math.MaxUint64},
		{math.Nextafter(0x1p64, math.Inf(1)), math.MaxInt64, math.MaxUint64},
		{math.Nextafter(-0x1p64, 0), math.MinInt64, 0},
		{-0x1p64, math.MinInt64, 0},
		{math.Nextafter(-0x1p64, math.Inf(-1)), math.MinInt64, 0},
		{1e100, math.MaxInt64, math.MaxUint64}, {-1e100, math.MinInt64, 0},
		{math.NaN(), 0, 0}, {math.Inf(1), math.MaxInt64, math.MaxUint64},
		{math.Inf(-1), math.MinInt64, 0},
	} {
		t.Run(fmt.Sprint(test.f), func(t *testing.T) {
			require.Equal(t, test.signed, sql.ClampFloatToInteger[int64](test.f))
			require.Equal(t, test.unsigned, sql.ClampFloatToInteger[uint64](test.f))
			require.Equal(t, int(test.signed), sql.ClampFloatToInteger[int](test.f))
			require.Equal(t, uint(test.unsigned), sql.ClampFloatToInteger[uint](test.f))
			// Compare with the upstream casts only inside their defined domains.
			if test.f >= -0x1p63 && test.f < 0x1p63 {
				require.Equal(t, int(test.f), sql.ClampFloatToInteger[int](test.f))
			}
			if test.f >= 0 && test.f < 0x1p64 {
				require.Equal(t, uint(test.f), sql.ClampFloatToInteger[uint](test.f))
			}
		})
	}
	// Narrow and named integer types use their own bounds as well.
	type nativeInt int
	type nativeUint uint
	require.Equal(t, nativeInt(1<<53), sql.ClampFloatToInteger[nativeInt](0x1p53))
	require.Equal(t, nativeUint(1<<53), sql.ClampFloatToInteger[nativeUint](0x1p53))
	require.Equal(t, int8(math.MaxInt8), sql.ClampFloatToInteger[int8](0x1p7))
	require.Equal(t, int8(math.MinInt8), sql.ClampFloatToInteger[int8](-0x1p7-1))
	require.Equal(t, uint8(math.MaxUint8), sql.ClampFloatToInteger[uint8](0x1p8))
	require.Equal(t, uint8(0), sql.ClampFloatToInteger[uint8](-1))
	require.Equal(t, int16(math.MaxInt16), sql.ClampFloatToInteger[int16](0x1p15))
	require.Equal(t, uint16(math.MaxUint16), sql.ClampFloatToInteger[uint16](0x1p16))
	require.Equal(t, int32(math.MaxInt32), sql.ClampFloatToInteger[int32](0x1p31))
	require.Equal(t, uint32(math.MaxUint32), sql.ClampFloatToInteger[uint32](0x1p32))
}
