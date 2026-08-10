# ADR-0026: Forward ForeignKey Object Cache and Nullability

- 상태: Proposed
- 날짜: 2026-08-10
- 관련 work/contract:
  [GDJ-0026](../../work/0026-forward-foreign-key-object-cache-and-nullability-product-slice.md),
  REL-003, REL-006, Q-013
- 선행 결정: [ADR-0002](0002-codegen-generics-runtime-metadata.md),
  [ADR-0003](0003-typed-and-dynamic-query-apis.md),
  [ADR-0006](0006-codegen-input-package-boundary.md),
  [ADR-0008](0008-m1-sqlite-driver-and-execution-boundary.md),
  [ADR-0012](0012-queryset-evaluation-cache-ownership.md),
  [ADR-0023](0023-symbolic-relation-binding-and-shared-relation-ast.md),
  [ADR-0024](0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md),
  [ADR-0025](0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)
- 대체하는 ADR: 없음

## 상태와 범위

이 ADR은 **Proposed**입니다. Exact API와 ownership은 independent architecture/API/scope review를 반영해
implementation 전에 동결했지만, product code/local/hosted verification은 아직 없습니다. Baseline
`bffc52844de87a2791959ea1e8f99c60dd13d1aa`만
[EVID-20260810-042](../status/TEST_EVIDENCE.md#evid-20260810-042--gdj-0025-final-exact-head-ci-and-gdj-0026-activation-baseline)의
exact 26/26 hosted gate를 통과했습니다. Activation diff 자체는 `not run/pending`입니다.

결정 범위는 AutoField-target one-hop forward relation의 generated opaque object wrapper, required/nullable
lazy load와 instance-owned cache, nullable local-NULL fast path, typed/dynamic relation-level `isnull` shared AST와
SQLite root-FK JOIN-0 trim입니다. REL-003/006 외 reverse/eager/write/delete/DDL/migration, LEFT JOIN,
non-SQLite backend와 broader target key는 결정하지 않습니다.

## 맥락

ADR-0024의 v3 main은 `Post{AuthorID int64, ReviewerID *int64}` plain scalar storage만 생성합니다. ADR-0025는
additive query companion/project query bridge와 required target-field relation path를 만들었지만 object
loader/cache와 nullable path는 의도적으로 제외했습니다. ADR-0023은 cache가 Query AST가 아니라 evaluated
model/result instance를 소유하고 `isnull`은 join trim이 가능하다고 정했습니다.

REL-003만 구현하면 required relation cache ABI가 nullable absent semantics보다 먼저 굳습니다. REL-006만 raw
`ReviewerID IS NULL` scalar condition으로 구현하면 result는 맞아도 typed/dynamic relation provenance와 future
backend validation이 사라집니다. 둘을 함께 구현해야 required/non-null nullable load, local NULL fast path,
cache ownership과 source-key relation AST를 하나의 bounded boundary로 검증할 수 있습니다.

## 결정 기준

- Existing plain v3 model/main, metadata/query companions, project `Bind`/query bridge bytes와 behavior 보존
- Source/target descriptor의 caller-owned mutable state를 bound object/cache에 retain하지 않음
- Caller source-key callback, target Manager와 reflection field read 없는 structurally bound key extraction
- Generated source app가 target app를 import하지 않고 project object bridge만 concrete types 연결
- Freshly DB-loaded source instance 하나가 cache를 소유하고 pointer alias/Fresh/new From 의미가 명확함
- Successful empty/non-empty/cardinality snapshot과 retryable I/O failure의 cache 경계
- Nil/typed-nil/zero/value-copy receiver가 panic 또는 silent cache share 없이 structured failure
- Typed/dynamic reviewer isnull의 Plan.Equal과 relation provenance 보존
- Existing `RelationPath.Terminal() FieldRef` ABI를 깨지 않는 additive AST
- SQLite compiler가 validated source-key scope만 root FK로 trim하고 JOIN 0을 증명
- REL-003/006만 status 전환하고 eight relation contracts를 ordered NI로 유지

## 고려한 선택지

### Plain generated model에 target object/cache hidden fields 추가

직관적인 `post.Author` cache를 만들 수 있지만 ADR-0024의 exact plain v3 struct bytes/ABI를 바꾸고 caller
value copy마다 mutex/cache ownership 문제가 생깁니다. 기존 literal/model serialization과 generator upgrade를
이 packet이 선결하므로 채택하지 않습니다.

### Object binder에 source-key callback과 target Manager 전달

구현은 작지만 callback/Manager가 bound symbolic field/target과 구조적으로 연결되지 않습니다. 잘못된 field,
manager 또는 constant callback이 locked payload를 false-pass할 수 있습니다. Public input으로 채택하지 않습니다.

### `RelationObjectDescriptor` original interface를 BoundModel에 저장

Pointer/stateful descriptor가 bind 뒤 mutation되면 Scan/Clone/storage behavior가 바뀌고 immutable snapshot 및
concurrent read claim을 위반합니다. Metadata만 clone해도 method receiver alias는 남습니다. Original interface
retention을 금지하고 explicit immutable snapshot capability를 채택합니다.

### Nullable isnull을 scalar FK Condition으로 즉시 lower

SQLite result/JOIN count는 맞지만 relation source/target/cardinality/nullability가 Plan에서 사라집니다. Typed와
dynamic path가 relation compiler validation을 우회하고 ADR-0023 shared relation AST를 깨므로 채택하지 않습니다.

### Copyable exported wrapper value

Private QuerySet state pointer가 있어도 `copy := *object`가 cache를 묵시적으로 공유합니다. Value 내부 mutex는
copy-after-use 위험이 있습니다. Pointer return, private fields와 self sentinel을 채택합니다.

### Sealed descriptor companion + opaque project wrapper + source-key relation scope

Existing generated artifacts를 보존하면서 project bridge에서 concrete source/target을 연결합니다. Descriptor와
storage는 immutable zero-state snapshots로 seal하고 QuerySet의 accepted cache state machine을 재사용합니다.
Nullable isnull은 source-key terminal scope를 가진 relation path로 compiler까지 남깁니다. 이 option을 채택합니다.

## 결정

### Immutable descriptor/storage seal

Exact additive ORM capability는 다음과 같습니다.

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

`BindModel`은 descriptor Metadata snapshot validation을 유지합니다. Input이 object capability를 제공하면
`SnapshotRelationObjectDescriptor`를 exactly once 호출하고 다음 순서로 검증합니다.

1. original nil/typed-nil과 project snapshot/identity/metadata
2. returned snapshot nil/typed-nil
3. snapshot dynamic type가 named non-pointer zero-size struct인지
4. snapshot Metadata가 bound model과 exact semantic equality인지

실패는 `CategoryQuery/CodeInvalidPlan`; original descriptor는 private state에 저장하지 않습니다. 성공한
snapshot만 optional sealed slot에 저장하며 immutable/concurrent-safe가 interface contract입니다. Existing
non-object `BindModel` use는 query path에 계속 사용할 수 있지만 object binders는 source/target 모두 sealed
snapshot을 요구합니다.

`BindRelationStorage`는 source model의 exact canonical `ir.Field`를 받습니다. Returned storage는 nil/typed-nil,
named non-pointer zero-size shape와 `reflect.DeepEqual(storage.Field(), canonicalField)`를 cold bind-time에
검증합니다. Generated storage's `Value(M)` is a direct field access and uses no reflection. Required FK returns
`query.Integer`; nullable nil returns `query.Null`, non-nil returns `query.Integer`; unknown/wrong field returns false.
Storage snapshot is immutable/concurrent-safe. Caller-provided callbacks/Manager/global registry are absent.

### Object relation binding and loading

```go
type RequiredForwardObject[S, T any] struct { /* private */ }
type NullableForwardObject[S, T any] struct { /* private */ }
type RelatedObject[T any] struct { /* private, including _self */ }

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

Binders require one project snapshot, exact source relation/target, AutoField target key, many-to-one cardinality and
matching required/nullable shape. They derive storage, target `Manager`, PK field and path only from sealed values.
Zero/different snapshots, missing capability/storage, target mismatch and invalid shape return invalid-plan or existing
field errors before I/O; no partial handle is published.

`From` rejects nil/typed-nil backend. It deep-clones the source through sealed descriptor before extracting storage,
then builds target PK `Exact` QuerySet with exact limit 2. It does no I/O and publishes a pointer only after every
validation/construction succeeds. Required null/wrong-kind storage is invalid-plan. Nullable null records an absent
state and creates no target QuerySet.

`RelatedObject.Get` checks nil/already-canceled context before any warm/null path and delegates non-null access to
`Limit(2).All`, not `First`.

| Materialized rows/state | Public result |
|---|---|
| nullable local NULL | `(zero, false, nil)`, backend I/O 0 |
| 0 successful rows | `CategoryModelState/CodeRelatedObjectMissing` |
| 1 successful row | cloned target, `true`, nil error |
| 2 successful rows | `CategoryIntegrity/CodeRelatedObjectCardinality` |

Exact new codes are:

```go
const CodeRelatedObjectMissing = "related_object_missing"
const CodeRelatedObjectCardinality = "related_object_cardinality"
```

Limit 2 bounds cardinality observation. Successful 0/1/2 snapshots are QuerySet successes and are cached; warm 0/2
calls reclassify the same error without I/O. Backend/query/scan/rows/close/context failures are not cached. ADR-0012
owner/waiter singleflight, waiter-only cancellation, owner cancellation retry and model clone rules apply unchanged.

The backend is captured by `From`. Cold access after a session expires returns its error. Warm success may return its
cached clone; `Fresh` retains the same backend and cannot revive the session. A different backend requires a new
`From`. Pointer aliases share one state; `Fresh` and separate `From` create independent QuerySet state.

`RelatedObject` carries `_self *RelatedObject[T]`, set only after all-success construction. Every method validates
`r != nil && r._self == r`; nil/zero/composite/dereference-copy returns invalid-plan. `Fresh` is error-bearing and
creates a new valid pointer; no receiver panics.

### Relation-level nullable isnull AST

Existing `RelationPath.Terminal() FieldRef`, `NewForwardRelationPath` and required related-field meaning stay
source-compatible. Exact additive surface:

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

The existing constructor sets scope `related_field`. The nullable constructor requires valid symbolic source/target,
nonblank tables/PK, integer nullable `sourceKey`; it creates exactly one forward many-to-one nullable hop. Its
`Terminal()` is the source key and scope is `source_key`. Equality/clone/Condition/Plan include scope. Typed
`NullableForwardObject.IsNull` and new dynamic object parser both use this constructor followed by existing
`NewRelatedCondition(path, LookupIsNull, query.Boolean(value))`.

This is relation-level provenance, not legacy scalar lowering. `reviewer__isnull` is the only new dynamic shape.
`LookupPolicy`, when non-nil, receives a fresh clone of the exact canonical source FK field (`reviewer`,
`reviewer_id`, nullable ForeignKey) and `query.LookupIsNull`; rejection precedes Boolean value parsing.
`reviewer__name`, required `author__isnull`, reverse/multi-hop, extra/empty segments and other lookups remain
`CategoryField/CodeUnsupportedLookup` before I/O. Policy rejection and non-bool value keep existing codes.

Existing `ParseDynamicRelations` and generated `BlogPostRelations.ParseDynamic` retain their ADR-0025 semantics.
New `ParseDynamicRelationObjects` is an ordered additive superset called only by the new object aggregate: it preserves
the existing required two-segment implicit-exact `author__name`/`author__id` behavior and adds nullable
`reviewer__isnull`. Mixed required/nullability inputs preserve caller order, are atomic on rejection, and produce
predicates/Plans equal to the corresponding typed constructors. Existing project-query v1 bytes and behavior are not
widened under the old generator version.

### SQLite compiler decision

The existing scalar branch and required `related_field` INNER JOIN branch remain exact. The additive branch accepts
only a condition satisfying all of:

- one relation hop, direction forward, cardinality many-to-one, nullable true
- terminal scope `source_key`
- `Condition.Field()` equals the path terminal/source key, which equals the hop source field/column and is integer nullable
- that source key exists in `Plan.Columns()` by exact `FieldRef.Equal`
- source identity is canonical/nonblank, source table equals `Plan.Table()`, and source key matches the hop
- valid target identity/table/PK metadata is retained
- lookup exact `IsNull`, value exact Boolean

It then qualifies the root as `t0` and emits `"t0"."reviewer_id" IS NULL` or `IS NOT NULL`, adding no join.
Invalid scope/metadata/value/lookup fails before `QueryContext`. Nullable target-field traversal, LEFT JOIN and
required source-key isnull remain unsupported.

### Additive generator surface

```go
package codegen

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

`GenerateRelationObject` accepts normalized scalar IR v2 targets and relation IR v3 sources. Every model descriptor
receives exact generated provenance `GoDjRelationObjectGeneratorVersion`, `GoDjRelationObjectSchemaSHA256`, immutable
snapshot and storage-binding methods. FK source fields get private named zero-size storage types with direct field
access; target-only v2 descriptors return no storage. The descriptor prerequisite is the existing main-generator
output for v2 targets and the existing `GenerateRelationQuery` companion for v3 sources. The additive object output
does not replace or independently compile without that prerequisite, and it does not rewrite existing
main/metadata/query bytes. A missing prerequisite is a caller compile/publication failure, not a new pure-generator
failure mode. Before replacement, the caller compiles the union `{existing main (+ relation metadata), v3
relation-query companion when applicable, new relation-object companion}` and publishes only on all-success; every
old member remains byte-identical.

`GenerateProjectRelationObject` snapshots/normalizes/canonicalizes inputs. Alias is hardened ASCII lower-camel with
generated aliases and import paths for `context`, `db`, `orm`, `ir`, `query`, keywords, `init` and exact used
predeclared identifiers `bool`, `error`, `false`, `nil` rejected;
import paths, prefixes and all generated namespaces are unique. App packages never import one another; only the project
object bridge imports both. Normalize/namespace/render/gofmt failure returns no bytes and never panics. External
compile/bind/no-rewrite remains a caller publication gate rather than a failure the pure byte generator can observe.

Exact fixture-level project surface:

```go
const GoDjProjectRelationObjectGeneratorVersion = "godj-codegen-rel-object-project-v1"

type BlogPostReviewerObjectRelation struct { /* private nullable handle */ }
func (r BlogPostReviewerObjectRelation) IsNull(bool) orm.Predicate[blog.Post]

type BlogPostObjectFactory struct {
	Reviewer BlogPostReviewerObjectRelation
	/* private bound model and required/nullable handles */
}
func (f BlogPostObjectFactory) ParseDynamic(
	orm.LookupPolicy, []orm.LookupInput,
) ([]orm.Predicate[blog.Post], error)
func (f BlogPostObjectFactory) From(db.Queryer, blog.Post) (*BlogPostObject, error)

type BlogPostObject struct { /* private model, related handles, _self */ }
func (o *BlogPostObject) Model() (blog.Post, error)
func (o *BlogPostObject) Author(context.Context) (authors.Author, error)
func (o *BlogPostObject) Reviewer(context.Context) (authors.Author, bool, error)
func (o *BlogPostObject) Fresh() (*BlogPostObject, error)

type Objects struct { BlogPost BlogPostObjectFactory }
func BindObjects() (Objects, error)
```

`BindObjects` calls existing `Bind`, seals all descriptors, binds author/reviewer handles and returns zero on failure.
Factory `From` establishes an immutable deep-cloned Post snapshot before relation storage extraction, constructs both
related objects, then initializes `_self`; exact internal CloneModel call count is not a contract and no partial pointer escapes.
`BlogPostObject` validates `o != nil && o._self == o` on every method. `Model` returns a fresh deep clone. `Fresh`
returns an independent valid wrapper with the same source snapshot/backend. Nil, zero, composite and dereference-copy
are structured invalid-plan failures.

`GenerateProjectRelationQuery`, `RelationQueryGeneratorVersion`,
`ProjectRelationQueryGeneratorVersion`, their goldens and checked-in `relationqueryproduct` output are immutable in
this decision. Typed/dynamic nullable APIs are owned entirely by the new `Objects.BlogPost` surface.

### Product and compatibility decision

New `conformance/relationobjectproduct/**` is an independent checked-in generated fixture. The actual adapter reads
actual SQLite and generated APIs only; oracle/static fixtures and expected constants are not imported. REL-003 first
loads Post 10 with generated QuerySet, then observes required cold/warm 1/0. REL-006 observes nil Reviewer at 0 I/O and
typed/dynamic isnull `[11]` at one SELECT/JOIN 0. It additionally proves non-null nullable load, independent Fresh/new
From, clone isolation, singleflight/cancellation, failure retry and 0/2 cardinality behavior as invariant gates.

Only REL-003 and REL-006 transition `oracle_locked -> passing`; REL-001/004 remain passing and REL-002/005/007..012
stay ordered payload-free NI. Completion aggregate target is exact
`114 passing + 5 deviation + 8 oracle_locked`, relation 4/12. Reference oracle/static/SHA and Django scenario bytes
remain immutable; only manifest status and its Python assertion change.

### Hosted verification boundary

Exact top-level CI remains 26 required executions. The four existing relation-product coordinates expand their package
set and measured inventory only; no seventh matrix/service job is added. Linux/macOS x64/arm64 run normal/race/
CGO-disabled/vet/no-rewrite/clean, full Ubuntu executes Linux/386, four Python versions remain hosted-only, routine
local remains CPython 3.14.3 + uv 0.12.3 and historical exact darwin keeps uv 0.10.12.

Activation baseline run 31359958949 proves only `bffc5284...` and current REL-001/004. This Proposed ADR/activation
diff, implementation head and completion documentation each require their own non-reused exact-head evidence.

## Error ownership and precedence

Validation precedence is exact:

1. nil/typed-nil context/backend/descriptor and wrapper self sentinel
2. project/BoundModel snapshot and sealed descriptor snapshot
3. source relation/cardinality/nullability/target identity
4. exact storage field/shape/value kind and target AutoField
5. dynamic path shape/policy/value or typed predicate construction
6. target QuerySet/backend evaluation
7. successful row cardinality classification

Descriptor/snapshot/zero/copy/backend configuration errors are query invalid-plan except nil backend, which retains the
existing backend invalid-plan ownership used by QuerySet. Unknown relation/field and unsupported path retain existing
field taxonomy. Target backend/scan/rows errors preserve their existing causes/categories. Detail text is diagnostic,
not a compatibility contract.

## Rejected or deferred surface

- SourceKey callbacks, target Manager parameters, raw reflection loader and global descriptor registry
- Hidden fields/cache/mutex in generated model structs; value object wrapper
- Relation AST scalar erasure; modifying old project-query v1 to add Reviewer
- Required relation `isnull`, nullable target-field predicate, LEFT JOIN, reverse/multi-hop
- Cache invalidation on relation key mutation/write, eager cache priming, TTL/eviction
- Assignment/save/delete/DDL/migration/historical model and PostgreSQL/MySQL/Windows
- Non-AutoField/to_field/composite target, OneToOne/ManyToMany

## Acceptance 조건

ADR status can become Accepted only after all GDJ-0026 gates pass:

1. Exact public API compiles externally; nil/typed-nil/zero/value-copy misuse is panic-free structured failure.
2. Original descriptor interfaces are never retained; sealed descriptor/storage mutation and race tests pass.
3. Existing generated and relation product bytes/semantics remain exact; new companion/bridge is deterministic.
4. Required/nullable object cold/warm/Fresh/new From/clone/cardinality/failure/session/cancellation tests pass.
5. Typed/dynamic reviewer isnull Plan.Equal retains `source_key` scope and SQLite returns `[11]`, SELECT 1, JOIN 0.
6. Oracle-blind REL-003/006 actuals and mutation gates pass; aggregate/status/NI sequence is exact.
7. Local normal/race/CGO-disabled/vet/repetition/386/no-rewrite gates pass.
8. Exact implementation-head 26/26 hosted executions and independent audits pass with skip 0/P0..P3=0.

Until then this document is a reviewed proposal, not evidence that REL-003/006 or relation-object APIs exist.

## Consequences

- Plain generated models and existing relation-query ABI remain stable while object access is additive.
- Immutable descriptor sealing adds cold bind-time reflection/shape checks but keeps row/key hot paths direct.
- Reusing QuerySet gives object loading tested cache/cancellation/clone behavior and bounds cardinality to two rows.
- Pointer/self sentinels make accidental dereference-copy a visible error rather than silent shared state.
- Source-key terminal scope preserves relation provenance while allowing SQLite to avoid a needless join.
- The design deliberately does not solve eager hydration, write invalidation, reverse cache or multi-backend lifetime.
