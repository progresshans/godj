---
id: GDJ-0027
status: active
updated: 2026-08-11
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "9ba1d0ee4cb96c265269000700beb5889fef2206"
depends_on: ["GDJ-0026"]
contracts: ["REL-005", "Q-013"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "query/relation.go"
  - "query/relation_test.go"
  - "orm/descriptor.go"
  - "orm/dynamic_relation.go"
  - "orm/dynamic_relation_test.go"
  - "orm/reverse_relation.go"
  - "orm/reverse_relation_test.go"
  - "orm/reverse_relation_external_test.go"
  - "orm/reverse_object.go"
  - "orm/reverse_object_test.go"
  - "orm/reverse_object_external_test.go"
  - "codegen/project_relation_reverse.go"
  - "codegen/project_relation_reverse_test.go"
  - "codegen/testdata/relation_reverse/**"
  - "db/sqlite/compiler.go"
  - "db/sqlite/compiler_test.go"
  - "db/sqlite/integration_test.go"
  - "conformance/README.md"
  - "conformance/contracts/relation-manifest.json"
  - "conformance/relationreverseproduct/**"
  - "conformance/runners/godj/relation_scenarios.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/runners/django/tests/test_relation_scenarios.py"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/internal/protocol/product_compare_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_reverse/**"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0027-reverse-foreign-key-accessor-and-lookup.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0026-forward-foreign-key-object-cache-and-nullability-product-slice.md"
  - "work/0027-reverse-foreign-key-accessor-and-lookup-product-slice.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Reverse ForeignKey Accessor and Lookup Product Slice

## 결과 목표

GDJ-0027은 locked Django 6.1 `REL-005`를 하나의 bounded product slice로 구현합니다. 이미 project binding에
존재하는 reverse metadata를 shared relation AST, typed/dynamic query, SQLite compiler와 generated project
object surface까지 연결합니다.

1. Actual SQLite에서 freshly loaded `Author(1)`의 generated reverse object accessor `Posts()`를 평가하면
   Post IDs exact `[10, 11]`, SELECT 1, JOIN 0입니다.
2. Typed `reverseRelations.AuthorsAuthor.Posts.Title.Exact("Alpha")`와 dynamic
   `posts__title=Alpha`는 같은 immutable `RelationPath`와 `Plan`을 만들고 Author IDs exact `[1]`, SELECT 1,
   INNER JOIN 1을 반환합니다.
3. 두 경로의 construction은 I/O 0이고 database state는 불변입니다. 미지원 reverse shape는 구조화 오류로
   actual query 전에 닫습니다.

완료 aggregate 목표는 exact 12 adapter sets/127 contracts =
`115 passing + 5 deviation + 7 oracle_locked`, relation actual REL-001/003/004/005/006 5/12입니다.

## 기준 상태와 선행 증거

- Baseline은 `codex/revision-fenced-migration-lifecycle@9ba1d0ee4cb96c265269000700beb5889fef2206`입니다.
- Baseline exact-head [run 31374150640](https://github.com/progresshans/godj/actions/runs/31374150640)은
  26/26 jobs와 326/326 recorded steps를 성공했습니다. Four relation-product legs는 각각 533/533/0,
  54,076 bytes, SHA-256 `6d2958b63e68dcbf0a63aa02adb47cdf005a4896af80f22e4acc49e78dd07aee`입니다.
  이 사실은 activation evidence에 새로 기록하되 GDJ-0027 구현 증거로 재사용하지 않습니다.
- [ADR-0023](../docs/adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)의 typed/dynamic shared
  relation AST, [ADR-0025](../docs/adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)의 related
  field predicate, [ADR-0026](../docs/adr/0026-forward-foreign-key-object-cache-and-nullability.md)의 sealed
  descriptor/object cache를 보존합니다.
- Current product는 exact `114 passing + 5 deviation + 8 oracle_locked`, relation 4/12입니다.

## Locked REL-005 외부 동작

Fixture는 existing authors/blog 의미를 그대로 사용합니다.

- authors: `(1, Ada)`, `(2, Bob)`, `(3, Cleo)`
- posts: `(10, Alpha, author=1, reviewer=2)`, `(11, Beta, author=1, reviewer=NULL)`,
  `(12, Gamma, author=3, reviewer=2)`
- reverse declarations: `Author.posts <- Post.author`, `Author.reviewed_posts <- Post.reviewer`

Accessor contract는 actual SQLite에서 Author 1을 먼저 freshly load합니다. Generated project reverse factory로
그 instance를 감싼 뒤 `Posts()`를 explicit source-PK ascending order로 평가합니다. 결과는 exact `[10,11]`,
evaluation SELECT 1, JOIN 0입니다. Setup, source Author load와 teardown은 metric window 밖입니다.

Lookup contract는 typed `Posts.Title.Exact("Alpha")`와 dynamic `posts__title=Alpha`의 `Plan.Equal`을 먼저
확인합니다. 둘 모두 Author root를 대상으로 Post를 INNER JOIN하고 exact `[1]`, SELECT 1, JOIN 1을 반환합니다.
같은 reverse edge의 둘 이상의 terminal predicate는 하나의 JOIN을 재사용합니다. Nullable declaration에서
파생한 `reviewed_posts__title` exact 역시 target value predicate이므로 INNER JOIN입니다.

## Reverse AST 결정

Reverse hop은 physical ForeignKey declaration을 뒤집어 저장하지 않습니다. 다음 declaration-centric 의미를
보존합니다.

- `Source`: FK declaration owner `blog.post`
- `SourceTable`: `blog_post`
- `Field` / `SourceColumn`: `author` / `author_id`
- `Target`: reverse namespace owner `authors.author`
- `TargetTable` / target primary key: `authors_author` / `id`
- `Direction`: `reverse`
- traversal cardinality: `one_to_many`
- `Nullable`: original FK declaration 값
- `ReverseName`: `posts`

Exact additive query surface는 [Proposed ADR-0027](../docs/adr/0027-reverse-foreign-key-accessor-and-lookup.md)에
동결합니다.

```go
const RelationReverse RelationDirection = "reverse"

func NewReverseRelationPath(
	source ir.ModelIdentity,
	sourceTable, sourceField, sourceColumn string,
	target ir.ModelIdentity,
	targetTable, targetPKColumn, reverseName string,
	nullable bool,
	terminal FieldRef,
) (RelationPath, error)

func (h RelationHop) ReverseName() string
```

Existing forward constructors, `RelationHop.Field()`과 source/target accessors는 source-compatible합니다. Reverse
name은 private hop state에 저장하고 clone/equality에 포함합니다. Constructor는 one-hop reverse one-to-many,
related-field terminal만 만들며 blank/noncanonical structural metadata와 unsupported terminal `FieldRef` kind를
구조화 오류로 거부합니다. Terminal이 실제 source model field인지, source/target model/table/column/PK가 project
schema와 일치하는지는 complete snapshot을 가진 ORM binder가 constructor 호출 전에 검증합니다.

SQLite는 direction별로 root와 JOIN orientation을 검증합니다.

- forward: root=`SourceTable`, join=`root.SourceColumn = alias.TargetPK`
- reverse: root=`TargetTable`, joined table=`SourceTable`,
  join=`root.TargetPK = alias.SourceColumn`, terminal은 joined alias

Join identity에는 direction을 포함합니다. 같은 physical self-relation의 forward와 reverse path가 alias를
잘못 공유해서는 안 됩니다. Scalar, forward related-field와 nullable source-key branches의 SQL/args/error
semantics는 byte/behavior locked입니다. Compiler는 Plan과 path가 실제로 제공하는 root table, canonical
identity/string columns, direction/cardinality/reverse name, terminal `FieldRef`와 lookup/value만 검증합니다.
Schema-semantic source/target model membership은 compiler의 입력이 아니며 ORM binder 책임입니다.

## Query API와 object accessor 분리

Reverse predicate binding은 owner primary-key presence와 무관해야 합니다. Query-only API와 object accessor를
한 handle에 결합하지 않습니다.

```go
type ReverseRelation[Owner, Source any] struct { /* private sealed state */ }

func BindReverse[Owner, Source any](
	owner BoundModel[Owner], reverseName string, source BoundModel[Source],
) (ReverseRelation[Owner, Source], error)

func (r ReverseRelation[Owner, Source]) Integer(
	field IntegerField[Source],
) (RelatedIntegerField[Owner], error)

func (r ReverseRelation[Owner, Source]) String(
	field StringField[Source],
) (RelatedStringField[Owner], error)
```

`BindReverse`는 same immutable project snapshot, exact reverse namespace, forward declaration, source/owner model,
source FK column, target AutoField와 one-to-many traversal을 검증합니다. `ReverseRelation`은 owner descriptor의
PK capability를 요구하지 않습니다. Typed and dynamic builders use the same state/path constructor.

Accessor 전용 capability는 이미 sealed된 object descriptor에만 추가합니다.

```go
type PrimaryKeyObjectDescriptor[M any] interface {
	RelationObjectDescriptor[M]
	PrimaryKey(M) (query.Value, bool)
}

type ReverseObject[Owner, Source any] struct { /* private sealed state */ }
type RelatedSet[M any] struct { /* private QuerySet and self sentinel */ }

func BindReverseObject[Owner, Source any](
	owner BoundModel[Owner], reverseName string, source BoundModel[Source],
) (ReverseObject[Owner, Source], error)

func (r ReverseObject[Owner, Source]) From(
	backend db.Queryer, owner Owner,
) (*RelatedSet[Source], error)

func (s *RelatedSet[M]) OrderBy(orderings ...Ordering[M]) (*RelatedSet[M], error)
func (s *RelatedSet[M]) All(context.Context) ([]M, error)
func (s *RelatedSet[M]) Fresh() (*RelatedSet[M], error)
```

`BoundModel` layout과 `BindModel` behavior는 바꾸지 않습니다. `BindReverseObject`는 owner의 existing sealed
`objectDescriptor` dynamic value를 `PrimaryKeyObjectDescriptor[Owner]`로 type-assert합니다. Original descriptor,
`WriteDescriptor`, caller callback/Manager와 reflection field access를 retain하지 않습니다. v2 generated
`AuthorDescriptor`는 hidden presence bit가 있는 `PrimaryKey`와 existing object snapshot을 통해 capability를
충족합니다. Capability가 없는 v3 owner는 query-only reverse lookup은 사용할 수 있지만 accessor binding은
pre-I/O `query_error/invalid_plan`으로 거부합니다.

`From`은 nil/typed-nil backend를 `CategoryBackend/CodeInvalidPlan`, zero/different snapshot과 wrong PK kind를
`CategoryQuery/CodeInvalidPlan`, missing owner PK를 기존 `CategoryQuery/CodeMissingPrimaryKey`로 I/O 전에
거부합니다. 숫자 `0`만으로 presence를 추론하지 않습니다. It builds a source QuerySet with exact local FK predicate and
default source AutoField ascending ordering, then publishes one pointer only after complete validation.

`RelatedSet`은 pointer/self-sentinel로 nil/zero/composite/dereference-copy를 `invalid_plan`으로 거부합니다.
`All`은 underlying QuerySet의 clone/cache/singleflight/cancellation/retry semantics를 그대로 사용합니다.
`OrderBy`와 `Fresh`는 새 pointer/new evaluation state를 반환하고 I/O를 하지 않습니다. Public `Filter`, IN,
prefetch/warm injection API는 만들지 않습니다. 후속 REL-012가 private ownership을 확장할 수는 있지만 이번
packet에서 speculative constructor 또는 unused warm state를 구현하지 않습니다.

## Dynamic lookup와 오류 순서

Existing `ParseDynamicRelations`와 generated forward delegates는 byte/semantic unchanged입니다. Additive
`ParseDynamicReverseRelations`가 named reverse relation의 exact two-segment target-field lookup만 지원합니다.
Reverse resolution은 `snapshot.reverse`의 owner/name과 대응하는 `snapshot.forward`의 source/field를 함께
검증합니다.

```go
func ParseDynamicReverseRelations[M any](
	owner BoundModel[M], policy LookupPolicy, inputs []LookupInput,
) ([]Predicate[M], error)
```

Error precedence는 다음과 같이 고정합니다.

1. path shape: exactly two non-empty segments; suffix/multi-hop/empty segment 거부
2. reverse relation namespace resolution
3. related terminal field resolution와 supported non-null Auto/Char kind
4. `LookupPolicy` with fresh canonical terminal field and `LookupExact`
5. dynamic value parsing
6. path/condition publication

어느 입력 하나라도 실패하면 predicates는 nil이며 partial prefix를 공개하지 않습니다. Unknown namespace는
`unknown_relation`, unknown terminal은 `unknown_related_field`; unsupported shape/kind는 `unsupported_lookup`입니다.

## Project-only additive codegen

Per-app main, metadata, relation-query/object companions와 existing project query/object generators를 수정하지
않습니다. New project-only generator가 existing package union을 compile-time 연결합니다.

```go
const ProjectRelationReverseGeneratorVersion = "godj-codegen-rel-reverse-project-v1"

type RelationReversePackage struct {
	Alias      string
	ImportPath string
	Schema     ir.Schema
}

func GenerateProjectRelationReverse(
	packageName string,
	packages []RelationReversePackage,
) ([]byte, error)
```

The exact fixture publication union has nine files: authors main + relation metadata + relation object; blog main +
relation metadata + relation query + relation object; project binding bridge + new project relation-reverse companion.
Authors v2 main already supplies its descriptor/PK and needs no query companion. The pure generator does not inspect a
filesystem or claim prerequisite presence: it owns input clone/normalize, namespace resolution, render and gofmt only.
Missing/wrong-version prerequisite companions are caller publication/union-compile failures, tested in an isolated
external module with last-known-good outputs preserved.

Generated file owns two independent aggregates.

- `ReverseRelations` / `BindReverseRelations`: every named reverse edge's query-only typed handles and one dynamic delegate
- `ReverseObjects` / `BindReverseObjects`: PK-capability From-only owner factories and owner wrappers

Fixture query surface includes `ReverseRelations.AuthorsAuthor.Posts.Title`,
`ReverseRelations.AuthorsAuthor.ReviewedPosts.Title`, and `ReverseRelations.AuthorsAuthor.ParseDynamic`. Object surface
is deliberately not a duplicate query namespace: `ReverseObjects.AuthorsAuthor` exposes only `From`, and opaque pointer
`AuthorsAuthorReverseObject` exposes `Model()`, `Posts()`, `ReviewedPosts()`, `Fresh()`. The wrapper privately owns one
`*RelatedSet[blog.Post]` per named reverse edge and has its own self sentinel, so value-copy cannot share cache silently.
`Model` and `Fresh` are error-bearing and return clones/new valid pointer. `BindReverseRelations` does not fail merely
because the owner lacks PK capability; `BindReverseObjects` is all-or-nothing and may reject that capability separately.

`BindReverseObjects` binds the private `Posts` and `ReviewedPosts` runtime object handles from one local snapshot and
does not reuse a separately bound `ReverseRelations` value. Its exact public factory method is
`From(db.Queryer, authors.Author) (*AuthorsAuthorReverseObject, error)`; wrapper methods are
`Model() (authors.Author, error)`, `Posts() (*orm.RelatedSet[blog.Post], error)`,
`ReviewedPosts() (*orm.RelatedSet[blog.Post], error)`, and `Fresh() (*AuthorsAuthorReverseObject, error)`. Repeated calls
to the same accessor share that wrapper-owned cache; the two edges have independent sets. `Fresh` creates an independent
wrapper and both caches with the captured backend and cloned model.

IR reverse name has no GoName. Accepted generated selectors are ASCII lower-snake identifiers with non-empty segments;
each segment is title-cased and concatenated (`posts -> Posts`, `reviewed_posts -> ReviewedPosts`). Leading/trailing or
repeated underscore, digit-leading segment, non-ASCII/punctuation, a raw whole name or any segment equal to a Go keyword
or `init`, and empty results are rejected before bytes publication. The generator rejects collisions among transformed
reverse selectors, fixed aggregates/functions/versions/import aliases, model surfaces, query-owner method
`ParseDynamic`, owner wrapper methods (`Model`, `Posts`, `ReviewedPosts`, `Fresh`), object factory method `From`, private
receiver/locals and all declarations emitted by immutable prerequisite generators. All locals
and receivers use alias-impossible underscore-prefixed names. Inputs are cloned/normalized/canonically sorted;
permutation produces exact bytes and any failure returns nil bytes.

## Product, false-green와 CI gates

New `conformance/relationreverseproduct/**` is separate from existing relation products so prior generated bytes and
goldens remain unchanged. Its generator candidate and checked-in outputs must match exactly. App packages have direct
app-to-app Imports/Deps exact 0; only the project companion imports both apps.

Required focused gates:

- reverse AST constructor/accessors/clone/equality/mutation isolation and forward/reverse self-edge join-key distinction
- same-snapshot query binding, exact reverse/forward reconstruction, nullable preservation and query/object capability split
- typed/dynamic `Plan.Equal`, precedence, policy-before-value and atomic failure
- missing/zero/typed-nil/copy receiver/owner PK errors before backend I/O
- `RelatedSet` ordered All/Fresh, deep clones, singleflight, owner/waiter cancellation and retry
- SQLite exact reverse SQL/args/root inversion/one-edge reuse, invalid metadata pre-`QueryContext`
- actual accessor `[10,11]` SELECT1/JOIN0 and lookup `[1]` SELECT1/INNER1 with unchanged DB state
- result/db_state/metrics mutation gates and godjcheck no-output on any mismatch
- generator deterministic/golden/permutation/namespace/no-write/external union compile and import graph
- current relation manifest changes only REL-005 `oracle_locked -> passing`; oracle/static/NI/SHA bytes unchanged
- exact inventory repin, normal local integration and one independent final audit
- existing exact 26-job hosted topology with four relation-product coordinates, race, CGO0, vet, no-rewrite,
  actual bounded Linux/386 and exact Darwin/Python gates

로컬 각 lane은 focused normal tests만 필수로 하고 중복 full race/CGO0/vet 반복은 하지 않습니다. Root integration은
한 번의 full normal/format/generate/conformance gate를 실행하며, 무거운 race/CGO0/vet/플랫폼 matrix는 exact
implementation-head GitHub Actions가 소유합니다. 이는 gate 삭제가 아니라 실행 위치 중복 제거입니다.

## 명시적 비목표

- REL-002 relation assignment/write와 unsaved-target save guard
- REL-007/008 reverse delete collector, PROTECT/SET_NULL bulk mutation/transaction
- REL-009/010 eager select-related INNER/LEFT projection와 cache priming
- REL-011 eager reverse path rejection product
- REL-012 reverse prefetch, IN-list AST, batching/grouping/warm cache injection
- reverse manager mutation, public Filter/IN/prefetch API, multi-hop/chained reverse traversal
- OneToOne/ManyToMany, custom target/to_field/composite PK
- DDL/migration/schema IR 변경, PostgreSQL/MySQL/Windows backend 지원 주장
- query Plan/Value와 ORM Manager semantics 변경

## 완료 조건

- [ ] work/ADR/status activation 문서가 exact baseline evidence와 API를 고정한다.
- [ ] activation diff가 독립 API/scope audit P0/P1/P2/P3=0을 통과한다.
- [ ] reverse AST/query/object/codegen/SQLite implementation이 allowed_paths 안에서 완료된다.
- [ ] REL-005 actual observation과 oracle-blind mismatch gates가 통과한다.
- [ ] aggregate가 exact `115 + 5 + 7`, relation 5/12로 재고정된다.
- [ ] local focused lane gates와 one full integration gate가 통과한다.
- [ ] exact implementation-head hosted 26-job matrix가 통과한다.
- [ ] completion docs/ADR transition과 final evidence가 별도 exact head로 검증된다.

## 진행 기록

- [x] 2026-08-11: interrupted laptop state를 재감사해 local/origin/PR head `9ba1d0e...`, clean worktree,
  run `31374150640` 26/26·326/326 success를 확인했습니다.
- [x] 2026-08-11: REL-002/005/007-012 dependency를 재비교하고 three independent design passes를 통해
  REL-005-only를 최소 정직한 next vertical slice로 선택했습니다.
- [x] 2026-08-11: query-only/object capability split, declaration-centric reverse AST, project-only generator와
  bounded RelatedSet ownership을 activation 전에 freeze했습니다.
- [ ] activation documentation audit/commit/push/hosted exact-head evidence
- [ ] runtime/codegen/SQLite/conformance implementation and integration audit
- [ ] implementation/completion/final hosted evidence

## 현재 blocker와 다음 작업

현재 제품 blocker는 코드 결함이 아니라 GDJ-0027 activation diff 자체의 independent audit와 commit/push입니다.
Baseline hosted evidence를 새 EVID에 기록하되 activation 또는 implementation 증거로 재사용하지 않습니다.

다음 작업은 exact합니다.

1. ADR-0027과 activation status/index/evidence 문서를 이 packet과 동기화합니다.
2. Exact activation diff를 independent API/scope audit합니다.
3. Activation commit을 push한 뒤 hosted CI를 기다리지 않고 runtime/codegen/SQLite-conformance lanes를 병렬 착수합니다.
4. 각 lane focused normal gate 후 root에서 한 번 full integration하고 exact implementation head를 push합니다.
5. Hosted exact 26-job matrix에서 heavy race/CGO0/vet/platform 증거를 수집합니다.

## 인수인계

- 현재 worktree의 GDJ-0027 문서는 activation candidate일 뿐 implementation/acceptance 증거가 아닙니다.
- Existing relation product/generator bytes와 oracle/static/NI/SHA를 변경하지 않습니다.
- 공개 API를 바꾸기 전 이 work와 ADR을 먼저 amend하고 independent API audit을 다시 받습니다.
- Draft PR #1은 open/draft로 유지하며 사용자의 명시적 요청 없이 merge하지 않습니다.
