# Maintained patches

Upstream base: `d5acf30e85194e579501ca7712e6c2c6dc400e74`. Branch: `swarmion`.
Original fork patch: `c336135d2287120b574b22bb7c4332dc0312c507` — Fill the date of a TIME
converted to DATETIME from the query time (`sql/types/datetime.go`).
Each behavior patch has its own commit. A shared validation commit carries
the G6/G7 inventory and remaining aggregate query-fixture updates.

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

## G6 — Exact SUM and AVG

Commit: `1ef05ba76b19781daba3dc795fb8a6feceb64d73`, appended to pushed G5
`e7eb0866b` without rewriting it.

Files (behavior commit and shared validation follow-up):

- `enginetest/enginetests.go`
- `enginetest/join_op_tests.go`
- `enginetest/memory_engine_test.go`
- `enginetest/queries/aggregation_queries.go`
- `enginetest/queries/charset_collation_engine.go`
- `enginetest/queries/create_table_queries.go`
- `enginetest/queries/derived_table_outer_scope_visibility_queries.go`
- `enginetest/queries/generated_columns.go`
- `enginetest/queries/join_queries.go`
- `enginetest/queries/name_resolution_queries.go`
- `enginetest/queries/priv_auth_queries.go`
- `enginetest/queries/procedure_queries.go`
- `enginetest/queries/queries.go`
- `enginetest/queries/set_operations_queries.go`
- `enginetest/queries/time_queries.go`
- `enginetest/queries/window_functions_queries.go`
- `numeric_aggregates_test.go`
- `optgen/cmd/source/unary_aggs.yaml`
- `sql/expression/function/aggregation/avg_test.go`
- `sql/expression/function/aggregation/numeric_aggs.go`
- `sql/expression/function/aggregation/numeric_aggs_test.go`
- `sql/expression/function/aggregation/unary_agg_buffers.go`
- `sql/expression/function/aggregation/unary_aggs.og.go`
- `sql/expression/function/aggregation/window_functions.go`
- `sql/expression/function/aggregation/window_iter_test.go`
- `sql/expression/function/aggregation/window_partition_test.go`
- `PATCHES.md` (shared inventory)

For an exact argument with precision p and scale s, SUM returns
DECIMAL(min(p+22,65),s). AVG returns DECIMAL(p+d,min(s+d,30)), with
session `div_precision_increment` d (default 4). Integer columns have scale
zero and precision 3/5/7/10/19 for signed TINYINT/SMALLINT/MEDIUMINT/INT/BIGINT,
and 3/5/8/10/20 for unsigned versions; integer literals use their digit count
(Boolean literals use one digit). Floating-point and string arguments return
DOUBLE. These rules follow
[MySQL's implementation](https://github.com/mysql/mysql-server/blob/8.4/sql/item_sum.cc)
and [precision math examples](https://dev.mysql.com/doc/refman/8.4/en/precision-math-examples.html).

Grouped SUM/AVG and DISTINCT variants accumulate exact inputs directly in
apd decimals, without float intermediates. Window SUM/AVG use independent
exact decimal prefixes and exact subtraction for moving frames. AVG retains
internal division precision; serialization rounds to the declared result scale,
and stored-column conversion applies the destination bounds. NULL-only and empty
inputs/frames still return NULL. Approximate arguments retain DOUBLE.

Previous MySQL discrepancies corrected:

- Both functions advertised DOUBLE for all arguments. Integer buffers also
  converted through DOUBLE, losing precision above 2^53. Grouped DECIMAL
  buffers sometimes emitted decimal despite DOUBLE metadata; windows always
  emitted DOUBLE and could lose DECIMAL precision.
- Exact AVG now uses the declared argument scale plus d, capped at 30, rather
  than unrestricted float digits or runtime decimal exponent plus a fixed 4.
  It now honors a nondefault division increment.
- Exact DISTINCT aggregates inherit these rules. MySQL disallows DISTINCT
  SUM/AVG windows; the existing SQL error tests require that same rejection.
- Exact accumulator conversion/add errors propagate instead of silent
  fallback to zero or discarded decimal-add errors.

Retained differences and scope:

- SUM's existing `IsNullable` metadata remains false, although empty/NULL-only
  sums return NULL. MySQL marks SUM nullable. Keeping it preserves all existing
  query-plan expectations.
- Aggregate precision follows GMS argument metadata. Many integer expressions
  have fixed-width metadata instead of MySQL's expression-specific precision.
  Argument evaluation itself can already have lost precision, for example
  inside GMS division's existing floating-point path; this patch cannot recover it.
- Internal decimal sums use arbitrary precision, unlike MySQL's fixed-size
  buffers. AVG also retains quotient guard digits beyond MySQL's finite buffer
  capacity. Stored-column conversion enforces destination precision/scale;
  bare expression serialization applies display scale without a column-shaped
  integer limit. Extreme values and overflow timing can therefore differ from
  MySQL. Errors retain GMS error types.
- Existing conversions of malformed strings, temporal values, ENUM/SET, BIT,
  and other non-integer/non-DECIMAL types remain unchanged and can differ from
  MySQL's numeric conversions. They remain on GMS's DOUBLE path. DISTINCT
  still hashes original string representations before numeric conversion, so
  equivalent spellings can remain distinct (the existing string-DISTINCT TODO).
- Decimal arithmetic can retain negative zero: `SUM(0) * -1` now exposes the
  inherited decimal text `-0`, unlike MySQL's decimal zero. SUM/AVG themselves
  produce numeric zero. Internal apd values can omit trailing zeroes; wire
  formatting uses the declared scale.

`TestExactNumericAggregates` checks schema precision/scale and exact wire bytes
for SUM, AVG, SUM DISTINCT, AVG DISTINCT, GROUP BY, and windows over INT,
near-limit BIGINT UNSIGNED, and DECIMAL(10,3), plus NULL-only inputs and moving
one-row frames with nonzero partition offsets. The unsigned sum is exactly
55340232221128654843; the distinct sum is 36893488147419103229; averages are
18446744073709551614.3333 and 18446744073709551614.5000.
`TestNumericAggregateTypes` checks every integer width, literals, caps and
approximate types. `TestExactAveragePrecisionIncrement` checks d=6 and exact
0.333333333. All three pass on native arm64 and amd64/Rosetta. Copying only
the new tests to detached parent `e7eb0866b` fails: SQL cases and type tests
report DOUBLE instead of DECIMAL, and d=6 reports DOUBLE instead of DECIMAL(16,9).

176 existing expectation entries change from float to decimal text or decimal
schema metadata. The G6 commit updates 125 entries: 13 in
`aggregation_queries.go`, 45 in `queries.go`, 67 in `window_functions_queries.go`.
They cover grouped/DISTINCT/CTE results, arithmetic consuming aggregates,
window partitions/frames, fractional AVG rounding, and two explicit column
types. Every query, changed column and old/new value is listed in the G6
commit message, including the negative-zero case. The shared validation commit
updates 51 more entries found by auditing the full enginetest suite: prepared
bindings, named windows, HAVING, aliases, generated columns, temporal scripts,
set operations, privileges, stored procedures, joins and CREATE TABLE AS SELECT
results/schema metadata. It lists every additional query and changed column
in its own message. The updated CTAS fixtures also fail when copied alone
to detached `e7eb0866b`. No query-plan expectations change.

## G7 — Context policy for cost-based plans

Commit: `47186e89bb5e2c0373fd013381b603527f233c9a`, directly after G6.

Files:

- `cost_based_plans_test.go`
- `sql/session.go`
- `sql/session_test.go`
- `sql/analyzer/batch.go`
- `sql/analyzer/indexed_joins.go`
- `sql/analyzer/costed_index_scan.go`
- `sql/analyzer/cost_based_plans_test.go`
- `sql/memo/join_order_builder.go`
- `sql/memo/memo.go`
- `sql/memo/cost_based_plans_test.go`
- `PATCHES.md`

`ctx.SetDisableCostBasedPlans(true)` enables an embedding-only policy; the
unexported context field defaults to false. Derived contexts (including spans
and subqueries) inherit it. SQL system/user variables cannot change it, and
a new context made from a plain context and sharing only the session starts
with the default. A SQL parent carries its policies into fresh contexts as well.
Set it before analysis.

The shared `Batch.evalOnce` skips `optimizeJoinsId`, including all three
hard-coded occurrences in `node_batches.go` (simple SELECT, UPDATE, DELETE).
Join optimization's direct entries also return the original tree.
Rule-based resolution, filter pushdown, subquery lowering and execution-index
assignment continue, producing executable nested-loop joins and scans.

The complete production costing call graph is gated:

- `optimizeJoins` alone calls `costedIndexScans`; both are gated, along with
  `inOrderReplanJoin` and `replanJoin`.
- `costedIndexScans` and memo `addIndexScans` call `getCostedIndexScan`; all
  three return before index/statistics inspection when disabled.
- `getCostedIndexScan` alone calls `indexCoster.cost`, which is also gated.
- `replanJoin` calls join `ReorderJoin` and memo optimization. Direct
  `ReorderJoin` callers keep the original graph without alternative enumeration.
- `Memo.optimizeMemoGroup` alone calls `Coster.EstimateCost`. Direct memo users
  instead choose the first implementation recursively, with no costing or
  cost-dependent DISTINCT choice. Plan descriptions tolerate absent statistics
  (zero/unknown estimates), so those plans build without cardinality collection.

With the flag false, optimizer behavior and plans are unchanged. When true,
cost-based index selection and hints processed by that optimizer are bypassed;
independent rule-based index lowering remains. MySQL has no equivalent context
API. Results retain SQL semantics, but plans, performance and unordered row
order can differ from MySQL's optimizer. The flag does not remove floating-point
arithmetic from SQL expressions.

`TestDisableCostBasedPlans` has 15 cases with failing memo/index-cost hooks:
simple SELECT, UPDATE, DELETE, inner/left/right/USING joins, IN/NOT IN/
correlated/EXISTS subqueries, INSERT SELECT/JOIN, UPDATE JOIN and DELETE JOIN.
All produce expected rows with zero calls and no cost-chosen physical joins.
Each clears the flag and proves both hooks receive calls again.
`TestCostBasedPlanFlagIsNotSQLSettable` and `TestDisableCostBasedPlansContext`
check SQL isolation, defaults, inheritance and independent derived setters.
`TestDisableCostBasedPlanEntryPoints`, `TestDisableCostBasedPlansInEveryBatch`
and `TestDisableCostBasedMemoPlans` cover direct entries, continued lowering,
absence of alternatives and building a memo plan without a coster.

Parent evidence: tests copied to G6 `1ef05ba76` first fail to compile because
the API is absent. Applying only the context field/accessor diff makes them
compile without changing optimization: all 15 execution cases reach a failing
cost hook, the batch test calls the forbidden rule, and the memo test enumerates
alternatives. The API-only diff and both runs remain in the evidence directory.

## G7b — deterministic index choice without cost-based plans

G7b supersedes G7's scan-only analyzer behavior. With
`ctx.SetDisableCostBasedPlans(true)`, the shared batch substitutes
`ruleBasedIndexPlans` for `optimizeJoinsId`, including the shortened SELECT,
UPDATE and DELETE batches. The direct optimizer entry uses the same lowering.
The costed-index and memo entry guards remain in place.

Files:

- `sql/index_selection.go`, `sql/index_selection_test.go`
- `sql/analyzer/rule_based_index.go`
- `sql/analyzer/batch.go`, `sql/analyzer/indexed_joins.go`
- `sql/analyzer/costed_index_scan.go`
- `sql/analyzer/index_analyzer.go`
- `sql/analyzer/apply_indexes_from_outer_scope.go`
- `sql/analyzer/replace_sort.go`
- `sql/plan/alter_foreign_key.go`
- `cost_based_plans_test.go`, `rule_based_index_test.go`
- `scripts/check_rule_based_plans.sh`, `PATCHES.md`

`sql.ChooseIndex` compares integer equality-prefix lengths and a boolean for a
range on the next column. Complete primary keys win, then complete unique keys
(ties by index ID), then longest equality prefix, then a range on the next
column, then index ID. Names compare lexicographically. With no usable prefix
or leading range, the access remains a table scan. Spatial, full-text and vector
indexes cannot implement these ordinary scalar lookups. Partial indexes still
require their predicate; foreign-key eligibility retains its existing checks.

Static filtered accesses reuse the existing filter decomposition and the
extracted `buildIndexScan` range/lookup construction, without statistics,
histograms, functional-dependency costing or cost hooks. Residual and imprecise
filters remain in the plan. Aliases and column pruning are preserved. ORDER BY
can reuse the chosen lookup's ordering, but cannot select another index for a
scan fallback. SQL expression arithmetic is unaffected.

Join lowering retains the existing join tree, with no join enumeration or
hash/merge selection. Eligible inner, left and semi joins use the same selector
on equality lookup keys, including right-side constant equality filters, and
reuse `LookupBuilder`. Other joins remain nested loops where a lookup would
lose preserved rows or null candidates. Existing semantic lowering, including
RIGHT JOIN normalization, is unchanged. Correlated-subquery lookups use the same
selector and keep additional equality predicates as residual filters. FK checks
use the selector in `FindFKIndexWithPrefix`; DML row selection uses the same
filtered-access lowering as SELECT. Embeddings still implement physical indexes
and their own storage-level mutation operations.

With the flag false, all original selection branches and range construction
remain unchanged. No default plan fixtures were edited.

Validation:

- All 20 index-choice cases, plus a correlated unique-key lookup, select the same
  index and plan across ten runs. Nine selector cases rotate/reverse index lists
  across ten runs, including composite PKs, unique ties, gaps and range ties.
- Instrumented 100,000-row tables count binary-search probes, key checks and
  iterator reads. PK/unique UPDATE and DELETE each examine 19 rows. Child insert
  examines 19 parent rows; a rejected parent delete examines 19 parent and 19
  child rows. Every statement asserts at most 64 total reads, and checks its
  mutation or FK violation.
- All six large-table subtests fail on `896ecf4ab`: the four ordinary DML cases
  read 100,000 rows and parent delete reads 50,000. Child insert already had an
  indexed FK check, but selects `A_id` instead of the required `PRIMARY`.
  Index-choice and corpus regressions also fail on that parent.
- The fixed-schema corpus contains 50 queries. The reproduction script writes
  separate arm64 and amd64 plan files, runs both with `gms_pure_go`, and compares
  them with `cmp`. Native arm64 and amd64/Rosetta outputs are byte-identical.
- `go test -p 2 -count=1 -tags=gms_pure_go ./ ./sql ./sql/analyzer ./sql/plan`
  passes. The requested enginetest selection
  `TestQueryPlans|TestJoinPlanning|TestIndexQueryPlans|TestQueries` passes with
  unchanged expectations. Gofmt and `git diff --check` pass.

Logs, parent regression evidence and both architecture plan files are retained
in `.git/patch-evidence/g7b/`. Reproduce the architecture comparison with
`scripts/check_rule_based_plans.sh /path/to/output-directory` on an arm64 host
with amd64 execution support. This check does not alter Go cache settings.

## G8 — Context validators for analysis and planning

Commit: the G8 commit containing this entry, appended to `6454d622b`.

`ctx.SetValidators(node func(*sql.Context, sql.Node) error,
expr func(*sql.Context, sql.Expression) error)` installs optional embedding-only
validators. Both default to nil. Derived contexts inherit the callbacks by value;
a fresh context sharing the session starts without them. Passing nil clears a
callback. SQL system/session variables cannot set them, and similarly named user
variables have no effect. Install validators before building the statement.
`ValidateNode` and `ValidateExpression` wrap rejections with `%w`, preserving
`errors.Is`. Callbacks receive the current derived context and may inspect the
supplied object's children; they may run repeatedly on the same object.

Call sites:

- `sql/planbuilder/scalar.go`: validate each resolved `buildScalar` return after
  bind-type fixup, including CHECK, DEFAULT, generated and ON UPDATE definitions.
- `sql/analyzer/analyzer.go`: validate each input before rules and each successful
  output after the last selected batch and memo lowering. The common selector
  includes shortened SELECT/UPDATE/DELETE batches, recursive subqueries, trigger
  bodies, and paths with no batches. EXPLAIN's wrapper is also validated.
- Immediately before all 15 expression `Eval` calls in analyzer/planbuilder:
  constant folding (`optimization_rules.go`), enum defaults and normalization
  (`resolve_column_defaults.go`), LIMIT/OFFSET (`validation_rules.go`), both index
  bound evaluations (`costed_index_scan.go`), SET (`planbuilder/set.go`), OFFSET
  (`select.go`), character-set introducers (`scalar.go`), KILL connection IDs
  (`process.go`), AS OF (`show.go`), and procedure AS OF (`proc.go`).
- `sql/columndefault.go`: guard `CheckType`'s indirect analysis-time evaluation.
  Validator errors bypass existing ordinary-evaluation fallback handling.
  Index-bound rejections propagate through both costed and rule-based planning.
- Precision inspection in `planbuilder/ddl.go` now reads the known literal's
  `Value()` directly instead of evaluating it with a nil context.

`sql/analyzer/eval_inventory_test.go` inventories every production `Eval`
reference in both source trees, plus `columndefault.go`. It checks counts and
immediately preceding validation guards. The batch runner is classified as
non-expression evaluation; runtime default delegation is exempt because its
planning caller validates it. A temporary new Eval site was injected: the test
failed with `UNCLASSIFIED`, then passed after that temporary file was removed.

Tests in `context_validators_test.go`, `sql/session_test.go`,
`sql/analyzer/context_validators_test.go` and
`sql/planbuilder/context_validators_test.go` cover:

- Zero evaluations of rejected counted functions in scalar SELECTs, folding in a
  SELECT filter, dead CASE branches, column defaults, CHECK and generated
  definitions, and a bound LIMIT value; separate analyzer tests install the
  callback only after building and prove the unguarded controls evaluate.
- Direct default normalization, default type checking, enum-default and index
  bound evaluation; LIMIT/OFFSET, SET, AS OF and introducer rejection;
  bind-type fixup, ON UPDATE, SQL isolation, inheritance and independent setters.
- Entry rejection before rules and final-plan validation for simple SELECT,
  UPDATE, DELETE, INSERT SELECT, joins, subqueries, EXPLAIN, COMMIT, CREATE TRIGGER
  and the analyzed body of an installed trigger used by UPDATE.

Parent evidence: copying the tests to detached `6454d622b` fails to compile
because the API is absent. Applying only `sql/session.go`'s context API lets the
tests run: behavioral regressions and the unguarded-source inventory fail.
Folded SELECT/dead CASE and default evaluation counters are 1 instead of 0;
index IN bounds evaluate twice. Parent callbacks never see the final plans.
The standalone context API tests pass with that API-only patch.

Validation (default caches, identity and signing; no existing fixtures changed):

```sh
go test -p 2 -count=1 -tags=gms_pure_go . ./sql ./sql/analyzer ./sql/planbuilder
go test -p 2 -count=1 -tags=gms_pure_go ./enginetest/ -run 'TestQueryPlans|TestJoinPlanning|TestIndexQueryPlans|TestQueries'
```

Both checks pass, as do gofmt and `git diff --check`. The known 42 TestRegex
failures documented under G6/G7 are outside this requested enginetest selection;
no failures occur in G8's required checks. Evidence, the API-only parent patch,
and the inventory injection failure are in `.git/patch-evidence/g8/`.

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

## G6/G7 validation and reproduction

These patches append to pushed `e7eb0866b`. Prior commits and other branches
are unchanged; nothing is pushed. Both commits use default identity/signing.
Evidence and expectation inventory are in `.git/patch-evidence/g6-g7/`;
detached regression worktrees are removed.

Complete tests pass for every changed Go package except enginetest's
pre-existing TestRegex failures. The full enginetest suite was also run to
check query fixtures outside the requested filter. The YAML source has no
Go package; its generator support package is tested:

```sh
go test -p 2 -count=1 -tags=gms_pure_go . ./sql ./sql/analyzer ./sql/memo ./sql/expression/function/aggregation ./enginetest/queries ./optgen/cmd/support
go test -p 2 -count=1 -tags=gms_pure_go ./enginetest/ -run 'TestQueryPlans|TestJoinPlanning|TestIndexQueryPlans|TestQueries|TestAggregat|TestWindow'
go test -p 2 -count=1 -tags=gms_pure_go ./enginetest/
```

All requested query, aggregate, window and plan checks pass. G7 changes none
of their expectations; TestQueryPlans, TestJoinPlanning and TestIndexQueryPlans
pass unchanged. No pre-existing failures occur in these required checks. The
four previously documented TestRegexpReplaceWithFlags failures belong to the
unchanged `sql/expression/function` package. The additional full enginetest run
has 42 TestRegex subtest failures, all reproduced on untouched `e7eb0866b`;
the parent and final failure-name sets are identical, with no new failures.
Its complete suite passes outside that pre-existing failure set.

G6 numeric tests pass in both requested modes:

```sh
numeric_tests='TestExactNumericAggregates|TestNumericAggregateTypes|TestExactAveragePrecisionIncrement'
go test -p 2 -count=1 -tags=gms_pure_go . ./sql/expression/function/aggregation -run "$numeric_tests"
GOARCH=amd64 go test -p 2 -count=1 -tags=gms_pure_go . ./sql/expression/function/aggregation -run "$numeric_tests"
```

The owner waived wasm for G6. All changed Go files pass gofmt and
`git diff --check`. Default Go caches are used; no caches are overridden or cleaned.

### Review follow-up: caller policies in fresh contexts

Fresh `sql.NewContext` calls inherit G7's flag and both G8 validators when their
parent is a `*sql.Context`. Stored procedure statements pass that parent itself;
this also covers procedures invoked by triggers. The new context still gets its
own query time and option-selected session. `WithContext` already copies all
policies and carries integrator context values (including Dolt D3).

`TestFreshContextInventory` inventories production creation sites, with explicit
classifications for caller propagation, initialization, metadata and formatting.
Analyzer and subquery helpers use copy-derived contexts. Triggers use the caller;
scheduled events execute in contexts supplied by the embedding scheduler's factory,
which is an independent entry point and must install its own policies.
`TestCallerPoliciesInProcedureAndTriggerBodies` rejects a precreated procedure's
function without evaluation and observes zero cost hooks for joined bodies, both
for CALL and a trigger invoking CALL. All four cases failed before this fix.
`TestCallerPoliciesInDirectTriggerBody` also covers inline trigger expressions
and joined subqueries. Both direct cases already passed on initial e94658573;
no direct-trigger production change was needed.

### Review follow-up: content-hashed index eligibility

Costed and rule-based index selection share operator eligibility, including the
predicates included in lookup construction. Unprefixed unique TEXT/BLOB index
fields can answer equality and NULL operations, but cannot answer ordering ranges.
The same restriction applies to costed conjunctions. Specialized index operators
are confined to their matching index class. Ineligible ranges remain residual
filters or fall back to a scan.

`TestRuleBasedContentHashedIndexRange` models legacy hash ordering, where `b` sorts
before `a`: the pre-fix `s > 'a'` lookup returned only ID 3. The scan fallback returns
IDs 2 and 3, including range and conjunction cases.

### Review follow-up: validate before function construction

The planbuilder validates every already-built argument tree immediately before
scalar, aggregate, window and table function construction. `NewUTCTimestamp` also
validates before its eager argument evaluation, including direct constructor calls;
`NewUnixTimestamp` now reads literal metadata with `Value` instead of `Eval`.
`TestConstructorEvalInventory` reviews every production constructor in
`sql/expression/function` and requires the UTC_TIMESTAMP evaluation guard.

`TestValidateFunctionArgumentsBeforeConstruction` and the direct constructor test
both evaluated their rejected argument once before this fix and now evaluate it
zero times. The review's GMS CREATE TABLE with SLEEP was already rejected at G8's
scalar-return hook (`TestCreateTableRejectsSleepBeforeConstruction` passed before
this follow-up); Dolt's separate D3 hook required pre-constructor integration.

The constructor inventory also follows calls to package-local helpers. This found
`NewUnixTimestamp`'s `evalNowType` helper reading a precision literal through Eval;
it now reads Value directly. Moving an Eval into a new helper cannot bypass the
inventory. The inventory failed on this helper before its removal.

The fresh-context inventory includes raw Context literals and allocations as well
as constructor calls. The factory's literal is explicitly classified; new raw
allocations fail the inventory until reviewed.


### Review follow-up: AVG precision and aggregate result boundaries

The four numeric findings are fixed together. AVG divides with integer quotient
and remainder, retaining unrounded quotient digits and a real guard digit derived
from the remainder. Both grouped and moving-frame paths use this helper. Integer
arithmetic supplies enough fractional capacity for the argument scale, division
increment and row-count magnitude. This prevents double rounding and preserves
positive values below the display scale. AVG expression precision is p+d, including
69 digits for a DECIMAL(65,0) input at the default increment; the 65-digit limit
still applies to declared stored columns. Aggregate and internal decimal conversion
retain expression precision instead of applying column-shaped bounds.

This separates expression evaluation from result formatting as in MySQL 8.0's
[item_sum.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.36/sql/item_sum.cc#L2159)
and [expression metadata setter](https://github.com/mysql/mysql-server/blob/mysql-8.0.36/sql/item.h#L1330).
The [decimal division implementation](https://github.com/mysql/mysql-server/blob/mysql-8.0.36/strings/decimal.cc#L2064)
uses nine-digit fractional groups. GMS uses arbitrary-size coefficients and extra
guards, so extreme internal precision and overflow timing remain a documented
difference from MySQL's finite decimal buffers.

A **result boundary** here means text/wire serialization through the returned
column type's SQL or SQLValue method (including the server's RowToSQL path), or
conversion to an explicitly declared DECIMAL column/cast type when storing a
value. These boundaries round once at their declared scale. Intermediate plan
rows and Go RowIter results retain the internal apd value so enclosing expressions
can compare or compute with it. Copying an intermediate row is not a boundary.
Aggregate serialization emits all declared fractional digits, including zeroes.
The existing apd negative-zero behavior in decimal arithmetic remains unchanged.

Regressions: `TestAverageReviewRegressions` failed before the fix for 5/11 (grouped
and moving frames), zero division increment, AVG(v)>0 for 0,0,1e-30, and both
65-digit AVG forms. `TestAverageDeclaredScale` failed with wire 0 rather than
0.0000. Existing G6 assertions now compare exact wire strings; the division unit
test compares 64 signed quotient cases to independently computed rational rounding.

Only four enginetest query fixtures changed bytes, all in queries/queries.go:

- Recursive CTE SUM over SUM(1) and 2.00: 3.00 becomes
  3.000000000000000000000000000000.
- Recursive CTE SUM over SUM(1) and 2.00/3.0: 1.666667 becomes
  1.666667000000000000000000000000.
- SUM(DISTINCT CAST(i AS DECIMAL)) from mytable: 6 becomes
  6.000000000000000000000000000000.
- The one_pk_three_idx query selecting FIRST_VALUE, LAG, COUNT, MAX and window
  AVG(v2): the AVG zeroes at pk 0, 1, 4 and 5 become 0.0000.

The first three honor existing GMS metadata at scale 30; this does not change its
unspecified-CAST or union metadata rules. The enginetest decimal normalizer uses
result-type formatting so internal guard digits do not appear as display bytes.
Plan fixtures are unchanged. Before/after logs and architecture checks remain in
.git/patch-evidence/review-fixes/.


All review-fix changed-package checks pass except the four pre-existing multiline
RegexpReplace failures, whose names match an initial-source overlay exactly.
The required enginetest selection passes; complete enginetest has only the same
42 TestRegex failures as the earlier untouched-parent evidence. The numeric
regressions and independent rational-rounding tests pass on native arm64 and
amd64 through Rosetta. All checks use -p 2, -count=1 and gms_pure_go, with default
Go caches. Gofmt and whitespace checks pass.


Review-fix commit index (all appended, signed, and unpushed):

- Caller inheritance: 2572e684f; allocation inventory: 8d6d61f25; direct trigger
  regression coverage: cacc31507.
- Shared content-hash eligibility: 3471ba1a8.
- Pre-constructor validation: ed7938d89; helper evaluation inventory: d95c511dd.
- Numeric findings 4–7 and their result-boundary/byte expectations: 00f705502.

Dolt's corresponding fixes are 6da743602 (resolver/wrapper contexts), 8043a8875
(generic row iterators), b55f914e5 (pre-constructor adapters) and 08b85381d
(coverage of all three schema resolver paths).
