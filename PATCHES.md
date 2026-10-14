# Maintained patches

Upstream base: `d5acf30e85194e579501ca7712e6c2c6dc400e74`. Branch: `swarmion`.
Original fork patch: `c336135d2287120b574b22bb7c4332dc0312c507` — Fill the date of a TIME
converted to DATETIME from the query time (`sql/types/datetime.go`).
Each behavior patch has its own commit; the refined G5 commit also carries this inventory.

## G1 — Query-date conversion regression coverage

Commit: `d8a38607c492641026b8eab6364f577eb1b1ffd2` (tests-only follow-up to `c336135d2`).

Files:

- `engine_test.go`
- `sql/types/datetime_test.go`

`TestTimeToDatetimeQueryDate` fixes query time to 2001-02-03 and converts positive,
negative, and over-24-hour TIME values through `Datetime.Convert`.
`TestInsertTimeToDatetimeQueryDate` executes an INSERT of a TIME cast into a
DATETIME column with the same context, expecting 2001-02-03 12:34:56. MySQL fills
TIME's missing date with the current query date; the fork honors `QueryTime()`
so fixed query contexts do not read a process's wall-clock date. See
[MySQL date/time conversion](https://dev.mysql.com/doc/refman/8.4/en/date-and-time-type-conversion.html).

Evidence: both tests pass on parent `c336135d2`, because that parent already
contains the fix. Copying the test files onto detached upstream `d5acf30e8519`
makes both tests fail: the actual date is the current wall-clock date, rather
than 2001-02-03. A tests-only follow-up cannot fail on its already-fixed parent;
this is the regression against the original patch's parent, without rewriting
that patch.

## G2 — Multi-table UPDATE target order

Commit: `b0c94bc4b1e1bf43a5d09c98b771dc6a2e6737f6`.

Files:

- `engine_test.go`
- `sql/analyzer/apply_foreign_keys.go`
- `sql/analyzer/assign_update_join.go`
- `sql/plan/update.go`
- `sql/plan/update_join.go`
- `sql/rowexec/dml.go`
- `sql/rowexec/update.go`

Capture table-reference order when constructing the UPDATE, before join
optimization. Carry it through the UPDATE JOIN and foreign-key wrappers. Visit
target updaters in that order for creation, row updates, and editor lifecycle
callbacks; any targets absent from statement metadata use lexical alias order.
The row iterator uses the same ordering. Join execution order is unchanged.

This is a deterministic engine tie-breaker. MySQL does not guarantee a
particular multi-table UPDATE order and documents that optimizer-chosen order
can fail with parent/child FKs. Therefore this fork can differ from MySQL's
chosen order or success/failure for such statements. Two aliases of one physical
table retain the existing separate-update behavior, with the later table
reference now consistently winning. See
[MySQL UPDATE](https://dev.mysql.com/doc/refman/8.4/en/update.html).

Test: `TestUpdateJoinTargetOrder` repeats fresh-engine alias and cascading-FK
cases 100 times each. The child-first UPDATE followed by its parent's key update
leaves the child at `(parent_id,v)=(2,1)`; alias `z` followed by `a` leaves `v=2`.
The test copied onto parent `d8a38607c` fails: aliases leave `v=1` on
iteration 4, and the cascade case leaves `(1,1)` on iteration 1. Complete
`sql/plan`, `sql/analyzer`, `sql/rowexec`, and root packages pass.

## G3 — JSON_SEARCH object order

Commit: `c9068849e3936e719da357e6146ebb8f03474100`.

Files:

- `sql/expression/function/json/json_search.go`
- `sql/expression/function/json/jsontests/json_search_test.go`

Visit object keys by byte length, then lexical bytes, before recursion. This
matches JSON_KEYS and MySQL binary-JSON object ordering; arrays retain index
order. MySQL's public JSON_SEARCH contract leaves both the first match and the
`all` array order unspecified, so this is a compatible deterministic choice,
not a new ordering promise attributed to MySQL. See
[MySQL JSON_SEARCH](https://dev.mysql.com/doc/refman/8.4/en/json-search-functions.html).

Test: `TestJSONSearchObjectOrder` runs `one` and `all` 100 times for two-key,
ten-key, and nested-object/array inputs. It checks both length and lexical
ordering. On parent `b0c94bc4b`, the copied test fails with different paths
and array order. Both complete JSON packages pass.

## G4 — Bounds-checked floating-point integer conversions

Commit: `e0b621483e0d0ab09e4a4f4d3907a2799b0f3c18`.

Files:

- `memory/external_stored_procedures.go`
- `memory/stats.go`
- `sql/analyzer/costed_index_scan.go`
- `sql/events.go`
- `sql/expression/bit_ops.go`
- `sql/expression/bit_ops_test.go`
- `sql/expression/div.go`
- `sql/expression/function/conv.go`
- `sql/expression/function/format.go`
- `sql/expression/function/greatest_least.go`
- `sql/expression/function/sleep.go`
- `sql/expression/function/string.go`
- `sql/expression/function/string_test.go`
- `sql/float_conversion.go`
- `sql/float_conversion_test.go`
- `sql/index_builder.go`
- `sql/memo/coster.go`
- `sql/memo/rel_props.go`
- `sql/planbuilder/ddl.go`
- `sql/rowexec/insert.go`
- `sql/rowexec/show_iters.go`
- `sql/statistics.go`
- `sql/stats/helper.go`
- `sql/stats/join.go`
- `sql/types/bit.go`
- `sql/types/compare_numbers.go`
- `sql/types/datetime.go`
- `sql/types/enum.go`
- `sql/types/float_conversion_test.go`
- `sql/types/json_encode.go`
- `sql/types/number.go`
- `sql/types/set.go`
- `sql/types/system_bool.go`
- `sql/types/system_enum.go`
- `sql/types/system_int.go`
- `sql/types/system_set.go`
- `sql/types/system_uint.go`
- `sql/types/year.go`
- `sql/variables/system_variables.go`

Shared helper: `sql.ClampFloatToInteger[T]` truncates toward zero, saturates at
the destination's bounds, maps NaN to zero, and saturates infinities. Every type uses its **actual width**. Native `int`/`uint` are 64-bit on
arm64, amd64, and js/wasm; compile-time assertions reject any target where either
is not eight bytes. In-range conversions, including values above 2^31 and 2^53,
retain the upstream cast result. The system ENUM integrality guard also uses
the full signed 64-bit range. Callers retain their existing rounding and error
policies. All casts inside the helper occur only after representability checks.
`Float64IsInt64` provides exact integrality checks with an exclusive 2^63 upper
bound; comparing against float64(MaxInt64) would incorrectly accept 2^63.

Policies by caller:
- Bit operands and shift counts: retain rounding away from zero, saturate to
  signed int64, then reinterpret the bits as uint64. MySQL's `Item_float::val_int`
  also uses signed saturation before bitwise operators' unsigned interpretation.
  Existing GMS fractional rounding is preserved, although MySQL uses `rint`.
  Shifts by 64 or more remain zero. **Unary `~` is unsupported at this pinned
  GMS revision** (`SELECT ~1e100` returns `unsupported feature: unary operator:
  ~`); this patch does not introduce a new operator.
- HEX: retain the existing signed negative path and unsigned positive path for
  representable inputs. Negative overflow saturates to MinInt64. Positive inputs
  at or above 2^64 use MySQL's signed saturation and produce `7FFFFFFFFFFFFFFF`.
  NaN becomes `0`. Existing positive inputs in [2^63,2^64) still use GMS's unsigned
  path, which differs from MySQL 8.4's signed `val_int` path; changing that already
  deterministic range would exceed this patch's scope.
- BIN: numeric and parsed-string floats truncate and saturate as signed int64;
  NaN becomes zero. The pre-existing negative byte formatter is preserved
  (MinInt64 renders `100000000000000`, unlike MySQL's 64-bit representation).
- Number types, including `sql.Value`: retain existing rounding; signed overflow
  and underflow saturate with range flags, and NaN errors. For negative unsigned
  floats, preserve the public converter's complement rule, now explicitly
  `MaxUint64 - clampUint64(-f-1)`, with Underflow. Use the same rule for float32
  and typed values. Thus -2^63 gives MaxInt64, while -2^64, -1e100 and -Inf give 0.
  This deliberately preserves a GMS rule rather than claiming MySQL modulo
  semantics. Nonnegative unsigned values saturate at MaxUint64 on overflow.
- Mixed integer/float comparison: guard the signed/unsigned bounds before
  casting. Preserve precise integer tie-breaking and fractional comparisons;
  NaN sorts before finite integers, consistent with Go's `cmp.Compare` float
  ordering. JSON equality and containment inherit this through
  `compareJSONNumber` in `sql/types/json_value.go`; that file needs no edit.
- BIT rejects NaN and unsigned overflow; ENUM/SET/YEAR retain their integer
  validation after explicit saturation (unsigned negatives and NaN clamp to 0).
  System variables and integer index probes reject floats outside their exact
  representable domains. JSON serialization's integer fast path is guarded.
- Other unbounded scalar, AUTO_INCREMENT, event, SHOW/status, statistics and
  planner conversions use the helper. This defines their exceptional conversion
  results without changing their surrounding evaluation, error, or rounding
  policies. It does not address independent randomness, wall-clock reads,
  integer arithmetic overflow, or floating-point arithmetic differences.

The saturation policy follows MySQL where applicable; otherwise the explicit
exception rules above replace implementation-dependent Go casts. Relevant
primary sources: [MySQL overflow handling](https://dev.mysql.com/doc/refman/8.4/en/out-of-range-and-overflow.html),
[MySQL Item_float](https://github.com/mysql/mysql-server/blob/8.4/sql/item.h),
[MySQL bit operators](https://github.com/mysql/mysql-server/blob/8.4/sql/item_func.cc),
and [MySQL HEX](https://github.com/mysql/mysql-server/blob/8.4/sql/item_strfunc.cc).

Tests: `TestClampFloatToIntegerBoundaries`, `TestFloatIntegerBoundaries`,
`TestMixedFloatIntegerComparisonBoundaries`,
`TestOtherFloatIntegerBoundaries`, `TestBitFloatBoundaries`, and
`TestHexBinFloatBoundaries` check ±2^63, ±2^64, ±1e100, NaN, ±Inf, adjacent
representable values, typed-value conversions, JSON equality/containment,
serialization, integer-like types, both bit operands and shift counts, and
numeric/string BIN inputs. The shared-helper test adds native and named
integer types, narrow integer widths, both sides of 2^31 and 2^53, and adjacent
representable floats on both sides of ±2^63 and ±2^64. It also compares in-range
native conversions with the original Go casts. Expected values are explicit
constants, never the host CPU's overflowing conversion result.

Parent evidence from this revision: copy only `sql/types/float_conversion_test.go`,
`sql/expression/bit_ops_test.go`, and `sql/expression/function/string_test.go` onto
detached parent `c9068849e`. All five caller test functions fail on amd64
(Rosetta), including `1e100 & 1` returning 0 instead of 1 and incorrect HEX/BIN
results. All six patched tests pass natively on arm64, on amd64 via Rosetta,
and on js/wasm via Node. The new helper does not exist on the parent, so its
unit test is not copied there. A standalone 386 compilation of the helper
fails at both native-width assertions. Copying the new helper test onto the
replaced G4 `dcb63df3e` also fails: 2^31 incorrectly becomes 2147483647 and
values around 2^53 incorrectly hit the same 32-bit bound.

A typed AST scan of production `sql/...` and `memory/...` identified remaining
raw casts. They are guarded system-variable/JSON casts; sub-billion time
fractions and time-zone offsets; duration-bounded DATEDIFF/TIME_FORMAT operands;
decimal scale increments divided by nine; bounded reservoir queue sizes (4000);
and COUNT prefix sums bounded by allocated row buffers. `sql/testutils.go` is a
test helper. Unbounded reservoir skip counts and uptime conversion are patched.

## G5 — NULL-aware AntiHash cached misses

Commit: the G5 commit containing this entry, directly after
`e0b621483e0d0ab09e4a4f4d3907a2799b0f3c18`.

Files:

- `sql/plan/hash_lookup.go`
- `sql/rowexec/rel.go`
- `sql/rowexec/other_iters.go`
- `sql/rowexec/join_test.go`

While building an exclude-NULLs lookup, also collect the RHS rows whose key
contains a NULL component, preserving input order. Publish this bucket only
when the complete lookup is published at child EOF; abandoned iterations
publish neither. `Dispose` releases both. Key evaluation happens once, with
NULL detection before hashing, including scalar and tuple keys.

A cached probe with no NULL component returns its ordinary bucket on a hit.
On a miss, it returns only the NULL-key bucket: a different non-NULL key makes
the equality false and cannot affect NOT IN. A probe containing NULL replays
the whole child, normally `CachedResults`, for per-row comparisons, even if
its hash has a bucket. Empty buckets and empty children remain empty.
The extra storage is O(number of NULL-key RHS rows); a non-NULL miss replays
only that many rows, rather than the entire RHS. There is no map traversal.

`existsIter.Next` in `join_iters.go` rejects an anti-join LHS row when the
condition is TRUE or NULL. Tuple equality in `comparison.go` still returns
FALSE if any non-NULL component differs, so an unrelated RHS NULL must not
reject the LHS. Consequently every NULL-key candidate must be tested, and
no further miss candidates are needed. `other_iters.go` retains all RHS rows
in their normal hash buckets as well as collecting the NULL bucket.

Scope of the unchanged hit rule: an equality-key hit that evaluates TRUE
already rejects the LHS. The optimizer's `addHashJoins` requires every join
filter to be a key equality. An explicit plan with an extra residual predicate
can require more on a hit: with LHS `(1,1)`, RHS `(a,b,eligible)` rows
`(1,1,0),(1,NULL,1)`, and condition `key equality AND eligible=1`, the hit is
FALSE but the omitted NULL-key row is UNKNOWN. The ordinary Anti iterator
rejects this LHS, while AntiHash accepts it. A diagnostic in the local evidence
directory confirms this difference. A false-positive lossy hash hit whose
bucket evaluates entirely FALSE likewise needs the NULL bucket. These are
separate hit-path limitations; the coordinator's specified hit behavior is
retained here. The refined miss rule itself needs no additional candidates.

Exactness test: the original `TestAntiHashNotInCachedMiss` is retained unchanged:
five cases, 100 repetitions each, using an explicit AntiHash plan. LHS
`(3,3),(1,3)` and RHS `(1,NULL),(2,2)` return only `(3,3)`. Additional cases
cover a NULL on the LHS, an unrelated RHS NULL, a cached hit, and an empty RHS.
Expected results follow [MySQL row equality](https://dev.mysql.com/doc/refman/8.4/en/comparison-operators.html)
and [MySQL NOT IN](https://dev.mysql.com/doc/refman/8.4/en/any-in-some-subqueries.html);
no MySQL server was run.

Replay test: `TestAntiHashNotInCachedMissReplay` drains the actual lookup
iterator for each LHS probe, independently of anti-join early rejection.
It uses 10,000 RHS rows and 128 cached misses, with scalar and tuple keys,
with and without four NULL-key rows. It checks the per-probe upper bound and
the complete ordered candidate list, so returning one arbitrary row cannot
pass. It also covers an abandoned first iteration, duplicate hits, NULL probes
in either tuple component, and a NULL probe whose hash has a bucket.

Measured replay counts, for both scalar and tuple keys:

- Four NULL-key rows: 4 per non-NULL miss, 512 over 128 probes.
- No NULL-key rows: 0 per non-NULL miss, 0 over 128 probes.
- Duplicate hit: 2 rows. NULL probes: all 10,000 rows in original order.
- Initial complete cache build: 10,000 rows, outside the cached-probe counts.

Fail-on-parent evidence: copying only the final test file onto detached
`e0b621483` makes both tests fail. The exactness test incorrectly includes
`(1,3)` at iteration 29 and `(NULL,2)` at iteration 0. The replay test returns
one arbitrary row on a miss instead of four NULL-key rows (or zero rows when
there are no NULL keys). Copying the replay test onto replaced G5 `a62e5a236`
also fails: a non-NULL miss replays 10,000 rows instead of at most four.
The refined code passes both tests.

## Validation and reproduction

Every changed Go file was formatted with `gofmt`; `git diff --check` passes.
Default Go caches, git identity and signing configuration were used. Detached
regression worktrees are created inside this clone and removed after testing.
Local logs and replaced patch diffs remain in `.git/patch-evidence/`. The original
G5 diff, test, and inventory and this revision's logs are in
`.git/patch-evidence/g5-refined/`.

The following complete changed-package checks were run for G1-G4 and the
previous G5 revision, including the retained G1-G3 packages:

```sh
go test -p 2 -count=1 -tags=gms_pure_go . ./sql ./memory ./sql/types ./sql/analyzer ./sql/plan ./sql/memo ./sql/planbuilder ./sql/rowexec ./sql/stats ./sql/variables ./sql/expression/...
```

All listed packages pass except `sql/expression/function`'s pre-existing
`TestRegexpReplaceWithFlags` (four multiline subtests). The same four failures
are reproduced on parent `c9068849e` using `gms_pure_go`; they are unrelated to
these patches. All new regression tests pass.

The six G4 boundary tests pass with Go 1.26.5 in all three requested modes:

```sh
boundary_tests='Test(ClampFloatToIntegerBoundaries|FloatIntegerBoundaries|MixedFloatIntegerComparisonBoundaries|BitFloatBoundaries|HexBinFloatBoundaries|OtherFloatIntegerBoundaries)$'
# Native darwin/arm64
go test -p 2 -count=1 -tags=gms_pure_go ./sql ./sql/types ./sql/expression ./sql/expression/function -run "$boundary_tests"
# darwin/amd64 via Rosetta
GOARCH=amd64 go test -p 2 -count=1 -tags=gms_pure_go ./sql ./sql/types ./sql/expression ./sql/expression/function -run "$boundary_tests"
# js/wasm via Node
GOOS=js GOARCH=wasm go test -p 2 -count=1 -tags=gms_pure_go -exec "$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./sql ./sql/types ./sql/expression ./sql/expression/function -run "$boundary_tests"
# G5 exactness and replay regressions
go test -p 2 -count=1 -tags=gms_pure_go ./sql/rowexec -run '^TestAntiHashNotInCachedMiss' -v
# Expected compile failure on a 32-bit target
GOOS=linux GOARCH=386 go test -p 2 sql/float_conversion.go
```

This G5 refinement passes all of the following checks (including the complete
rowexec and plan packages):

```sh
go test -p 2 -count=1 -tags=gms_pure_go ./sql/rowexec/ ./enginetest/ -run 'AntiHash|NotIn|TestQueryPlans|TestJoinPlanning|TestIndexQueryPlans'
go test -p 2 -count=1 -tags=gms_pure_go ./sql/rowexec/ ./sql/plan/
```

All four changed Go files pass `gofmt`; `git diff --check` passes. This revision
replaces only former G5 `a62e5a236` and documentation `1d5321d8c`. Commits through
`e0b621483` and all other branches are unchanged. Nothing was pushed.
