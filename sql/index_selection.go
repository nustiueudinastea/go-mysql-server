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

import "strings"

// ChooseIndex selects an index without statistics. match reports the number of
// leading columns constrained by equality and whether the next column has a
// range constraint. A negative prefix excludes an ineligible index.
// Complete primary and unique keys precede other indexes; otherwise the longest
// equality prefix wins, followed by a range on the next column and index name.
func ChooseIndex(indexes []Index, match func(Index) (int, bool)) Index {
	var best Index
	bestClass, bestPrefix, bestRange := -1, 0, false
	for _, idx := range indexes {
		if idx.IsSpatial() || idx.IsFullText() || idx.IsVector() {
			continue
		}
		prefix, hasRange := match(idx)
		if prefix < 0 || (prefix == 0 && !hasRange) {
			continue
		}
		class := 0
		if prefix >= len(idx.Expressions()) {
			if strings.EqualFold(idx.ID(), "PRIMARY") {
				class = 2
			} else if idx.IsUnique() {
				class = 1
			}
		}
		better := best == nil || class > bestClass
		if best != nil && class == bestClass {
			if class > 0 {
				better = idx.ID() < best.ID()
			} else {
				better = prefix > bestPrefix || (prefix == bestPrefix &&
					((hasRange && !bestRange) || (hasRange == bestRange && idx.ID() < best.ID())))
			}
		}
		if better {
			best, bestClass, bestPrefix, bestRange = idx, class, prefix, hasRange
		}
	}
	return best
}
