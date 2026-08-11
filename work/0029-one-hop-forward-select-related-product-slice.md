---
id: GDJ-0029
status: active
updated: 2026-08-11
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "5c0efef12560203d720e4c2dd7bda50c0324a228"
depends_on: ["GDJ-0028"]
contracts: ["REL-009", "REL-010", "REL-011", "Q-013", "Q-017"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "query/error.go"
  - "query/plan.go"
  - "query/plan_test.go"
  - "query/relation_projection.go"
  - "query/relation_projection_test.go"
  - "orm/select_related.go"
  - "orm/select_related_test.go"
  - "orm/select_related_external_test.go"
  - "db/sqlite/compiler.go"
  - "db/sqlite/compiler_test.go"
  - "db/sqlite/integration_test.go"
  - "codegen/relation_projection.go"
  - "codegen/relation_projection_test.go"
  - "codegen/project_relation_select_related.go"
  - "codegen/project_relation_select_related_test.go"
  - "codegen/testdata/relation_projection/**"
  - "codegen/testdata/relation_select_related/**"
  - "conformance/README.md"
  - "conformance/contracts/relation-manifest.json"
  - "conformance/relationselectproduct/**"
  - "conformance/runners/godj/relation_scenarios.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/runners/django/tests/test_relation_scenarios.py"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/internal/protocol/product_compare_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_select_related/**"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0029-one-hop-forward-select-related.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0028-reverse-foreign-key-prefetch-product-slice.md"
  - "work/0029-one-hop-forward-select-related-product-slice.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# One-hop Forward `select_related` Product Slice

## 결과 목표

GDJ-0029는 locked Django 6.1 relation contracts REL-009/010/011을 하나의 bounded SQLite product slice로
구현합니다. Required forward relation은 one-query `INNER JOIN`, nullable forward relation은 one-query
`LEFT OUTER JOIN`으로 source와 target을 함께 decode하고, multi-valued reverse path는 compiler/DB I/O 전에
`field_error/invalid_related_path`로 거부합니다.

1. Plain Post load 뒤 `author` 접근은 total SELECT 4이고 결과는
   `[(10,Ada),(11,Ada),(12,Cleo)]`입니다. Required eager load는 같은 결과, SELECT 1, INNER JOIN 1,
   access-extra 0입니다.
2. Nullable `reviewer` eager load는 `[(10,Bob),(11,NULL),(12,Bob)]`, SELECT 1,
   LEFT OUTER JOIN 1, INNER JOIN 0, access-extra 0입니다.
3. `Author.select_related("posts")`에 대응하는 generated dynamic path validation은
   `field_error/invalid_related_path`, query/mutation 0, unchanged DB입니다.
4. 모든 source/target row와 resource/context를 검증한 뒤에만 ready related objects를 포함한 결과를
   원자적으로 공개합니다.

현재 hosted-accepted 제품은 exact 12 checked-in manifests/127 contracts =
`116 passing + 5 deviation + 6 oracle_locked`, relation actual REL-001/003/004/005/006/012 6/12입니다.
Pre-hosted local implementation은 oracle-blind actual까지 exact
`119 passing + 5 deviation + 3 oracle_locked`, relation actual 9/12이고 completion에는 implementation
exact-head hosted acceptance가 아직 필요합니다.

## 기준 상태와 선행 증거

- Baseline은 clean
  `codex/revision-fenced-migration-lifecycle@5c0efef12560203d720e4c2dd7bda50c0324a228`입니다.
- [EVID-20260811-054](../docs/status/TEST_EVIDENCE.md#evid-20260811-054--gdj-0028-terminal-exact-head-ci-and-gdj-0029-activation-baseline)의
  [run 31436881856](https://github.com/progresshans/godj/actions/runs/31436881856)은 이 exact terminal baseline만
  검증합니다. 이 activation diff, Proposed API, REL-009/010/011 implementation 또는 target aggregate의
  증거로 재사용하지 않습니다.
- [ADR-0023](../docs/adr/0023-symbolic-relation-binding-and-shared-relation-ast.md),
  [ADR-0026](../docs/adr/0026-forward-foreign-key-object-cache-and-nullability.md),
  [ADR-0028](../docs/adr/0028-reverse-foreign-key-prefetch.md)의 immutable project binding, sealed descriptor,
  QuerySet cache/resource precedence와 generated project-only composition을 보존합니다.
- Activation commit `0a1da373a443527e48a154ca6ccc7284e5e80dc0`의
  [run 31465198903](https://github.com/progresshans/godj/actions/runs/31465198903)은 exact 26/26 jobs·326/326
  recorded steps와 hosted audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. 이 run은 activation/baseline만
  증명하며 아래 local implementation proof로 재사용하지 않습니다.

## Locked REL-009/010/011 외부 동작

Fixture는 existing authors `(1,Ada),(2,Bob),(3,Cleo)`와 posts
`(10,Alpha,author=1,reviewer=2)`, `(11,Beta,author=1,reviewer=NULL)`,
`(12,Gamma,author=3,reviewer=2)`를 그대로 사용합니다.

- REL-009 result는 `plain`과 `eager` 모두 `[(10,Ada),(11,Ada),(12,Cleo)]`입니다. Plain metrics는
  statement kinds four SELECT, query count 4, JOIN 0, access-extra 3입니다. Eager metrics는 one SELECT,
  one INNER JOIN, LEFT OUTER 0, access-extra 0입니다.
- REL-010 result rows는 `[(10,Bob),(11,NULL),(12,Bob)]`입니다. Metrics는 one SELECT,
  one LEFT OUTER JOIN, INNER 0, access-extra 0입니다.
- REL-011 result는 nil, error exact `field_error/invalid_related_path`, query count 0, statement/JOIN kinds empty,
  mutation count 0, unchanged DB입니다. Error message는 비계약입니다.

Raw SQL string, alias spelling, joined projection labels와 Django 내부 related-object cache identity는 protocol
contract가 아닙니다. Exact result/DB state/query and JOIN metrics가 cross-runtime payload이고, projection order,
source/target key equality, resource close, mutation-free trace는 Go internal false-green gate입니다.

## Immutable one-hop projection AST

Exact additive query surface는 다음입니다.

```go
type RelationProjection struct { /* private one-hop state and ordered target columns */ }

func NewForwardRelationProjection(
	source ir.ModelIdentity,
	sourceTable string,
	sourceKey FieldRef,
	target ir.ModelIdentity,
	targetTable string,
	targetKey FieldRef,
	orderedTargetColumns []FieldRef,
) (RelationProjection, error)

func (p RelationProjection) Hop() RelationHop
func (p RelationProjection) TargetColumns() []FieldRef
func (p RelationProjection) Equal(other RelationProjection) bool

func (p Plan) RelationProjection() (RelationProjection, bool)
func (p Plan) WithRelationProjection(projection RelationProjection) (Plan, error)
```

- Selection은 exactly one forward many-to-one hop과 target descriptor field order 그대로의 projection만
  표현합니다. Reverse constructor, multi-hop, multiple selection list와 caller-selected JOIN kind는 없습니다.
- Constructor는 canonical source/target identity/table/field/column, nonempty ordered target fields와 exactly one
  non-null integer target PK column을 검증합니다. Input slices와 returned accessors는 clone되고 Plan clone/Equal은
  selection을 deep-copy/compare합니다.
- `nullable=false`는 eager context에서 INNER, `nullable=true`는 LEFT OUTER입니다. Predicate context의 existing
  INNER/reverse join과 nullable source-key JOIN trim 의미는 바꾸지 않습니다.
- `WithRelationProjection`이 없는 모든 existing Plan/SQL bytes와 behavior는 locked입니다. A plan that already has
  a projection cannot be overwritten or extended: a second call returns zero Plan plus `query_error/invalid_plan`.
  SQLite는 forged/zero, non-forward, root mismatch, duplicate/corrupt target projection을 같은 code로 pre-I/O
  거부합니다.

## Sealed joined descriptor와 runtime API

Exact additive ORM surface는 다음입니다.

```go
type ProjectionDescriptor[M any] interface {
	ModelDescriptor[M]
	NewProjectionScan() ProjectionScan[M]
}

type ProjectionScan[M any] interface {
	Destinations() []any
	Decode() (model M, primaryKey query.Value, presence ProjectionPresence)
}

type ProjectionPresence uint8

const (
	ProjectionInvalid ProjectionPresence = iota
	ProjectionAbsent  ProjectionPresence = 1
	ProjectionPresent ProjectionPresence = 2
)

type ForwardSelectPath[S any] struct { /* private resolved path */ }

func ResolveForwardSelectPath[S any](
	source BoundModel[S],
	path string,
) (ForwardSelectPath[S], error)

type ForwardSelect[S, T any] struct { /* private sealed state */ }

func BindRequiredForwardSelect[S, T any](
	path ForwardSelectPath[S],
	relation RequiredForwardObject[S, T],
) (ForwardSelect[S, T], error)

func BindNullableForwardSelect[S, T any](
	path ForwardSelectPath[S],
	relation NullableForwardObject[S, T],
) (ForwardSelect[S, T], error)

func (s ForwardSelect[S, T]) Select(source QuerySet[S]) ForwardSelectQuery[S, T]

type ForwardSelectQuery[S, T any] struct { /* private All-only state */ }

func (q ForwardSelectQuery[S, T]) Plan() query.Plan
func (q ForwardSelectQuery[S, T]) Backend() db.Queryer
func (q ForwardSelectQuery[S, T]) All(context.Context) ([]*ForwardSelected[S, T], error)

type ForwardSelected[S, T any] struct { /* private pointer-owned snapshot */ }

func (s *ForwardSelected[S, T]) Source() (S, error)
func (s *ForwardSelected[S, T]) Related() (*RelatedObject[T], error)
```

`ProjectionDescriptor` is an additive capability implemented by a separate generated file in each app package; existing
named descriptor and root-only `ModelDescriptor.Scan` bytes remain unchanged. `NewProjectionScan` returns fresh private
state. Runtime concatenates source then target `Destinations` and calls `db.Row.Scan` exactly once. `Decode` restores
private primary-key presence and nullable values; target AutoField PK is the outer-join presence sentinel. Decode also
returns the projected primary-key `query.Value`, so runtime can validate source FK/target PK without reflection or a v3
`PrimaryKeyObjectDescriptor`. Absent returns `query.Null()`, Present a non-NULL integer key, and Invalid zero/null. Scan
state is never reused across rows or goroutines.

`ResolveForwardSelectPath` is the sole typed/dynamic resolver. It accepts one direct forward many-to-one name, carries
nullability and creates the canonical projection; reverse/unknown/blank/multi-hop fails pre-I/O. The required/nullable
binders derive the source/target bindings, storage and nullability from the already-sealed forward object handle owned by
the existing `BindObjects` snapshot. They match the path, target identity and exact immutable source/target projection
descriptors without calling project `Bind` again or reconstructing a target binding. `ForwardSelect.Select` consumes an
existing QuerySet so its Filter/OrderBy/Limit/Fresh plan is preserved without changing Manager/QuerySet APIs. Before any
backend I/O it verifies the bound source descriptor dynamic type/metadata, root table and exact root columns against the
path snapshot. A mismatch/corrupt QuerySet is stored as `query_error/invalid_plan` in the returned eager query and wins at
`All` under the configuration-error precedence.
The distinct `ForwardSelectQuery` deliberately exposes only Plan, Backend and All. It owns a separate successful cache;
Count/Exists/At/First/Iterate and a second filter/order surface are not added.

`All` returns non-nil empty on a successful empty query. It scans and validates the complete rowset, Rows.Err, Close and
context before publishing a ready cache. Every caller receives fresh source/target clones and distinct
`ForwardSelected`/`RelatedObject` pointer state. `Source` returns a clone. `Related` returns an already-ready object;
its live-context `Get` performs I/O 0, while nil/canceled context still wins. Required absence is impossible after a
successful load; nullable absence returns the normal absent object. `Fresh` on that object is the existing cold
source-FK exact behavior.

## Validation, atomicity and taxonomy

Terminal precedence is exact:

1. nil/typed-nil context -> `query_error/invalid_plan`
2. existing `ctx.Err()` -> original cancellation/deadline error
3. stored configuration error from Filter/OrderBy/Limit -> that error
4. nil/typed-nil backend -> `backend_error/invalid_plan`
5. zero/corrupt descriptor, relation, plan or evaluation state -> `query_error/invalid_plan`
6. backend query, joined scan, Rows.Err and Rows.Close causes under existing resource precedence
7. row membership/presence validation and final context recheck
8. success-only cache publication

Exact additive stable codes are:

```go
const CodeInvalidRelatedPath = "invalid_related_path"
const CodeRelatedObjectProjection = "related_object_projection"
```

- A generated dynamic direct path that is unknown, blank, multi-hop or reverse/multi-valued returns
  `field_error/invalid_related_path`, `Field=<input path>`, before compiler/backend I/O.
- Joined descriptor/source metadata mismatch, storage `Value(source)==false`, malformed AST and zero/corrupt state return
  `query_error/invalid_plan`.
- Required target absence; nullable FK/target presence disagreement; target PK absent, non-integer or unequal to the
  decoded source FK return `integrity_error/related_object_projection`, `Field=<source FK field>`.
- Driver scan errors, Rows.Err, Close and context causes remain wrapped/preserved rather than relabeled. `Detail` is
  diagnostic only.

Any failure returns nil, closes acquired rows exactly once, publishes no cache and allows an independent retry.
Cancellation is rechecked during scan, after resource close and immediately before publication. No partially ready
source/target pair is observable.

## SQLite compiler boundary

SQLite routes a Plan with `RelationProjection` through the relation compiler. It emits root columns followed by selected
target columns, and exactly one deterministic qualified forward JOIN. Required selection is `INNER JOIN`; nullable
selection is `LEFT OUTER JOIN`. Existing relation predicate on the same required edge reuses one join. Root Filter,
OrderBy and Limit remain qualified against the root alias and retain argument/order meaning.

The compiler does not infer eager behavior from a dummy WHERE condition, does not append a second query and does not let
the generated scanner guess column order. Relation selection with an unrelated/reverse/multiple hop, inconsistent edge,
missing target PK projection or unsupported target field fails before backend I/O.

## Additive app projection and project selection generators

Existing scalar/relation-query/object/reverse/prefetch generators, versions, goldens and every checked-in generated
product byte remain locked. Exact additive generators are:

```go
const RelationProjectionGeneratorVersion = "godj-codegen-rel-projection-v1"

func GenerateRelationProjection(
	packageName string,
	schema ir.Schema,
) ([]byte, error)

const ProjectRelationSelectRelatedGeneratorVersion = "godj-codegen-rel-select-related-project-v1"

func GenerateProjectRelationSelectRelated(
	packageName string,
	packages []RelationObjectPackage,
) ([]byte, error)
```

The first emits one app-local `zz_godj_relation_projection.go` companion that adds
`ProjectionDescriptor.NewProjectionScan` methods to existing named descriptors without changing their original file.
The second emits only project `zz_godj_relation_select_related.go`. It leaves the original object companion,
`Objects` and `BindObjects` bytes unchanged and adds a same-package method to the existing factory. Bounded fixture
surface is:

```go
type BlogPostSelectRelated struct { /* private immutable source */ }

func (f BlogPostObjectFactory) SelectRelated(
	source orm.QuerySet[blog.Post],
) BlogPostSelectRelated

func (s BlogPostSelectRelated) Author() BlogPostAuthorSelectRelatedQuery
func (s BlogPostSelectRelated) Reviewer() BlogPostReviewerSelectRelatedQuery
func (s BlogPostSelectRelated) ParseDynamic(path string) (BlogPostDynamicSelectRelatedQuery, error)

objects, err := project.BindObjects()
selected := objects.BlogPost.SelectRelated(sourceQuery)

// Typed execution over an existing QuerySet.
posts, err := selected.Author().All(ctx)
posts, err = selected.Reviewer().All(ctx)

// Dynamic executable dispatch through the same resolver.
dynamic, err := selected.ParseDynamic("author")
posts, err = dynamic.All(ctx)
```

`SelectRelated` returns an immutable singular builder. Its typed `Author()` and `Reviewer()` selectors use opaque
relation-specific All-only query types because different forward fields may have different target Go types. Each retains
the original QuerySet and converts `ForwardSelected` values into existing `BlogPostObject` wrappers only after runtime
`All` succeeds. It calls the existing factory on each cloned source, replaces only the selected private `author` or
`reviewer` with its ready object, and publishes no wrapper until all defensive assembly succeeds. The nonselected
relation remains cold. The singular AST does not promise `.Author().Reviewer()` composition.

`ParseDynamic` accepts exactly one case-sensitive direct forward many-to-one field and dispatches valid
`author`/`reviewer` through the same resolver and sealed handles. Reverse `posts`/`reviewed_posts`, unknown, blank and
`__` paths return `invalid_related_path` directly from parse, before query assembly or I/O. `Objects` does not gain a
dummy AuthorsAuthor factory merely to expose a negative call. REL-011 instead creates one separately valid project
binding, binds Author in that negative-only fixture and exercises `ResolveForwardSelectPath(..., "posts")` before
compiler/DB I/O. It never mixes that binding with eager object state or publishes a wrapper. A public cross-model target
union, nested path and no-argument auto-follow remain non-goals.

This spelling is a bounded low-level GDJ-0029 bridge, not the canonical model-centered application API or an API freeze.
The Proposed `project.Using(backend)` facade, relation-aware forward/reverse chaining, scalar access and FK mutation/cache
policy remain Q-013/Q-017 work. A later facade may delegate to this engine without changing the locked contract behavior.

Projection companions publish `GoDjRelationProjectionGeneratorVersion` and
`GoDjRelationProjectionSchemaSHA256`. Their per-row scans use nullable SQL holders for every projected field. Presence
zero is invalid, all projected fields NULL is absent, PK plus every non-null field present is present, and any partial
shape is invalid. Because the companion is in the app package, scalar-v2 decoding can restore the descriptor's private
primary-key-presence bit without reflection.

The generator clones/normalizes input, is permutation-deterministic and gofmt-valid, and returns nil bytes on any
input/namespace/render error. It reserves existing binding/query/object/reverse/prefetch declarations and exact import
aliases/paths, plus factory method `SelectRelated`, builder method `ParseDynamic`, generated relation selector names,
`context`, `sql`/`database/sql`, `db`, `orm`, `query`, `ir` and every used keyword/predeclared name. `SelectRelated` also
cannot collide with a schema relation selector. Adversarial type/selector/lower-first/private-field collisions fail.
Missing or ABI-incompatible `BlogPostObjectFactory` or other prerequisite companions are caller-owned exact twelve-file
union compile/publication failures; last-good output remains unchanged. The pure generator does not inspect checked-in
files.

## Product and false-green gates

REL-009/010/011 move together from `oracle_locked` to `passing` only after all gates pass:

- oracle-blind exact REL-009 plain/eager results, 4-vs-1 SELECT, INNER 1 and access-extra 3-vs-0
- oracle-blind exact REL-010 rows including middle NULL, one SELECT, LEFT OUTER 1, INNER 0, access-extra 0
- oracle-blind REL-011 exact error category/code, SQL/mutation 0 and unchanged DB
- typed required/nullable and valid dynamic execution converge on identical immutable `query.RelationProjection`; reverse
  rejection uses the same resolver kernel on a separately valid negative-only project binding rather than a contract-ID
  error stub
- compiler asserts exact root-then-target projection order, one reused join, root filter/order/limit preservation and no
  dummy predicate/second query
- joined descriptor required/nullable/partial-NULL/mismatched-key cases, source/target alias cloning and exact taxonomy
- nil/typed-nil/canceled/config/backend/handle precedence; scan/Rows.Err/Close/context failure, close once, nil/no partial,
  retry and concurrent shared-cache behavior
- warm related access I/O 0, `Fresh` cold, repeated/copy All clone and pointer-state isolation
- exact twelve-file candidate compile (existing nine prerequisite files + two app projection companions + one project
  select companion), generator permutation/namespace/adversarial/last-good tests and byte locks for all existing files
- manifest changes only REL-009/010/011 to exact target 10,788 bytes/SHA-256
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141`; reverting those three statuses restores
  current 10,806 bytes/SHA-256 `70fefee1b2e4bb72b7a84ff07e4d9737ee59d3056ca52641668a5915b29da477`
- oracle/static JSON and SHA, protocol shape and Django scenario behavior remain byte-identical

The Django test file may change only its hard-coded manifest-status expectation. Product aggregate target is exactly
`119 + 5 + 3`, relation 9/12; REL-002/007/008 remain ordered payload-free `not_implemented`.

Focused lane tests are local. Integration owner runs one root normal gate and independent API/runtime/codegen/final
audits. Heavy race/CGO0/vet, actual Ubuntu Linux/386, exact Darwin/Python and four product coordinates remain in the
existing exact 26-job hosted topology. Inventory run/pass/skip, bytes and SHA are remeasured from final bytes; no value is
predicted in activation docs.

## 명시적 비목표와 frozen paths

This packet does not add multiple/nested select-related fields, canonical project facade, relation-aware forward/reverse
chaining, reverse eager execution, a public cross-model target union, no-argument traversal, OneToOne, non-AutoField
target, annotation/aggregation/distinct, selected Count/Exists/At/Iterate, public cache injection, cross-call singleflight,
write invalidation, transaction sharing or non-SQLite support.

Frozen unless a new work/ADR explicitly reopens them:

- `query/value.go`, mutation AST, `orm/manager.go`, `orm/descriptor.go`, existing relation object/reverse/prefetch files
- existing code generators, versions, goldens and checked-in ten-file relation product union
- Schema DSL/IR format/hash, migration state/codec/execution and `go.mod`/`go.sum`
- Django relation scenario implementation, oracle/static JSON, SHA locks and protocol payload shape
- REL-002/007/008 and REL-012 behavior/status; write/delete/DDL/migration and every non-SQLite backend

## 체크리스트

- [x] GDJ-0029 active work and ADR-0029 Proposed scope created from exact tested baseline.
- [x] REL-009/010/011 indivisible boundary, bounded projection/runtime/generated building-block API, taxonomy and
      false-green gates frozen; canonical application facade remains Q-013/Q-017.
- [x] Activation documentation exact-head 26-job CI succeeds.
- [x] Query/ORM/compiler implementation and focused tests complete within allowed paths.
- [x] Pure generators, exact twelve-file union and old-byte/last-good tests pass.
- [x] Oracle-blind REL-009/010/011 actual and three-status-only manifest transition pass.
- [x] Root normal integration and independent audits report P0/P1/P2/P3=0.
- [ ] Implementation exact-head hosted 26-job acceptance passes before completion claims.
- [ ] Completion docs and later terminal evidence use separate heads and do not recursively reuse runs.

## 진행 기록

- 2026-08-11: GDJ-0028 terminal head `5c0efef12560203d720e4c2dd7bda50c0324a228`을 baseline으로
  REL-009/010/011 통합 one-hop forward eager slice를 활성화했습니다.
- 2026-08-11: Baseline run `31436881856`은 report-only EVID-054로 분리하고 later activation/implementation
  evidence로 재사용하지 않도록 고정했습니다.
- 2026-08-11: Required/nullable projection과 reverse rejection은 nullable scanner/LEFT JOIN을 뒤로 미루거나
  negative-only stub을 허용하지 않도록 indivisible packet으로 결정했습니다.
- 2026-08-11: Local Django 6.1 tag의 manager/ForeignKey descriptor/cache/select-related/reverse-manager 의미와
  current GoDj object binding을 대조했습니다. Separate `BindSelectRelated` aggregate는 제거하고 existing
  `BindObjects` snapshot을 재사용하는 bounded factory companion으로 좁혔으며, canonical
  `project.Using(backend)` relation facade와 chaining/FK mutation policy는 Q-013/Q-017에 남겼습니다.
- 2026-08-11: Activation commit `0a1da373a443527e48a154ca6ccc7284e5e80dc0`은 run `31465198903`의 exact
  26/26 jobs·326/326 recorded steps와 hosted audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- 2026-08-11: Uncommitted exact 49-entry implementation은 root `make ci`, local exact 630/630/0·63,928 bytes·
  SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`, target
  `119 passing + 5 deviation + 3 oracle_locked`, relation 9/12와 runtime/codegen/integration/remediation audit
  P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- 2026-08-11: Independent pre-commit audit가 same-edge terminal source-key/projection metadata 불일치를 허용하는
  P1 forged-plan gap을 발견했습니다. Source identity/target identity/target table/target PK 단독 mutation으로
  재현하고 pre-I/O provenance equality를 강제하는 최소 수정 뒤 정상 same-hop과 unrelated root relation filter를
  보존했으며, integration/remediation re-audit는 clean P0/P1/P2/P3=`0/0/0/0`입니다.

## 현재 blocker와 다음 정확한 작업

외부 blocker는 없습니다. Local implementation과 five-document pre-hosted sync를 의도적으로 commit/push한 뒤
그 exact implementation head에서 별도 26-job hosted acceptance를 실행하는 것이 다음 작업입니다. Activation
EVID-055/run `31465198903`은 implementation proof가 아니며 completion claim에는 implementation hosted evidence가
필요합니다. Draft PR은 사용자 요청 전 merge하지 않습니다.

## 인수인계

- hosted-accepted current: exact `116 passing + 5 deviation + 6 oracle_locked`, relation 6/12
- pre-hosted local implementation: exact `119 passing + 5 deviation + 3 oracle_locked`, relation 9/12;
  630/630/0·63,928 bytes·SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`
- active decision: ADR-0029 Proposed; Q-013 remains `Partial`
- implementation scope: exact paths in frontmatter; all other code/product/oracle/schema bytes frozen
- next evidence: implementation exact-head hosted CI; EVID-055 activation/local evidence와 별도 기록
