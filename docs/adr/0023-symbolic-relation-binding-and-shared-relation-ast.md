# ADR-0023: Symbolic Relation Binding and Shared Relation AST

- 상태: Proposed
- 날짜: 2026-08-10
- 관련 work/contract:
  [GDJ-0023](../../work/0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md),
  REL-001..REL-012, Q-013
- 선행 결정: [ADR-0001](0001-schema-ir-as-canonical-source.md),
  [ADR-0002](0002-codegen-generics-runtime-metadata.md),
  [ADR-0003](0003-typed-and-dynamic-query-apis.md),
  [ADR-0005](0005-contract-first-vertical-slices.md),
  [ADR-0006](0006-codegen-input-package-boundary.md),
  [ADR-0007](0007-m1-model-runtime-and-dynamic-query-boundaries.md),
  [ADR-0012](0012-queryset-evaluation-cache-ownership.md)
- 대체하는 ADR: 없음

## 상태와 범위

이 ADR은 아직 **Proposed**입니다. REL-001..012 reference contract와
`conformance/relationbinding/**` test-only feasibility가 모두 통과하기 전에는 public API,
generated source ABI 또는 제품 구현 결정으로 사용하지 않습니다.

제안하는 핵심 방향은 다음 네 가지입니다.

1. Cross-app relation target은 target Go type/import가 아니라 canonical symbolic model identity로
   Schema IR에 남깁니다.
2. 모든 app descriptor가 준비된 뒤 project-level binder가 forward/reverse relation을 원자적으로
   resolve합니다.
3. Generated typed relation path와 runtime dynamic lookup은 같은 immutable relation-aware Query AST로
   수렴합니다.
4. Current Schema IR v2를 재해석하지 않고 relation 의미는 explicit vNext에서만 표현합니다.

Exact public type/function 이름, IR vNext relation arm layout과 generated project bridge 형태는 spike
결과 뒤 이 ADR을 갱신하고 Accepted할 때 확정합니다.

## 맥락

현재 GoDj는 scalar model/field, model-specific generated FieldSet, generic Manager/QuerySet, runtime
descriptor와 single-table Query AST/SQLite compiler를 갖습니다. `auto`, `char`, `boolean`만 가진
Schema IR v2에는 relation target, reverse path, cardinality, delete policy와 eager-loading 의미가
없습니다.

M3의 첫 관계 단면은 다음 요구를 동시에 만족해야 합니다.

- `blog.Post.author → authors.Author` 같은 cross-app absolute target
- Forward field와 reverse `posts` path의 한 source of truth
- 서로 참조하는 app도 Go import cycle 없이 생성/compile
- 일반 application의 typed relation selector와 Admin/동적 filter의 string path
- Non-null/nullable FK의 join type, forward cache와 reverse prefetch plan
- Missing target/reverse collision을 실행 중 SQL error가 아니라 binding/generation 단계에서 거부
- Schema IR, codegen, runtime metadata, Query AST와 backend compiler의 기존 단방향 pipeline 유지

Django는 app registry와 Python model class의 lazy relationship resolution을 사용합니다. GoDj는 Python
class/import object graph를 복제하지 않으면서 같은 외부 동작을 Go의 package/type system에 맞게
재설계해야 합니다.

Cross-app generated package가 target package의 concrete model type을 직접 field/accessor signature에
넣으면 `authors ↔ blog` 같은 상호 relation에서 import cycle이 생깁니다. 반대로 모든 relation을
문자열/runtime reflection으로만 처리하면 typed API와 compile-time evidence를 잃고 잘못된 target이
query 실행까지 늦게 발견될 수 있습니다.

## 결정 기준

- REL-001..012의 metadata/result/error/DB state/query·join·mutation 의미를 구현할 수 있음
- Schema IR이 source target, physical FK column, nullability, reverse name와 delete policy의 canonical
  single source임
- Cross-app mutual/self relation에서 generated app-to-app import edge 0과 external module compile 성공
- Typed와 dynamic relation path가 동일한 immutable AST와 compiler input을 만듦
- Missing target, duplicate model identity와 reverse collision이 partial publication 없이 fail-closed
- Relation binding 이후 descriptor/AST가 immutable하고 concurrent read/evaluation에 안전함
- Current Schema IR v2, migration definition tuple `(1,1,1,2)`와 existing generated output을 조용히
  재해석하지 않음
- Candidate generation/compile 실패 때 ADR-0006의 last-good output byte identity 보존
- Runtime global registration, init/import order와 reflection hot path에 의존하지 않음
- Backend 차이는 capability/compiler/schema editor 경계에 남고 SQLite 의미를 PostgreSQL로 일반화하지
  않음
- Contract/reference proof와 제품 implementation/evidence를 분리할 수 있음

## 고려한 선택지

### Generated app package가 target app package를 직접 import

Forward accessor가 concrete target type을 바로 반환할 수 있어 사용 표면은 단순합니다. 그러나 app A와
B가 서로를 참조하면 Go import cycle이 되고 self/cross-app target을 generator가 서로 다른 규칙으로
처리해야 합니다. Build tag나 interface 추출로 cycle을 우회하면 사용자 package graph가 relation 방향에
따라 달라집니다. 기본 경계로 채택하지 않습니다.

### 모든 relation을 runtime string/reflection registry로 resolve

Django식 lazy lookup과 reverse path를 쉽게 만들 수 있지만 invalid target/reverse conflict가 startup 또는
query 시점까지 늦어집니다. Typed selector도 내부에서 string을 다시 parse하게 되어 typed/dynamic AST
equivalence가 evidence가 아니라 관례가 됩니다. Mutable global `init()` registry는 import order,
concurrent test isolation과 duplicate registration 의미도 만듭니다. 채택하지 않습니다.

### 모든 app model을 하나의 generated package로 합침

Concrete types 사이 import cycle은 사라지지만 app package 경계와 독립 regeneration이 무너집니다.
하나의 app 변경이 전체 project generated output을 다시 만들고 사용자 메서드/import ownership도
모놀리식 target에 결속됩니다. ADR-0006의 declaration/generated target 분리 위에 불필요한 project-wide
compile blast radius를 추가하므로 채택하지 않습니다.

### Scalar FK ID만 생성하고 relation은 전부 수동 사용자 코드로 둠

Database column은 표현할 수 있지만 reverse metadata, dynamic lookup, eager loading, delete policy와
migration/schema 의미의 source of truth가 여러 곳으로 갈라집니다. REL-001..012를 제품 framework
동작으로 구현할 수 없으므로 채택하지 않습니다.

### Symbolic target + atomic project binder + shared relation AST

Per-app generated package는 target Go package를 import하지 않고 scalar identity와 symbolic metadata를
보존합니다. Project-level generated/bound bridge가 모든 descriptor를 모은 뒤 typed path/loader를
조합하고, dynamic lookup도 같은 bound metadata에서 같은 AST를 만듭니다. 추가 project binding step과
generated bridge가 필요하지만 import graph, early validation과 typed/dynamic convergence를 함께 검증할
수 있습니다. Feasibility 대상으로 채택합니다.

## 제안 결정

### Canonical symbolic relation identity

Relation target identity는 최소 `(app_label, model_name)`의 canonical value입니다. Go import path,
generated package pointer, runtime address나 user display name을 identity로 사용하지 않습니다.
GDJ-0023 fixture와 첫 제품 후보는 target model의 canonical AutoField primary key만 사용합니다.
Non-PK/custom primary key와 explicit `to_field`는 이 identity에 암묵적으로 넣지 않고 후속 contract로
남깁니다. Source relation declaration 자체는 source model/field identity가 소유합니다.

Forward relation declaration은 source model/field identity에 결속되고 최소 다음 의미를 Schema IR vNext에
lossless하게 가져야 합니다.

- Target symbolic model identity
- Physical FK column
- Nullability
- Forward field name
- Explicit reverse name 또는 explicitly disabled reverse
- Cardinality (`many_to_one`; reverse projection은 `one_to_many`)
- Delete policy (`PROTECT`, `SET_NULL`은 첫 product subset 후보)

Target key는 normalization 때 문법/canonical form을 검증하지만 target 존재 여부는 하나의 app schema만
보는 local normalization에서 추측하지 않습니다. Project binding이 모든 app descriptor를 받은 뒤
존재와 cross-app conflict를 검증합니다.

`"authors.Author"` 같은 source spelling은 DSL input일 수 있으나 canonical IR/output identity는
normalized app/model key입니다. 같은 target의 different spelling, Go type name 또는 import alias가 다른
relation identity를 만들지 않습니다.

### Schema IR vNext와 v2 불변

Schema IR v2에는 relation meaning이 없으므로 v2의 reserved/zero field, side map 또는 runtime-only metadata로
relation을 추가하지 않습니다. Relation-capable schema는 explicit format version vNext를 사용하고 old v2
decoder/consumer는 unknown version으로 fail-closed해야 합니다.

Feasibility는 두 concrete layout을 비교합니다.

1. Existing field union의 explicit relation arm
2. Scalar storage field와 별도 ordered relation declaration

선택 기준은 canonical serialization/hash, declared ordering, duplicate field/column/reverse diagnostics,
codegen consumption과 historical state 확장 가능성입니다. Exact version number와 wire shape는 proof 뒤
결정합니다. 후보가 검증되지 않으면 이 ADR은 Proposed로 남습니다.

Existing migration definition tuple `(definition format 1, loader ABI 1, operation codec 1,
Schema IR 2)`는 변경하지 않습니다. Operation codec v1의 `CreateModel`/`AddField`에 relation-bearing vNext
IR을 넣지 않습니다. Relation migration source/upgrade는 별도 compatibility contract와 version decision을
요구합니다.

### Project-level atomic binder

Binding은 app별 descriptor를 순서대로 mutate하는 registry가 아니라 다음 all-or-nothing 단계입니다.

```text
all normalized app/model descriptors snapshot
→ validate globally unique model keys
→ validate source relation identities/columns
→ resolve every target key
→ construct forward and reverse candidates
→ validate reverse namespace/cardinality conflicts
→ publish one immutable binding set
```

어느 단계에서든 실패하면 forward/reverse handle을 하나도 publish하지 않습니다. Error order는 canonical
model/relation key 순으로 deterministic해야 합니다. Missing target, duplicate model key, duplicate relation
identity와 reverse-name collision은 structured candidate error가 됩니다. Exact public category/code는
GDJ-0024 전에 별도 contract로 고정합니다.

Binder input은 caller mutation과 alias하지 않는 snapshot이어야 하고 successful binding output은 immutable
value로 취급합니다. Runtime global mutable registry와 `init()` registration은 사용하지 않습니다.

### Generated package와 project bridge ownership

Per-app generated target은 자기 model의 scalar FK storage와 source-side descriptor metadata를 가질 수 있지만
target app generated package를 import하지 않습니다. Target concrete type이 필요한 typed relation selector와
loader 후보는 별도 project binding/bridge package가 두 app package를 함께 import해 구성합니다.

이 bridge는 Schema IR을 재해석하지 않고 binder가 검증한 identity와 generated descriptors를 연결합니다.
Generated app package, bridge와 runtime metadata가 각자 relation definition을 복제하지 않습니다. Candidate
compile 실패, unresolved target 또는 reverse collision에서 new output을 publish하지 않고 last-good
generated bytes를 보존합니다.

Exact generated file split, package name, exported accessor와 generic type signature는 test-only spike 이름을
그대로 채택하지 않고 GDJ-0024에서 결정합니다.

### Runtime metadata와 reverse relation

Successful binding은 forward와 reverse metadata를 같은 logical edge의 두 projection으로 만듭니다.
REL-001 fixture에서 다음 의미가 한 edge source로부터 나와야 합니다.

```text
blog.post.author
  forward: column=author_id, target=authors.author, nullable=false, reverse=posts
  reverse: authors.author.posts, target=blog.post, one_to_many=true
```

Forward/reverse metadata를 별도 수동 선언으로 만들지 않습니다. Reverse name이 disabled가 아니라면 target
namespace collision을 binding 전에 검사합니다. Runtime dynamic lookup은 이 immutable bound metadata만
탐색하며 raw Schema DSL 또는 generated Go AST를 읽지 않습니다.

### Typed/dynamic shared relation AST

ADR-0003의 typed/dynamic convergence를 relation path에도 적용합니다.

```text
generated typed selector ─┐
                          ├→ bound relation-path validation → immutable shared Query AST
dynamic string lookup  ───┘                                  → backend compiler/loader plan
```

Shared relation path는 ordered hop, source/target descriptor identity, direction, cardinality, nullability와
lookup meaning을 담습니다. Raw SQL, Django path string, Go package/type pointer 또는 loader cache object는
AST identity가 아닙니다.

Equivalent typed/dynamic `author.name`, `author.id`, reverse `posts.title`는 canonical deep equality와
byte-deterministic plan으로 검증합니다. 동일 relation에 여러 predicate가 있으면 compiler input의 relation
edge를 공유할 수 있어야 합니다. Join 종류는 nullability만으로 고정하지 않고 lookup/eager operation
context와 edge nullability가 함께 결정합니다. Target-value predicate는 nullable edge에서도 `INNER`로
좁혀질 수 있고, `isnull`은 join을 trim할 수 있으며, non-null/nullable `select_related`는 각각
`INNER`/`LEFT OUTER`입니다.

Dynamic unknown target/field/lookup, multi-valued reverse path를 `select_related`에 사용하는 오류는 SQL compile
또는 DB I/O 전에 반환합니다. Typed API의 exact error-bearing builder/selector signature는 결정하지
않습니다.

### Lazy forward cache와 eager loader plan

Relation object cache는 immutable Query AST 자체가 아니라 evaluated model/result instance가 소유해야
합니다. 같은 freshly-loaded instance의 forward relation 첫 access는 lazy load할 수 있고 두 번째 access는
cache를 사용합니다. QuerySet value copy 사이에 relation object cache를 암묵적으로 공유한다고 가정하지
않습니다.

`select_related`는 main query의 single-valued forward relation join plan이고 reverse one-to-many는 허용하지
않습니다. `prefetch_related` reverse FK는 primary result와 target-key batch query를 분리하고 evaluation
result에 cache를 attach합니다. Related manager에서 추가 filter/order가 들어오면 cache 소비 의미가 달라질
수 있으므로 REL-012는 exact `.posts.all()` 소비만 잠급니다.

Exact cache container, pointer alias, concurrent waiter/cancellation ownership과 nested eager loading은
GDJ-0024 또는 후속 contract가 결정합니다.

### Delete policy와 backend 경계

Delete policy는 relation IR/metadata 의미이지만 transaction execution은 write/orchestration과 backend가
소유합니다. `PROTECT`는 target/source DB state mutation 0으로 실패해야 합니다. `SET_NULL`은 source FK
update와 target delete를 한 product transaction에서 실행하고 중간 실패 시 둘 다 rollback해야 합니다.

Django collector의 private SELECT choreography나 raw SQL 문자열을 복제하지 않습니다. Stable external
meaning은 protected source rows, mutation 0 또는 `[UPDATE, DELETE]` order/affected row, transaction count와
final DB state입니다. Actual SQLite FK constraint/orphan rejection을 검증하되 이 의미를 PostgreSQL/MySQL
지원으로 일반화하지 않습니다.

## Contract/reference와 제품 경계

GDJ-0023은 REL-001..012 manifest/oracle/static fixture와 test-only binding proof만 만듭니다. Relation
manifest status는 `oracle_locked`이고 `conformance/runners/godj/**` actual adapter가 없습니다.

완료 시 reference aggregate는 12 set/127 contract, 132 ordered cross-binding입니다. 제품 aggregate는
기존 11 adapter/115 contract와 `110 passing + 5 deviation`으로 그대로입니다. 따라서 상태는
`110 passing + 5 deviation + 12 oracle_locked`이며 relation product support를 주장하지 않습니다.

ADR이 Accepted된 뒤 GDJ-0024와 후속 bounded product packets가 각각 선언한 exact
Schema IR/codegen/metadata/AST/compiler/loader/write subset을 구현하고 independent actual adapter를
연결한 contract만 red/passing 또는 reviewed deviation으로 전환할 수 있습니다. 아직 구현하지 않은
REL contract는 `oracle_locked`를 유지합니다.

## 결과

제안이 채택되면 cross-app relation은 Go import cycle 없이 canonical Schema IR에서 runtime/query까지 한
identity를 유지할 수 있습니다. Typed/dynamic API가 별도 join compiler를 만들지 않고 same AST를 사용하며,
reverse metadata와 eager plan도 forward declaration에서 파생됩니다.

대신 project-wide binding/generation 단계와 bridge package가 추가되고, 하나의 app schema만으로는 target
existence를 완전히 validate할 수 없습니다. Schema IR version, generator와 migration codec upgrade 책임도
명시적으로 늘어납니다. Central binding 실패는 product build/generation을 막으므로 deterministic
diagnostic과 last-good preservation이 필수입니다.

## 의도적으로 결정하지 않은 것

- Schema IR vNext exact version number, field-union versus separate relation-list wire shape
- DSL `ForeignKey` constructor syntax와 symbolic target source spelling
- Generated project bridge package/path, exported types와 accessor/selector signature
- FK scalar Go type, nullable representation과 target primary-key type 제약
- Relation object cache concrete type, concurrent access/cancellation과 transaction/session ownership
- Multi-hop path, nested `select_related`, custom `Prefetch`, filtered relation와 aggregation
- OneToOne, many-to-many, self-symmetrical relation와 inheritance
- `CASCADE`, `RESTRICT`, `DO_NOTHING`, `SET_DEFAULT`와 database cascade 선택
- Relation migration operation codec, autodetector/writer, historical model과 schema editor
- Public structured error taxonomy와 message/detail exposure
- PostgreSQL/MySQL compiler, deferrable constraint와 multi-DB router
- Generated ABI/semver와 old v2 project upgrade tool

## Feasibility와 검증

ADR을 Accepted로 바꾸기 전에 `conformance/relationbinding/**`에서 다음을 모두 통과해야 합니다.

- Two app mutual relation과 self relation external module `go list`/`go test`
- Generated app-to-app import edge 0과 project bridge의 acyclic dependency graph
- Canonical symbolic key normalize/serialize/hash와 hash/map order independence
- Missing target, duplicate model identity, invalid source relation, reverse collision의 deterministic error
- 어느 binding failure에서도 partial forward/reverse publish 0
- Candidate compile/binding failure에서 last-good generated bytes 불변
- Equivalent typed/dynamic forward/reverse relation input의 canonical shared AST byte equality
- Unknown/invalid/multi-valued select-related path의 compiler/DB I/O 전 failure
- REL-004 target predicate `INNER`와 duplicate predicate join reuse, REL-006 `isnull` join 0,
  REL-009 non-null eager `INNER`, REL-010 nullable eager `LEFT OUTER`의 distinct operation-context semantics
- Reverse prefetch primary/batch plan 분리와 key total ordering
- IR v2 relation acceptance 0, vNext candidate deterministic round-trip/hash와 old consumer rejection
- Relation-bearing payload가 existing migration tuple/codec v1에서 거부됨
- SET_NULL update 뒤 target delete injected failure의 full transaction rollback
- Normal/race/CGO-disabled/vet/repetition과 four hosted OS/architecture proof
- Product package diff 0, relation actual adapter 0, existing 11 product aggregate/artifact 불변
- Independent architecture/import/false-green audit P0/P1/P2/P3 finding 0

## Hosted 검증

Existing exact 18 required executions을 보존하고 relation proof를 다음 네 official runner coordinate에서
추가해 exact 22로 운영하는 안을 제안합니다.

- `ubuntu-22.04` amd64
- `ubuntu-24.04-arm` arm64
- `macos-15-intel` amd64
- `macos-26` arm64

Routine local Python은 CPython 3.14.3와 uv 0.12.3 하나만 사용합니다. 기존 Python compatibility matrix는
uv 0.12.3으로 exact `3.12.13`, `3.13.15`, `3.14.3`, `3.14.7`을 검증합니다. Existing exact locked
profile/job과 its historical lock identity는 relation proof/matrix job이 다시 쓰지 않습니다.

PostgreSQL/MySQL job은 실제 relation backend/compiler/schema/write/transaction adapter와 reusable REL
corpus가 생긴 뒤 추가합니다. Service container health만 보는 job은 이 ADR의 acceptance evidence가
아닙니다.

## 채택/기각 규칙

- 모든 reference/binding/IR/import/rollback gate가 통과하고 exact-head 22/22 hosted evidence가 있으면
  concrete IR layout과 project bridge 결론을 이 ADR에 반영한 뒤 Accepted로 승격합니다.
- Symbolic identity는 가능하지만 proposed bridge/IR layout이 실패하면 성공한 사실만 기록하고 대안을
  비교한 채 Proposed를 유지합니다.
- App-to-app import, partial publication, typed/dynamic AST divergence, v2 silent reinterpretation 또는
  last-good drift가 하나라도 남으면 현재 제안을 채택하지 않습니다.
- Contract oracle만 잠겼다는 이유로 ADR을 Accepted하거나 GDJ-0024 제품 작업을 완료로 표현하지 않습니다.
