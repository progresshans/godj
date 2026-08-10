---
id: GDJ-0028
status: active
updated: 2026-08-11
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "e9dc361f983f1c02af1f63737a1f282998d5a533"
depends_on: ["GDJ-0027"]
contracts: ["REL-012", "Q-013"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "query/error.go"
  - "query/plan.go"
  - "query/plan_test.go"
  - "orm/reverse_prefetch.go"
  - "orm/reverse_prefetch_test.go"
  - "orm/reverse_prefetch_external_test.go"
  - "db/sqlite/compiler.go"
  - "db/sqlite/compiler_test.go"
  - "db/sqlite/integration_test.go"
  - "codegen/project_relation_prefetch.go"
  - "codegen/project_relation_prefetch_test.go"
  - "codegen/testdata/relation_prefetch/**"
  - "conformance/README.md"
  - "conformance/contracts/relation-manifest.json"
  - "conformance/relationprefetchproduct/**"
  - "conformance/runners/godj/relation_scenarios.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/runners/django/tests/test_relation_scenarios.py"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/internal/protocol/product_compare_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_prefetch/**"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0028-reverse-foreign-key-prefetch.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0027-reverse-foreign-key-accessor-and-lookup-product-slice.md"
  - "work/0028-reverse-foreign-key-prefetch-product-slice.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Reverse ForeignKey Prefetch Product Slice

## 결과 목표

GDJ-0028은 locked Django 6.1 `REL-012` 하나를 bounded SQLite product slice로 구현합니다. Existing generated
Manager/QuerySet이 primary Author query를 소유하고, 별도 generated project prefetch surface가 그 결과를 받아
source ForeignKey `IN` batch exactly one으로 평가한 뒤 owner별 `RelatedSet` cache를 원자적으로 warm합니다.

1. Author IDs 1, 2, 3을 primary-key order로 load한 뒤 `Posts` prefetch를 수행하면 exact
   `[(1,[10,11]),(2,[]),(3,[12])]`입니다.
2. Metric window의 SQL은 primary SELECT 1 + `blog_post.author_id IN (1,2,3)` batch SELECT 1, total SELECT 2,
   JOIN 0입니다. Batch key count는 3이고 subsequent exact `.Posts().All()` access는 extra query 0입니다.
3. 모든 source row와 grouping을 검증하기 전에는 어느 owner cache도 공개하지 않습니다. 실패/cancellation은
   nil output, partial publication 0이며 독립 retry가 가능합니다.
4. Database state는 불변입니다.

완료 aggregate 목표는 exact 12 adapter sets/127 contracts =
`116 passing + 5 deviation + 6 oracle_locked`, relation actual REL-001/003/004/005/006/012 6/12입니다. 이 수치는
implementation, oracle-blind actual과 exact-head hosted acceptance 뒤의 target일 뿐 activation 현재 분류가
아닙니다. 현재는 exact `115 + 5 + 7`, relation 5/12입니다.

## 기준 상태와 선행 증거

- Baseline은 clean `codex/revision-fenced-migration-lifecycle@e9dc361f983f1c02af1f63737a1f282998d5a533`입니다.
- [EVID-20260811-050](../docs/status/TEST_EVIDENCE.md#evid-20260811-050--gdj-0027-terminal-exact-head-ci-and-gdj-0028-activation-baseline)의
  [run 31424055711](https://github.com/progresshans/godj/actions/runs/31424055711)은 baseline exact head에서
  26/26 jobs와 326/326 recorded steps를 성공했습니다. Four relation-product coordinates는 각각
  569/569/0, 57,738 bytes, SHA-256
  `739bb6fc4bc3a5665cbaa455bed45d4ddf9683d78c4ff74b02c1d0208862c2d7`입니다.
- EVID-050은 clean baseline만 증명합니다. 이 activation diff, Proposed API, REL-012 implementation 또는 later
  head의 evidence로 재사용하지 않습니다. Activation diff exact-head CI는 아직 `not run/pending`입니다.
- [ADR-0023](../docs/adr/0023-symbolic-relation-binding-and-shared-relation-ast.md),
  [ADR-0026](../docs/adr/0026-forward-foreign-key-object-cache-and-nullability.md)과
  [ADR-0027](../docs/adr/0027-reverse-foreign-key-accessor-and-lookup.md)의 immutable binding, sealed descriptor,
  QuerySet evaluation cache와 generated reverse object ownership을 보존합니다.
- Baseline product는 exact `115 passing + 5 deviation + 7 oracle_locked`, relation actual
  REL-001/003/004/005/006 5/12입니다.

## Locked REL-012 외부 동작

Fixture는 existing authors/blog 의미를 그대로 사용합니다.

- authors: `(1, Ada)`, `(2, Bob)`, `(3, Cleo)`
- posts: `(10, Alpha, author=1, reviewer=2)`, `(11, Beta, author=1, reviewer=NULL)`,
  `(12, Gamma, author=3, reviewer=2)`
- reverse declarations: `Author.posts <- Post.author`, `Author.reviewed_posts <- Post.reviewer`

Stable actual observation은 다음을 모두 비교합니다.

- result: `[(1,[10,11]),(2,[]),(3,[12])]`
- primary query count 1, batch query count 1, total query count 2
- batch predicate column exact `author_id`, batch key count 3
- JOIN kinds `[]`, inner/left JOIN count 0
- returned wrapper의 exact `Posts().All()` consumption extra query 0
- unchanged authors/posts DB state

The frozen cross-runtime/protocol payload ends at those fields and statement kinds exact `[SELECT, SELECT]`; it does not
add a bound-argument vector or mutation-count field. Exact sorted integer arguments `[1,2,3]`, source-PK ordering and
mutation-free SQL trace are Go compiler/product internal false-green gates that must pass before adapter success is
published, while oracle/static/protocol bytes and shape remain frozen.

Primary Author query는 new prefetch API 내부로 숨기지 않습니다. Caller가 existing QuerySet으로 owners를 load한
뒤 그 slice를 generated `Posts` method에 전달합니다. Empty/duplicate/permuted/cap/error tests는 runtime contract
gates이며 reference payload를 넓히지 않습니다.

## Public runtime API freeze

Exact additive ORM surface는 다음뿐입니다.

```go
type ReversePrefetch[Owner, Source any] struct { /* private sealed state */ }

func BindReversePrefetch[Owner, Source any](
	reverse ReverseObject[Owner, Source],
) (ReversePrefetch[Owner, Source], error)

func (p ReversePrefetch[Owner, Source]) Load(
	ctx context.Context,
	backend db.Queryer,
	owners []Owner,
) ([]*RelatedSet[Source], error)
```

- `ReversePrefetch` is a separate immutable/copyable capability. It is not embedded into or aliased with
  `ReverseObject`.
- `BindReversePrefetch` validates the existing reverse object's sealed state and additionally binds the source
  ForeignKey `RelationStorage[Source]`. This does not strengthen or change `BindReverseObject`.
- Existing `ReverseObject`, `RelatedSet`, Manager/QuerySet signatures, behavior and generated reverse bytes are
  unchanged.
- `Load` owns exactly one batch query. It never runs the primary owner query and does not create a global registry,
  background goroutine or cross-call singleflight.

## Immutable IN condition freeze

Exact additive query surface is:

```go
const LookupIn Lookup = "in"

func NewInCondition(field FieldRef, values []Value) (Condition, error)
func (c Condition) Values() ([]Value, bool)
```

- `NewInCondition` requires `FieldRef.Name` and `Column` nonempty and containing no NUL, and kind exactly one of
  integer/string/boolean; nullable may be either value and no additional ASCII/SQL-name restriction is imposed beyond
  existing compiler quoting. It then accepts a nonempty list containing no NULL and whose every value has that exact kind.
  Invalid/unsupported fields and empty, NULL or wrong-kind lists fail `query_error/invalid_plan`; a zero FieldRef plus
  zero Value cannot false-pass kind equality.
- Input is cloned. Condition retains a private pointer to an immutable cloned list so `Condition` remains Go-comparable;
  `clone`, `Plan` copy-on-write and `Condition.Equal` compare list contents deeply. `Values` returns a fresh clone and
  reports `true` only for a valid list-backed condition.
- `Condition.Value()` returns the zero `Value` for `LookupIn`. Existing scalar constructors and conditions retain exact
  behavior.
- Scalar `NewCondition(field, LookupIn, scalar)` is not a supported IN representation. SQLite compiler rejects it before
  backend I/O with `query_error/invalid_plan` rather than guessing a one-element list.
- This packet adds no typed public `.In`, dynamic `__in`, RelatedSet filter, public warm injection or general bulk API.

SQLite compiles a valid root-table IN condition to the exact source FK column plus one placeholder per value, preserving
the already sorted list order in arguments. Empty parentheses, NULL members, mixed kinds, relation-path IN and scalar
misuse fail before backend I/O. Existing exact/isnull/related scalar SQL, argument order and error bytes stay locked.

## `Load` validation and error precedence

`Load` performs these steps in exact order:

1. nil or typed-nil `context.Context` -> `query_error/invalid_plan`
2. already canceled/deadline context -> original `ctx.Err()`
3. nil or typed-nil `db.Queryer` -> `backend_error/invalid_plan`
4. zero, corrupt or unbound `ReversePrefetch` handle -> `query_error/invalid_plan`
5. clone every owner in caller order, then inspect primary keys in caller order:
   - first absent PK -> `query_error/missing_primary_key`, `Field=<owner primary-key field>`
   - present NULL or non-integer PK -> `query_error/invalid_plan`
6. sort/dedupe distinct integer keys; 999 is inclusive, more than 999 ->
   `argument_error/invalid_value` before I/O
7. issue exactly one source batch query
8. propagate scan, `Rows.Err`, `Rows.Close` and context/backend causes using existing resource precedence
9. validate every decoded row and group it, checking `ctx.Err()` during and after grouping
10. recheck `ctx.Err()` immediately before publication, then publish all ready sets only after the complete batch and
    every validation succeeds

Empty owners are the only shortened path: steps 1-4 still run, then return a non-nil empty slice and perform I/O 0.
Owner cloning/PK inspection and cap calculation are skipped because there are no owners. A generic `db.Queryer` cannot
probe a closed backend without I/O, so closed-backend liveness is not part of empty behavior.

Duplicate owner primary keys produce one distinct batch key but a separate `RelatedSet` pointer/evaluation state for each
input position. Return order is exact caller order. Owner snapshots and source values do not retain caller-visible aliases.

## Batch scan, membership and atomic publication

The batch query selects the source model with source FK `IN` sorted distinct keys and source primary key ascending. It
contains no relation path or JOIN. Every returned source is decoded by the existing sealed source descriptor and cloned.
Grouping then obtains the physical source FK via the `RelationStorage[Source]` bound at `BindReversePrefetch` time.

Exact grouping taxonomy is:

- `RelationStorage.Value(decoded)` returns `false` -> `query_error/invalid_plan`
- returned FK value is NULL or non-integer -> `integrity_error/related_set_membership`,
  `Field=<source ForeignKey field>`
- returned integer FK is outside the requested distinct set -> `integrity_error/related_set_membership`,
  `Field=<source ForeignKey field>`

The additive stable code is:

```go
const CodeRelatedSetMembership = "related_set_membership"
```

`Detail` remains diagnostic and is not a compatibility contract. These cases do not reuse
`related_object_cardinality` or `invalid_plan` integrity taxonomy. Any query/scan/rows/close/context/membership failure
returns nil output, closes acquired rows exactly once and publishes no ready state. A later independent `Load` can retry.
Cancellation that arrives after row evaluation but during/after grouping or at the publication boundary therefore cannot
publish warm caches.

On success, every returned set retains a cold source-FK exact plan plus source-PK ascending order and a private ready
evaluation state populated with only its cloned group. `All` with a live context uses that cache with I/O 0 and returns
fresh clones. Nil/already-canceled context validation wins over a warm cache. `Fresh` and `OrderBy` create cold independent
states and therefore execute their normal source query; they do not inherit the prefetched cache. The warm set retains
the backend exactly like existing `RelatedSet`: warm `All` can read after backend close, while `Fresh`/`OrderBy` observe
the backend error. There is no chunking or partial-success publication.

## Project-only generated companion

Existing `GenerateProjectRelationReverse`, version string, golden and checked-in nine-file relation product are byte
locked. A separate generator is additive:

```go
const ProjectRelationPrefetchGeneratorVersion = "godj-codegen-rel-prefetch-project-v1"

func GenerateProjectRelationPrefetch(
	packageName string,
	packages []RelationReversePackage,
) ([]byte, error)
```

It consumes the same canonical project relation package input but emits a separate companion with exact version constant
`GoDjProjectRelationPrefetchGeneratorVersion`. For the fixture the generated surface is:

```go
type AuthorsAuthorReversePrefetches struct { /* private objects/posts/reviewedPosts */ }

func (p AuthorsAuthorReversePrefetches) Posts(
	ctx context.Context,
	backend db.Queryer,
	owners []authors.Author,
) ([]*AuthorsAuthorReverseObject, error)

func (p AuthorsAuthorReversePrefetches) ReviewedPosts(
	ctx context.Context,
	backend db.Queryer,
	owners []authors.Author,
) ([]*AuthorsAuthorReverseObject, error)

type ReversePrefetches struct {
	AuthorsAuthor AuthorsAuthorReversePrefetches
}

func BindReversePrefetches() (ReversePrefetches, error)
```

`BindReversePrefetches` composes the existing `BindReverseObjects` capability with private `BindReversePrefetch` handles.
It emits bundles only for the same presence-aware object-capable owner/reverse set already emitted by `ReverseObjects`;
query-only/v3 reverse owners are omitted rather than emitted as guaranteed bind failures. Direct runtime prefetch likewise
requires an already successfully bound `ReverseObject`.
Each public method first deep-clones the entire owner slice with the concrete generated descriptor into a private snapshot
slice; this pure step cannot fail or perform I/O. It then invokes only the corresponding runtime `Load` once on those
snapshots **before** constructing wrappers, so exact context/backend/handle/owner-PK precedence is preserved. After
successful batch load it calls the existing factory `From` on the same snapshots, replaces only the selected private
related set in each wrapper, and returns wrappers only after every defensive construction/replacement step succeeds. It
never reads raw owners again after `Load`. After the private snapshot completes, subsequent caller-visible alias mutation
cannot change wrapper Model/PK and warm-set matching; the caller must synchronize any mutation concurrent with the call.
The other reverse set stays cold. Any failure returns nil and no wrapper was publicly observable; even the defensive
post-batch wrapper failure cannot partially publish output. Duplicate inputs receive independent wrappers/ready sets.

Generation must be canonical under package permutation and `gofmt` valid. It reuses the canonical reverse alias
reservations `db`, `orm`, `query`, `ir`, `bool`, `error`, `false`, `nil`, `true`, `init` and every Go keyword, including
their already locked runtime import paths. It additionally reserves exact alias/import path `context` and the indexed-slice
template builtins `make`/`len`. It seeds every immutable binding/query/object/reverse prerequisite declaration, including
their version constants, aggregates, bind functions and derived types. Each per-owner bundle reserves private field
`objects`; every reverse selector's lower-first private name such as `posts`/`reviewedPosts` must be unique and must not
collide with `objects` or another fixed/derived field. Adversarial aliases and selector collisions fail with nil bytes.
Input normalization, namespace, unsupported-shape and render/gofmt failures are generator-owned and return nil bytes. A pure generator cannot inspect the
caller's checked-in reverse companion or its version, so missing/ABI-incompatible `BindReverseObjects` prerequisite is a
caller-owned ten-file union compile/publication failure. Publication preserves last-good output on either generator or
union-compile failure. The new external product union contains the existing exact nine generated files plus one new
prefetch companion only; no same-surface version-constant check is invented.

## Product, false-green and CI gates

REL-012 changes from `oracle_locked` to `passing` only when all of these are true:

- oracle-blind cross-runtime payload reproduces exact result, DB state, statement kinds/two SELECT split, source FK column,
  key count 3, JOIN 0 and related-access extra 0
- internal compiler/product gates separately prove exact sorted bound args `[1,2,3]`, source-PK order and mutation-free
  trace before successful actual publication; they do not add protocol fields
- reverting only REL-012 manifest status restores current 10,812-byte manifest and SHA-256
  `640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136`
- Django oracle/static JSON and SHA lock remain byte-identical
- current exact nine relation-product generated files and old reverse generator/golden remain byte-identical
- aggregate becomes only `116 passing + 5 deviation + 6 oracle_locked`, relation 6/12; REL-002/007..011 remain ordered
  payload-free `not_implemented`

Required focused runtime/compiler gates include:

- IN clone/equality/comparability, permutation, empty/NULL/wrong-kind and scalar misuse
- Load empty, duplicate, input permutation, explicit zero-but-present PK, first missing/invalid PK, nil/typed-nil operands
- 999 distinct key success and 1000 pre-I/O failure with exact sorted/deduped bound arguments
- scan/Rows.Err/Close/backend/context failures, close exactly once, nil output/no partial warm state and retry
- source storage false, NULL/non-integer/unrequested membership exact categories/code/Field
- warm clone isolation, concurrent `All` I/O 0, nil/canceled context precedence, `Fresh`/`OrderBy` cold behavior
- deterministic generator/package permutation/gofmt, exact alias/import-path/prerequisite declaration/private-field
  adversaries, plus caller-owned missing-prerequisite/ABI union-compile failure and last-good preservation
- exact ten-file external compile and current nine-file byte/hash lock

Local work is lane-bounded: each implementation lane runs focused normal tests; integration owner runs one root full normal
gate and independent API/runtime/codegen/final audits. Heavy race/CGO-disabled/vet/actual Ubuntu Linux/386, exact Darwin,
four Python compatibility legs and four relation-product platform coordinates remain hosted CI responsibilities. The
workflow stays exact 26 jobs; adding the new product package expands commands/inventories inside existing jobs, not job
count. Inventory run/pass/skip count, byte length and SHA pins are remeasured once from final generated bytes.

## 명시적 비목표와 frozen paths

This packet does not implement:

- public QuerySet `Prefetch`, Django path-string `prefetch_related`, custom `Prefetch` object or nested eager graph
- public `__in`/typed `.In`, arbitrary related filter/order warm consumption, chunked batches or public cache injection
- cross-call singleflight, background goroutine, request scheduler, transaction/session sharing or write invalidation
- REL-009..011 forward eager projection/LEFT JOIN/reverse eager rejection behavior
- REL-002/007/008 write/delete, schema/IR/DDL/migration codec or non-PK `to_field`
- PostgreSQL/MySQL/other backend compiler or multi-DB/router behavior

Frozen product paths include `query/value.go`, `orm/manager.go`, `orm/reverse_object.go`,
`codegen/project_relation_reverse.go`, its tests/golden, `conformance/relationreverseproduct/**`, relation
oracle/static/SHA, schema/IR/migrations, `go.mod`, `go.sum`, REL-002/007..011 and every non-SQLite backend. Any need to
change these paths stops implementation and requires work/ADR re-audit instead of silent scope widening.
Within the allowed Django test file, only the hard-coded product manifest-status expectation may change from final
REL-012 locked to passing; scenario/reference behavior and every other assertion remain byte/semantic locked.

## 완료 조건

- [x] exact clean baseline/EVID-050 and current-vs-target classification are documented without evidence reuse.
- [x] work/ADR activation freezes exact public API, allowed paths, taxonomy, atomicity, codegen and CI ownership.
- [ ] activation documentation diff passes independent API/scope audits and its own exact-head hosted 26-job CI.
- [ ] runtime/query/SQLite/codegen implementation stays inside allowed paths and focused lane tests pass.
- [ ] REL-012 oracle-blind actual and false-green/revert/locked-byte gates pass.
- [ ] root full normal integration and independent runtime/codegen/final audits pass.
- [ ] exact implementation-head hosted 26-job matrix and inventories pass.
- [ ] completion docs/ADR status transition and separate completion-documentation hosted evidence are synchronized.

## 진행 기록

- [x] 2026-08-11: Clean terminal baseline `e9dc361f...`, run `31424055711` exact 26/26·326/326 and audit
  P0/P1/P2/P3=`0/0/0/0` were recorded as EVID-050 baseline-only proof.
- [x] 2026-08-11: Independent feasibility/API audit froze separate `ReversePrefetch`, immutable IN representation,
  max-999 one-batch policy, exact validation precedence and `related_set_membership` taxonomy with no blocker.
- [x] 2026-08-11: Project-only prefetch companion, exact ten-file union, REL-012-only manifest transition and hosted/local
  gate ownership were frozen.
- [ ] activation diff audit/commit/push/hosted exact-head evidence
- [ ] runtime/query/SQLite/codegen/conformance implementation and integration audit
- [ ] implementation hosted evidence
- [ ] completion documentation and terminal evidence

## 현재 blocker와 다음 작업

External/API blocker는 없습니다. Activation diff exact-head CI는 pending이고 EVID-050을 그 proof로 재사용하지
않습니다.

1. Integration owner가 exact 15 activation document diff, links/frontmatter/fences, current/target wording and
   allowed-path/frozen-path consistency를 검증합니다.
2. Independent API/scope audit P0/P1/P2/P3=0 뒤 activation-only commit을 push하고 existing exact 26-job matrix를
   비동기로 시작합니다.
3. Activation CI와 병렬로 disjoint query/runtime/codegen/product lanes를 구현하되 each lane은 focused normal tests만
   local에서 실행합니다. Shared public API/status files는 integration owner만 수정합니다.
4. Final integration은 all lanes를 재감사하고 one full normal gate 뒤 exact implementation-head hosted matrix를
   시작합니다. Draft PR #1은 사용자 요청 전 merge하지 않습니다.

## 인수인계

- Current product는 exact `115 + 5 + 7`, relation 5/12입니다. Target `116 + 5 + 6`, relation 6/12를 현재
  support로 표현하지 않습니다.
- EVID-050/run `31424055711`은 exact clean baseline만 증명합니다. Activation/implementation evidence는 별도
  exact head가 필요합니다.
- Existing reverse/object API와 exact nine generated files, oracle/static/SHA and frozen paths를 보존합니다.
- Public API 또는 taxonomy 변경이 필요하면 implementation을 멈추고 Proposed ADR-0028/work를 먼저 갱신해
  independent audit을 다시 받습니다.
- Draft PR #1은 open/draft로 유지하고 사용자 명시적 요청 없이 merge하지 않습니다.
