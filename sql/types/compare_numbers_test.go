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

package types

import (
	"math"
	"testing"
)

func TestCompareNumbersBoundaryExact(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want int
	}{
		{"maxUint64_lt_2p64", uint64(math.MaxUint64), float64(0x1p64), -1},
		{"2p64_gt_maxUint64", float64(0x1p64), uint64(math.MaxUint64), 1},
		{"maxUint64_gt_prevFloat", uint64(math.MaxUint64), math.Nextafter(0x1p64, 0), 1},
		{"prevFloat_eq_uint", uint64(math.Nextafter(0x1p64, 0)), math.Nextafter(0x1p64, 0), 0},
		{"uint0_gt_negFloat", uint64(0), float64(-0.5), 1},
		{"uint1_lt_1p5", uint64(1), float64(1.5), -1},
		{"uint1_gt_0p5", uint64(1), float64(0.5), 1},
		{"uint_lt_posInf", uint64(math.MaxUint64), math.Inf(1), -1},
		{"uint_gt_negInf", uint64(0), math.Inf(-1), 1},
		{"uint_gt_nan", uint64(0), math.NaN(), 1},
		{"nan_lt_uint", math.NaN(), uint64(0), -1},

		{"maxInt64_lt_2p63", int64(math.MaxInt64), float64(0x1p63), -1},
		{"2p63_gt_maxInt64", float64(0x1p63), int64(math.MaxInt64), 1},
		{"maxInt64_gt_prevFloat", int64(math.MaxInt64), math.Nextafter(0x1p63, 0), 1},
		{"minInt64_eq_neg2p63", int64(math.MinInt64), float64(-0x1p63), 0},
		{"minInt64_gt_belowNeg2p63", int64(math.MinInt64), math.Nextafter(-0x1p63, math.Inf(-1)), 1},
		{"neg1_gt_neg1p5", int64(-1), float64(-1.5), 1},
		{"neg2_lt_neg1p5", int64(-2), float64(-1.5), -1},
		{"zero_gt_neg0p5", int64(0), float64(-0.5), 1},
		{"zero_lt_pos0p5", int64(0), float64(0.5), -1},
		{"int_lt_posInf", int64(math.MaxInt64), math.Inf(1), -1},
		{"int_gt_negInf", int64(math.MinInt64), math.Inf(-1), 1},
		{"int_gt_nan", int64(math.MinInt64), math.NaN(), 1},
		{"nan_lt_int", math.NaN(), int64(math.MinInt64), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareNumbers(tc.a, tc.b); got != tc.want {
				t.Fatalf("compareNumbers(%#v, %#v) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
			if got := compareNumbers(tc.b, tc.a); got != -tc.want {
				t.Fatalf("antisymmetry compareNumbers(%#v, %#v) = %d, want %d", tc.b, tc.a, got, -tc.want)
			}
		})
	}
}
