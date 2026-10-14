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

package sql

import (
	"math"
	"unsafe"
)

type floatInteger interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Swarmion requires 64-bit native integers, including on js/wasm. Fail at
// compile time on targets with a different width.
var _ [8]byte = [unsafe.Sizeof(int(0))]byte{}
var _ [8]byte = [unsafe.Sizeof(uint(0))]byte{}

// ClampFloatToInteger truncates f toward zero, saturating at the integer bounds.
// NaN becomes zero. Infinities saturate like other out-of-range values.
// Every destination uses its actual width, including native int and uint.
// Callers that need errors, rounding, or signed-to-unsigned wrapping must apply
// those policies separately. No out-of-range Go float-to-integer cast is made.
func ClampFloatToInteger[T floatInteger](f float64) T {
	var zero T
	if math.IsNaN(f) {
		return zero
	}
	bits := int(unsafe.Sizeof(zero) * 8)
	if ^zero < zero {
		limit := math.Ldexp(1, bits-1)
		max := (T(1) << (bits - 1)) - 1
		if f >= limit {
			return max
		}
		if f < -limit {
			return -max - 1
		}
	} else {
		if f <= 0 {
			return zero
		}
		if f >= math.Ldexp(1, bits) {
			return ^zero
		}
	}
	return T(f)
}

// Float64IsInt64 reports whether f is an integral, representable int64. The
// exclusive upper bound is 2^63: float64(MaxInt64) rounds up to that value.
func Float64IsInt64(f float64) bool {
	return f >= -0x1p63 && f < 0x1p63 && math.Trunc(f) == f
}
