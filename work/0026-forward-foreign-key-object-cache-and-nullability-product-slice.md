---
id: GDJ-0026
status: completed
updated: 2026-08-10
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "bffc52844de87a2791959ea1e8f99c60dd13d1aa"
depends_on: ["GDJ-0025"]
contracts: ["REL-003", "REL-006", "Q-013"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "query/error.go"
  - "query/plan_test.go"
  - "query/relation.go"
  - "query/relation_test.go"
  - "orm/descriptor.go"
  - "orm/relation_query.go"
  - "orm/relation_query_test.go"
  - "orm/relation_query_external_test.go"
  - "orm/dynamic_relation.go"
  - "orm/dynamic_relation_test.go"
  - "orm/relation_object.go"
  - "orm/relation_object_test.go"
  - "orm/relation_object_external_test.go"
  - "codegen/relation_object.go"
  - "codegen/relation_object_test.go"
  - "codegen/project_relation_object.go"
  - "codegen/project_relation_object_test.go"
  - "codegen/testdata/relation_object/**"
  - "db/sqlite/compiler.go"
  - "db/sqlite/compiler_test.go"
  - "db/sqlite/integration_test.go"
  - "conformance/README.md"
  - "conformance/contracts/relation-manifest.json"
  - "conformance/relationobjectproduct/**"
  - "conformance/runners/godj/relation_scenarios.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/runners/django/tests/test_relation_scenarios.py"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/internal/protocol/product_compare_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_object/**"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0026-forward-foreign-key-object-cache-and-nullability.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0025-forward-foreign-key-predicate-product-slice.md"
  - "work/0026-forward-foreign-key-object-cache-and-nullability-product-slice.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Forward ForeignKey Object Cache and Nullability Product Slice

## 결과 목표

GDJ-0026은 locked Django 6.1 `REL-003`과 `REL-006`을 하나의 bounded product slice로 구현합니다.
Required forward object access만 먼저 만들면 nullable key/cache ABI를 잘못 닫을 수 있고, nullable
`isnull`만 scalar FK condition으로 낮추면 relation provenance를 잃은 false green이 됩니다. 따라서 다음
세 경계를 함께 통과시킵니다.

1. Actual SQLite에서 freshly loaded `Post(10)`의 required `Author`를 첫 access SELECT 1, 같은 object
   wrapper의 두 번째 access SELECT 0으로 읽습니다.
2. `Post(11).ReviewerID == nil`은 object access가 SELECT 0인 absent success입니다. Non-null reviewer도
   같은 bounded object loader로 실제 target row를 읽습니다.
3. Typed `objects.BlogPost.Reviewer.IsNull(true)`와 dynamic `reviewer__isnull=true`는 같은 nullable one-hop
   relation-aware Plan을 만들고 SQLite가 root FK `IS NULL`로 trim해 result `[11]`, SELECT 1, JOIN 0을
   반환합니다.

완료 aggregate 목표는 exact 12 adapter sets/127 contracts =
`114 passing + 5 deviation + 8 oracle_locked`, relation actual REL-001/003/004/006 4/12입니다.

## 기준 상태와 선행 조건

- Baseline은 `codex/revision-fenced-migration-lifecycle@bffc52844de87a2791959ea1e8f99c60dd13d1aa`입니다.
- Baseline은 GDJ-0025 final evidence/status commit이며 Draft PR #1
  [run 31359958949](https://github.com/progresshans/godj/actions/runs/31359958949)의 exact 26/26 jobs와
  326/326 recorded steps를 통과했습니다.
  [EVID-042](../docs/status/TEST_EVIDENCE.md#evid-20260810-042--gdj-0025-final-exact-head-ci-and-gdj-0026-activation-baseline)에
  기록합니다. 이 run은 baseline의 REL-001/004만 증명하며 이 activation diff 또는 REL-003/006 증거로
  재사용하지 않습니다.
- [ADR-0023](../docs/adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)은 object cache가 Query AST가
  아니라 evaluated model/result instance를 소유하고 `isnull`이 join을 trim할 수 있다는 방향을 Accepted했습니다.
- [ADR-0012](../docs/adr/0012-queryset-evaluation-cache-ownership.md)은 successful full-result cache,
  singleflight, waiter cancellation, failure retry와 deep-clone 의미를 Accepted/implemented했습니다.
- [ADR-0024](../docs/adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)와
  [ADR-0025](../docs/adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)의 plain v3 model,
  immutable project binding, additive query companion와 required target-field path를 보존합니다.

## Locked REL-003/006 외부 동작

Fixture는 existing authors/blog 의미를 그대로 사용합니다.

- authors: `(1, Ada)`, `(2, Bob)`, `(3, Cleo)`
- posts: `(10, Alpha, author=1, reviewer=2)`, `(11, Beta, author=1, reviewer=NULL)`,
  `(12, Gamma, author=3, reviewer=2)`

REL-003 actual은 fixture literal `Post`를 직접 wrapper에 넣지 않습니다. Generated `PostObjects`와 actual
SQLite query로 Post 10을 freshly load한 뒤 object factory로 감쌉니다. Access window는 다음과 같습니다.

- cold `Author(ctx)`: `{id:1,name:"Ada"}`, SELECT 1, JOIN 0
- same wrapper warm `Author(ctx)`: 같은 값, SELECT 0, JOIN 0
- caller가 반환된 target 또는 nullable pointer를 바꿔도 model snapshot/canonical cache는 변하지 않음

REL-006은 freshly loaded Post 11의 `Reviewer(ctx)`가 `(zero,false,nil)`, SELECT 0이어야 합니다. Typed와
dynamic `reviewer__isnull=true` construction은 I/O 0이고 Plan.Equal이어야 하며 evaluation은 Post IDs exact
`[11]`, SELECT 1, INNER/LEFT JOIN 모두 0입니다. Setup/teardown과 initial Post load는 contract metric window
밖에서 별도 계측합니다. 두 contract 모두 database rows를 바꾸지 않습니다.

## Exact runtime API와 descriptor seal

Exact surface는 [Accepted ADR-0026](../docs/adr/0026-forward-foreign-key-object-cache-and-nullability.md)에
동결했습니다. Signature/export 변경이 필요하면 후속 ADR/work를 먼저 만듭니다.

```go
type RelationObjectDescriptor[M any] interface {
	ModelDescriptor[M]
	SnapshotRelationObjectDescriptor() RelationObjectDescriptor[M]
	BindRelationStorage(ir.Field) (RelationStorage[M], bool)
}

type RelationStorage[M any] interface {
	Field() ir.Field
	Value(M) (query.Value, bool)
}
```

`BindModel`은 input descriptor 자체를 retained state에 저장하지 않습니다. Descriptor가 object capability를
제공하면 `SnapshotRelationObjectDescriptor`를 정확히 한 번 호출하고, returned snapshot이 nil/typed-nil,
metadata mismatch이거나 named non-pointer zero-size struct가 아니면 `query_error/invalid_plan`으로 거부합니다.
성공한 snapshot은 immutable/concurrent-safe contract이며 `BoundModel`의 private sealed slot만 소유합니다.
Object binder는 source와 target 모두 이 sealed capability가 있어야 합니다.

`BindRelationStorage`는 exact canonical relation `ir.Field`만 받아 generated private zero-state storage를
반환합니다. Binder는 returned `Field()`를 canonical field와 다시 deep-equal 검증하고 storage도 named
non-pointer zero-size struct로 제한합니다. Generated `Value`는 `AuthorID`/`ReviewerID`를 direct access하므로
hot path reflection이 없습니다. Caller-provided source-key callback, target `Manager`, reflection field read,
global registry는 public input이 아닙니다. Target manager와 primary-key predicate는 target sealed descriptor와
bound snapshot에서 내부 파생합니다.

## Exact object loader API와 ownership

```go
type RequiredForwardObject[S, T any] struct { /* private sealed state */ }
type NullableForwardObject[S, T any] struct { /* private sealed state */ }
type RelatedObject[T any] struct { /* private state and self sentinel */ }

func BindRequiredForwardObject[S, T any](
	source BoundModel[S], field string, target BoundModel[T],
) (RequiredForwardObject[S, T], error)

func BindNullableForwardObject[S, T any](
	source BoundModel[S], field string, target BoundModel[T],
) (NullableForwardObject[S, T], error)

func (r RequiredForwardObject[S, T]) From(db.Queryer, S) (*RelatedObject[T], error)
func (r NullableForwardObject[S, T]) From(db.Queryer, S) (*RelatedObject[T], error)
func (r NullableForwardObject[S, T]) IsNull(bool) Predicate[S]

func (r *RelatedObject[T]) Get(context.Context) (T, bool, error)
func (r *RelatedObject[T]) Fresh() (*RelatedObject[T], error)

func ParseDynamicRelationObjects[M any](
	model BoundModel[M], policy LookupPolicy, inputs []LookupInput,
) ([]Predicate[M], error)
```

Both binders validate zero/different snapshots, source/target identity, cardinality, required-versus-nullable shape,
sealed descriptors/storage and target AutoField before publishing a handle. `From` validates nil/typed-nil backend,
clones the source model and pointer fields before storage extraction, and publishes only after the exact integer/null
key and a target PK exact QuerySet with limit 2 are fully built. Required null/missing storage or nullable storage of
the wrong kind is `query_error/invalid_plan` before I/O.

`RelatedObject` always uses target PK exact + `Limit(2)` + `All`; it never uses `First`.

- 0 rows: `model_state_error/related_object_missing`
- 1 row: cloned success with `ok=true`
- 2 rows: `integrity_error/related_object_cardinality`
- nullable local NULL: `(zero,false,nil)` without a QuerySet/backend call

Successful 0/1/2-row materialization is QuerySet success and remains cached. Warm 0/2 access reclassifies the same
missing/cardinality error without I/O. Backend/query/scan/rows/close/context failures are not cached and a later
independent call may retry. Nil/already-canceled context is rejected before null or warm fast paths. Concurrent `Get`
inherits ADR-0012 owner/waiter singleflight and cancellation behavior.

`From` captures the exact `db.Queryer`. A cold access after a transaction/session expires propagates its error. A warm
success may return its cached clone, but `Fresh` uses the same captured Queryer and cannot revive an expired session.
A different backend requires a new factory `From`. Direct pointer aliases share one object/cache; `Fresh` and a
separate `From` have independent state.

`RelatedObject` has a private `_self *RelatedObject[T]` sentinel set only after all construction succeeds. Nil, zero,
composite or dereference-copied values return `query_error/invalid_plan`; no public receiver panics.

## Exact relation AST와 SQLite trim

Existing `RelationPath.Terminal() FieldRef`와 required target-field constructor/API는 source-compatible하게
보존합니다. Additive exact surface는 다음과 같습니다.

```go
type RelationTerminalScope string

const (
	RelationTerminalRelatedField RelationTerminalScope = "related_field"
	RelationTerminalSourceKey    RelationTerminalScope = "source_key"
)

func NewNullableForwardRelationIsNullPath(
	source ir.ModelIdentity,
	sourceTable string,
	sourceKey FieldRef,
	target ir.ModelIdentity,
	targetTable, targetPKColumn string,
) (RelationPath, error)

func (p RelationPath) TerminalScope() RelationTerminalScope
```

Existing `NewForwardRelationPath` sets `related_field`. The new constructor requires an integer nullable source key,
creates exactly one nullable forward many-to-one hop, retains target identity/table/PK, sets scope `source_key`, and
keeps `Terminal()==sourceKey`. `NewRelatedCondition(path, LookupIsNull, Boolean(value))` remains the condition
constructor. `RelationPath.Equal`, clone, `Condition.Equal` and `Plan.Equal` include scope.

Typed `Reviewer.IsNull` and the new object aggregate's dynamic parser call the same private source-key path builder.
They do not erase the relation into a scalar condition. Existing `GenerateProjectRelationQuery` v1 bytes and its
`BlogPostRelations.ParseDynamic` semantics remain unchanged. The additive object parser is an ordered superset: it
preserves the existing required two-segment implicit-exact `author__name`/`author__id` behavior and adds only nullable
`reviewer__isnull`. Mixed required/nullability inputs preserve caller order, build the same predicates as their typed
forms (`Plan.Equal`), and publish no partial result on any rejection. For `reviewer__isnull`, `LookupPolicy` receives a
fresh exact canonical source FK field (`reviewer`, `reviewer_id`, nullable ForeignKey) plus `query.LookupIsNull`;
policy rejection occurs before bool value parsing. `reviewer__name`, required `author__isnull`,
reverse/multi-hop/extra suffixes remain `field_error/unsupported_lookup` before I/O.

SQLite accepts only exact source-key + `LookupIsNull` + Boolean + nullable forward one-hop metadata. It validates a
canonical nonblank hop source identity, `hop.SourceTable()==Plan.Table()`,
`Condition.Field()==path.Terminal()==sourceKey`, exact `FieldRef.Equal` presence of that source key in
`Plan.Columns()`, source-key field/column/hop consistency, target identity/table/PK, cardinality and scope, then
lowers to qualified
`"t0"."reviewer_id" IS NULL` or `IS NOT NULL` with zero JOIN. Nullable target-field traversal and LEFT JOIN remain
unsupported. Existing scalar SQL and required related-field INNER JOIN branches remain exact.

## Additive generator and project wrapper

```go
const RelationObjectGeneratorVersion = "godj-codegen-rel-object-v1"
const ProjectRelationObjectGeneratorVersion = "godj-codegen-rel-object-project-v1"

func GenerateRelationObject(packageName string, input ir.Schema) ([]byte, error)

type RelationObjectPackage struct {
	Alias      string
	ImportPath string
	Schema     ir.Schema
}

func GenerateProjectRelationObject(
	packageName string, packages []RelationObjectPackage,
) ([]byte, error)
```

`GenerateRelationObject` accepts normalized scalar v2 targets and relation v3 sources. It emits additive
`GoDjRelationObjectGeneratorVersion`, `GoDjRelationObjectSchemaSHA256`, snapshot/storage methods on every existing
model descriptor, and private storage types only for FK fields. It rewrites none of the existing main, metadata or
query companion bytes. A v2 target's descriptor is supplied by the existing main generator; a v3 source's descriptor
is supplied by the existing `GenerateRelationQuery` companion, which therefore must already be selected for that app.
The additive object file is not an independently compilable replacement for either prerequisite. Missing companion
bytes are detected by the caller's compile/publication gate, not by widening the pure generator's return contract.
Before replacement, publication compiles the exact union `{existing main (+ relation metadata), v3 relation-query
companion when applicable, new relation-object companion}` and publishes only that all-success union; no old member
is rewritten.

The project generator uses the same hardened ASCII lower-camel alias/import/prefix rules as the accepted query bridge,
with generated aliases and import paths for `context`, `db`, `orm`, `ir` and `query` reserved together with keywords,
`init` and exact used predeclared identifiers `bool`, `error`, `false`, `nil`. Normalize/namespace/render/gofmt failure returns
no bytes. External compile/bind/no-rewrite is
a caller-side publication gate; a pure generator does not claim to observe compile failure before returning bytes.
Exact fixture exports are:

```go
const GoDjProjectRelationObjectGeneratorVersion = "godj-codegen-rel-object-project-v1"

type BlogPostReviewerObjectRelation struct { /* private handle */ }
func (r BlogPostReviewerObjectRelation) IsNull(bool) orm.Predicate[blog.Post]

type BlogPostObjectFactory struct {
	Reviewer BlogPostReviewerObjectRelation
	/* private model, author and reviewer handles */
}

func (f BlogPostObjectFactory) ParseDynamic(
	orm.LookupPolicy, []orm.LookupInput,
) ([]orm.Predicate[blog.Post], error)
func (f BlogPostObjectFactory) From(db.Queryer, blog.Post) (*BlogPostObject, error)

type BlogPostObject struct { /* private model/related handles/_self */ }
func (o *BlogPostObject) Model() (blog.Post, error)
func (o *BlogPostObject) Author(context.Context) (authors.Author, error)
func (o *BlogPostObject) Reviewer(context.Context) (authors.Author, bool, error)
func (o *BlogPostObject) Fresh() (*BlogPostObject, error)

type Objects struct { BlogPost BlogPostObjectFactory }
func BindObjects() (Objects, error)
```

`BindObjects` calls existing project `Bind`, binds sealed v2/v3 descriptors, both relation handles and returns zero on
any failure. Factory `From` establishes one immutable deep-cloned source snapshot before storage extraction and
publishes an opaque pointer only after both related handles succeed; exact internal CloneModel call count is not an
external contract. `BlogPostObject` also has an exact `_self *BlogPostObject` sentinel; all receiver
methods validate it. Nil, zero, external composite or `copy := *object` then `&copy` return structured invalid-plan
errors rather than panic or silently sharing cache. `Model` returns a fresh deep clone. `Fresh` returns a new valid
pointer with independent relation QuerySets and the same captured backend/source snapshot.

Existing `GenerateProjectRelationQuery`, its generator version/golden and checked-in
`conformance/relationqueryproduct/**` bytes and behavior are explicitly forbidden from modification.

## Product fixture, manifest and false-green gates

New `conformance/relationobjectproduct/**` owns a separate checked-in generated project and actual SQLite observer.
It may reuse fixture meaning but does not import oracle/static data or expected payload constants. Existing
`relationproduct/**` and `relationqueryproduct/**` bytes remain exact.

- REL-003 Post 10 is loaded through generated `PostObjects` from actual SQLite before wrapping.
- Required cold/warm is exact 1/0; separate wrapper and `Fresh` are cold independently.
- Concurrent cold access is one SELECT; waiter cancellation does not cancel owner; owner cancellation can be retried.
- Returned source/target mutation cannot alter stored model/cache; backend/scan/rows/close failure retry is tested.
- 0 and 2-row successful snapshots produce stable warm missing/cardinality errors without a second SELECT.
- REL-006 nullable nil access is SELECT 0; non-null reviewer access proves the positive nullable loader path.
- Typed/dynamic isnull plans are equal; actual `[11]`, SELECT 1, JOIN 0 comes from SQLite.
- Changing source key, target row/name, nullable key, bool, path scope or expected ordering changes observation or
  fails structurally. Database state remains unchanged.

Manifest delta is status-only: REL-003 and REL-006 `oracle_locked -> passing`. Oracle, static not-implemented fixture,
SHA256SUMS, Django relation runner/scenario/oracle bytes stay immutable; only the Django manifest-status assertion may
change. Product order is exact observed REL-001/003/004/006 and eight ordered payload-free NI.

## CI와 완료 gate

- Preserve exact top-level 26-job topology: existing 2 + six four-coordinate matrices. No service-only job.
- Extend the existing relation-product four-coordinate package set with object runtime/codegen/SQLite/product paths;
  measure and pin exact run/pass/skip-0 inventory bytes/SHA only after implementation.
- Normal/race/CGO-disabled/vet on linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64; actual Ubuntu Linux/386.
- Existing v2/v3 main, metadata, query, project `Bind`/query bridge and both older product fixture bytes exact locked.
- New companion/bridge deterministic bytes, gofmt, schema hash/version, no-rewrite, external offline compile and exact
  app-to-app Imports/Deps 0.
- Public positive compile and negative source/target/model/value-copy/nil/typed-nil misuse fixtures.
- Descriptor/storage snapshot mutation, named non-pointer zero-size shape, post-bind caller mutation and concurrent
  reads; hot `Value` reflection 0.
- Typed/dynamic Plan.Equal, source-key scope mutation, SQLite JOIN-0 trim and old scalar/INNER SQL exact regression.
- Required/nullable access, cache/cardinality/failure/cancellation/session lifetime gates above, race and repetitions.
- Exact aggregate `114 + 5 + 8 = 127`, relation 4/12, manifest status-only and eight NI sequence.
- Routine local CPython 3.14.3 + uv 0.12.3 only; exact four Python versions remain hosted. Historical exact darwin
  keeps uv 0.10.12. `continue-on-error`, green skip, Windows and PostgreSQL/MySQL services remain absent.

## 완료 조건

- [x] Proposed ADR-0026 exact API/AST/cache/lifetime/error surface receives independent review.
- [x] Existing descriptor/query/project-query/generated product bytes and semantics are byte-preserved.
- [x] Additive v2/v3 object companions and project object bridge compile with app-to-app imports 0.
- [x] Sealed descriptor/storage snapshots reject mutable/aliased/typed-nil/copy-invalid shapes pre-I/O.
- [x] Required and nullable object loaders satisfy cold/warm/clone/singleflight/retry/cardinality/session contracts.
- [x] Typed/dynamic reviewer isnull plans are equal and SQLite returns `[11]` with SELECT 1/JOIN 0.
- [x] REL-003/006 oracle-blind actuals and independent mutation gates pass; eight other REL contracts stay NI.
- [x] Product aggregate is exact `114 passing + 5 deviation + 8 oracle_locked = 127` locally and at hosted head.
- [x] Local normal/race/CGO-disabled/vet/repetition/386 compile/no-rewrite/diff-clean gates pass.
- [x] Exact 26 hosted executions pass on the exact implementation head with job/recorded-step skip 0.
- [x] Independent API/codegen/SQLite/conformance/integration audits report P0..P3=0 locally.
- [x] Work/status/matrix/evidence and ADR are synchronized; ADR is Accepted only for the bounded slice.

## 비목표와 forbidden paths

- REL-002 assignment/save and every relation write API
- REL-005 reverse accessor/path/manager
- REL-007/008 delete collector, PROTECT/SET_NULL execution/transaction
- REL-009/010/011 `select_related`, eager hydration, LEFT JOIN and invalid reverse eager path
- REL-012 prefetch/IN batching/reverse cache
- nullable target-field predicate, `reviewer__name`, required `author__isnull`, reverse/multi-hop, OR/Q/order/aggregate
- model hidden cache state, generated main struct mutation, cache invalidation after writes, TTL/eviction
- OneToOne/ManyToMany, non-AutoField/to_field/composite target, relation DDL/migration/history
- generated batch publication/generate CLI, PostgreSQL/MySQL/Windows/multi-DB support
- `schema/**`, `migrations/**`, `go.mod`, `go.sum`, `orm/manager.go`
- existing `codegen/relation_query.go`, `codegen/project_relation_query.go`, their tests/goldens, existing
  `conformance/relationproduct/**` and `conformance/relationqueryproduct/**`
- Django relation runner/oracle/static fixture/SHA256SUMS

Any path not in frontmatter remains outside this completed packet. The single integration owner alone updates public
API, ADR, CURRENT and final status.

## 진행 기록

- 2026-08-10: GDJ-0025 final status head `bffc5284...` run `31359958949`의 exact 26/26 jobs와 326/326
  recorded steps를 EVID-042로 확인했습니다.
- 2026-08-10: Independent priority/API/scope audits selected REL-003+REL-006 together and corrected three false-green
  risks: scalar-erased isnull provenance, caller sourceKey/Manager injection, and copyable/mutable descriptor/object
  cache ownership.
- 2026-08-10: ADR-0026 Proposed, this work active. This activation documentation diff itself is
  `not run/pending`; baseline run 31359958949 is not its proof.
- 2026-08-10: Exact 15-file activation commit `aad4f7ff0d77a1abe16ebddd01782e78c335395f` passed Draft PR #1
  [run 31364944816](https://github.com/progresshans/godj/actions/runs/31364944816) with exact 26/26 jobs and
  326/326 recorded steps. This closes the activation-only pending state; it is not implementation-head evidence.
- 2026-08-10: The bounded implementation added only the sealed descriptor/storage and object runtime, additive
  v2/v3 relation-object generators/project bridge, relation `source_key` AST, SQLite JOIN-0 trim and the separate
  oracle-blind `relationobjectproduct` actual. Local product classification is exact
  `114 passing + 5 deviation + 8 oracle_locked`, with REL-001/003/004/006 actual 4/12.
- 2026-08-10: Generator feasibility exposed an alias false green: a valid package alias could shadow the emitted
  predeclared literal `false`. Before publication, Proposed ADR-0026 and this work were amended to reserve exact used
  predeclared identifiers `bool`, `error`, `false`, `nil`; generated receivers/locals were also moved to underscore
  prefixes. Existing main, metadata, query and project-query v1 bytes remain unchanged.
- 2026-08-10: Local `PYTHONDONTWRITEBYTECODE=1 make ci`, relation inventory 533/533/0 (54,076 bytes,
  SHA-256 `6d2958b63e68dcbf0a63aa02adb47cdf005a4896af80f22e4acc49e78dd07aee`), normal/race/CGO-disabled/vet,
  repetitions, generated no-rewrite, exact package Linux/386 cross-compile and twelve adapters passed. Independent
  runtime, codegen, SQLite/conformance and final integration/security/scope audits all report
  P0/P1/P2/P3=`0/0/0/0`; at this pre-hosted evidence point exact implementation-head CI had not run.
- 2026-08-10: Implementation commit `5be46141d943800a3c621975e3e5070f6d01eaf9`의 Draft PR #1
  [run 31370313755](https://github.com/progresshans/godj/actions/runs/31370313755)은 exact 26/26 jobs와
  326/326 recorded steps를 성공했습니다. Four relation-product coordinates는 각각 533 run/533 pass/0
  skip·54,076 bytes·SHA-256 `6d2958b6...7aee`를 재현했고 full Ubuntu actual Linux/386 exact package set,
  four exact Python compatibility legs와 exact Darwin 193/193도 통과했습니다. Independent hosted audit는
  P0/P1/P2/P3=`0/0/0/0`이며 EVID-044에 기록했습니다. Bounded ADR-0026을 Accepted, 이 work를 completed로
  전환합니다. 이 completion-documentation patch 자체 exact-head CI는 `not run/pending`이며 run
  `31370313755`를 그 증거로 재사용하지 않습니다.

## 현재 blocker

외부 blocker는 없습니다. Local implementation/conformance, all four independent local audit lanes와 exact
implementation-head hosted acceptance/hosted audit가 모두 통과했습니다. Completion-documentation patch의
별도 exact-head CI만 pending이며 bounded work completion의 blocker는 아닙니다.

## 다음 정확한 작업

1. Freeze the exact 15-file completion-documentation patch, preserving the EVID-001..043 body prefix and verifying
   frontmatter uniqueness, allowed paths, links, fences and `git diff --check`.
2. Commit/push only that documentation patch and run unchanged exact 26 at the completion-documentation head. Do not
   reuse implementation run 31370313755 as recursive proof.
3. Record the separate completion-documentation exact-head result without widening Q-013 or the supported surface.
4. Do not merge Draft PR #1 without user request.

## 인수인계

GDJ-0026 is completed at implementation head `5be46141d943800a3c621975e3e5070f6d01eaf9`, exact-26 tested by
run `31370313755`. It owns only REL-003/006 forward instance object cache, nullable absent access and
relation-provenance-preserving SQLite source-key isnull trim. Product comparison is exact `114 + 5 + 8`, relation
actual REL-001/003/004/006 4/12. ADR-0026 is Accepted for this bounded slice and Q-013 remains `Partial`.
Completion-documentation exact-head CI is pending and no Draft PR merge is authorized.
