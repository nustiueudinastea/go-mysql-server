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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/expression"
	"github.com/dolthub/go-mysql-server/sql/types"
)

func TestChooseIndex(t *testing.T) {
	index := func(name string, unique bool, cols ...string) sql.Index {
		idx := &memory.Index{Name: name, Unique: unique}
		for i, col := range cols {
			idx.Exprs = append(idx.Exprs, expression.NewGetField(i, types.Int32, col, false))
		}
		return idx
	}
	indexes := []sql.Index{
		index("PRIMARY", true, "id", "u"), index("a_unique", true, "u"), index("z_unique", true, "a", "b"),
		index("a_ab", false, "a", "b"), index("z_ac", false, "a", "c"), index("b_idx", false, "b"),
	}
	for _, tt := range []struct {
		name        string
		equal, rang []string
		want        string
	}{
		{"complete primary", []string{"id", "u", "a", "b"}, nil, "PRIMARY"},
		{"complete uniques tie by name", []string{"u", "a", "b"}, nil, "a_unique"},
		{"partial primary loses to complete unique", []string{"id", "a", "b"}, nil, "z_unique"},
		{"equality prefix", []string{"a"}, nil, "a_ab"},
		{"range breaks prefix tie", []string{"a"}, []string{"c"}, "z_ac"},
		{"range alone", nil, []string{"b"}, "b_idx"},
		{"equality precedes range", []string{"a"}, []string{"b"}, "a_ab"},
		{"uncovered prefix", []string{"c"}, nil, ""},
		{"uncovered range prefix", nil, []string{"c"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			contains := func(cols []string, target string) bool {
				for _, col := range cols {
					if col == target {
						return true
					}
				}
				return false
			}
			// Rotate and reverse candidates to exercise independent index enumeration orders.
			for run := 0; run < 10; run++ {
				candidates := make([]sql.Index, len(indexes))
				for i := range candidates {
					pos := (i + run) % len(indexes)
					if run%2 == 0 {
						pos = len(indexes) - 1 - pos
					}
					candidates[i] = indexes[pos]
				}
				got := sql.ChooseIndex(candidates, func(idx sql.Index) (int, bool) {
					prefix := 0
					for _, col := range idx.Expressions() {
						if !contains(tt.equal, col) {
							return prefix, contains(tt.rang, col)
						}
						prefix++
					}
					return prefix, false
				})
				if tt.want == "" {
					require.Nil(t, got)
				} else {
					require.Equal(t, tt.want, got.ID())
				}
			}
		})
	}
}
