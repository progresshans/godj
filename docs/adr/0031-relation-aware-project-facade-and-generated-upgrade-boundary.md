# ADR-0031: Relation-aware Project Facade and Generated Upgrade Boundary

- 상태: Accepted
- 날짜: 2026-08-12
- 관련 work/contract:
  [GDJ-0031](../../work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md), Q-013, Q-017
- 선행 결정: [ADR-0023](0023-symbolic-relation-binding-and-shared-relation-ast.md),
  [ADR-0026](0026-forward-foreign-key-object-cache-and-nullability.md),
  [ADR-0029](0029-one-hop-forward-select-related.md),
  [ADR-0030](0030-project-bound-protect-and-set-null-delete.md)
- 대체하는 ADR: 없음

## 상태와 범위

이 ADR은 **test-only compile feasibility 방법과 그 false-green 경계에 한해서 Accepted**입니다. Production
facade나 generator를 채택하지 않고, 현재 exact relation-delete fixture를 한 byte도 바꾸지 않는 overlay 기반
external compile-usability 검증 방법만 승인합니다.

Activation head `624347e15e6d6e6b6981fe14b75974226f72f9df`와 implementation head
`065390275ee7b69e224eeaeda57e4731321d7a44`는 각각 EVID-064와 EVID-065의 별도 exact-head hosted gate를
통과했습니다. Physical exact 16, generated exact 13과 logical exact 17 inventory, no-overlay failure, positive와
adversarial compile/source gates가 검증됐습니다.

Current product는 unchanged exact `121 passing + 5 deviation + 1 oracle_locked`, relation 11/12이고 REL-002만
locked입니다. 이 Accepted 결정은 `project.Using`, `Models`, `BlogPosts`, `Related`, `First` tuple, `Model` unwrap
또는 selector 이름을 canonical public API로 채택하지 않으며 production generated upgrade 정책도 결정하지 않습니다.

## 맥락

GoDj는 app별 raw model과 import-cycle-free project bridge, forward relation object, reverse/prefetch, eager selection과
relation delete를 bounded slice로 검증했습니다. 하지만 현재 application code가 관계마다 `BindObjects`와 factory
`From`을 직접 조립해야 하고, 저수준 surface들이 서로 다른 generated companion에 나뉩니다.

Django의 `post.author`는 descriptor와 runtime registry를 사용하지만 Go는 field access를 가로채 I/O와 error를
실행할 수 없습니다. GoDj의 목표는 Django 내부 구현 복제가 아니라 명시적 backend/session binding 뒤
`Author(ctx)`처럼 I/O와 error가 보이는 Go API입니다.

그렇다고 현재 `Bind*`, `From` 또는 All-only eager bridge를 곧바로 canonical API로 승격하면 manager 이름,
result wrapper, scalar access, generated upgrade와 REL-002 cache ownership을 검증하기 전에 공개 표면을 동결하게
됩니다. 먼저 external package에서 candidate syntax가 가능한지 test-only로 확인해야 합니다.

## 결정 기준

- Physical generated/fixture bytes와 current product behavior를 전혀 바꾸지 않음
- Backend/session을 global default 없이 명시적으로 전달
- Exact Go multiple return과 immutable query chain이 자연스럽게 컴파일됨
- Plain/lazy/eager source 결과가 같은 pointer type과 accessor를 사용
- Private wrapper field를 우회하지 않고 scalar 접근 경계를 명시
- Import cycle과 generated project package namespace를 보존
- Compile-only 증거와 runtime/product support 주장을 분리
- Existing project를 위한 future generated upgrade/collision 정책을 성급히 확정하지 않음
- Missing kernel을 다른 fixture나 source/oracle read로 위조하지 않음

## 고려한 선택지

### 현재 저수준 bridge를 canonical API로 선언

추가 구현 없이도 타입 안전성과 cache kernel을 재사용할 수 있지만 사용자가 매 relation마다 binder/factory를
조립해야 합니다. Eager query도 current source query를 별도로 전달해야 하므로 목표 application UX와 다릅니다.
저수준 surface는 계속 public building block일 수 있으나 그 존재만으로 canonical로 선언하지 않습니다.

### Production facade generator를 바로 추가

실제 generated upgrade를 검증할 수 있지만 exact name, scalar access, target wrapper, reverse와 cache ownership이
미결정인 상태에서 새 공개 API와 version을 먼저 만들어 버립니다. 이 단계에서는 거부합니다.

### 다른 relation product fixture를 합쳐 full chaining을 시뮬레이션

Current exact 16에는 reverse aggregate가 없습니다. 다른 conformance fixture를 import하거나 test-only reverse를
합성하면 실제 prerequisite를 숨기는 false green입니다. 이번 spike에서 거부합니다.

### Existing fixture 위 one-file Go overlay

Physical bytes와 production import graph를 보존하면서 same-package candidate가 private generated kernel을 조립할 수
있습니다. Overlay가 없을 때 candidate consumer가 실패하는 것도 함께 검증할 수 있어 feasibility와 product support를
분리합니다. 이 ADR이 제안하는 방식입니다.

## 제안

### Frozen physical exact 16

`conformance/relationdeleteproduct/**`의 13 generated files와 `fixture/schema.go`, `observer.go`, `product_test.go`
exact 16 files를 baseline으로 사용합니다. Fixture-relative sorted `path + NUL + decimal size + NUL + content`은
62,538 bytes/SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`입니다.
그중 generated exact 13은 26,140 bytes/SHA-256
`a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`입니다.

### Test-only logical exact 17

`internal/compiletest`만 수정합니다. Testdata의 project-package source 하나를 nonexistent physical `.go` target에
Go overlay로 매핑합니다. Logical compile view는 exact 17이지만 repository에는 16-file fixture만 남습니다.
Codegen entrypoint, version constant, golden이나 generated output은 추가하지 않습니다.

### Forward read-only candidate

Positive external consumer는 다음만 검증합니다.

- one-time candidate `project.Using(backend)`
- exact `post, found, err := models.BlogPosts.OrderBy(...).First(ctx)`
- `Filter`/`OrderBy`/`Limit` 뒤 source facade query type 유지
- lazy와 one-hop eager의 동일 source pointer type과 `Author(ctx)` accessor
- private wrapper scalar의 explicit `Model()` unwrap
- `db.RelationSession`을 `db.Queryer` candidate에 callback 내부에서 전달할 수 있는 structural assignability

`RelationSession` compile은 callback 이후 lifetime, query pinning 또는 broader facade capability 선택을 보증하지
않습니다. Exact 16에 없는 reverse, target relation-aware wrapper와 cache mutation은 검증하지 않습니다.

### AST/source whitelist와 no-inventory-growth

Overlay/consumer를 AST로 검사해 allowed public imports와 forward read-only symbols만 허용합니다. Other relation
fixtures, oracle/static/not-implemented/runner/protocol source, reflection/unsafe/process/file/network I/O와 reverse/
write/delete/cache/JSON/custom-method symbol을 거부합니다.

새 top-level `Test*`나 `t.Run`을 만들지 않고 existing compile tests에 case/helper를 결합해 test inventory와 product
inventory가 늘지 않게 합니다.

## 의도적으로 결정하지 않은 것

- Exact exported facade/manager/query/selector/type names
- `First`/`Get` not-found API와 `Limit` ergonomics
- target relation-aware wrapper와 reverse manager/chaining
- scalar promotion, JSON, custom methods, copy/clone
- REL-002 assignment/cache invalidation과 loaded state ownership
- write/delete facade와 capability interface
- transaction callback 이후 wrapper/session lifetime
- generated version, semver/deprecation, collision과 upgrade command

## 결과

Physical exact 16을 보존한 one-file overlay와 external consumer로 forward read-only 후보가 Go type/package
경계에서 컴파일 가능하다는 feasibility를 확인했습니다. 같은 consumer가 overlay 없이는 exact
`undefined: project.Using` 진단으로 실패하고, wrong-model 및 forbidden source mutation도 거부되므로 production
surface 부재를 숨기지 않습니다.

이 결과로 승인한 것은 test-only 검증 방법과 “제품 API가 아님”이라는 경계뿐입니다. Compile success는 production
support, runtime/cache/query-count 의미 또는 exact public name의 채택이 아닙니다. Candidate 이름과 generated
upgrade/collision/deprecation 정책은 Q-017 P1/open에 남습니다.

REL-002는 wrapper identity와 scalar mutation 경계가 좁혀진 뒤 별도 work/ADR에서 다룹니다. Reverse/full chaining도
필요한 generated aggregate를 명시한 별도 packet이어야 합니다.

## 검증

- Physical exact 16 count/bytes/digest와 exact 13 generated subset digest
- Exactly one overlay mapping과 logical exact 17 inventory
- External positive candidate compile, no-overlay failure와 typed negative compile
- Exact `OrderBy(...).First(ctx)` multi-assignment와 lazy/eager source pointer equality
- Explicit `Model()` unwrap과 callback-local `RelationSession` assignability
- AST/source/import whitelist와 other fixture/oracle/static read 금지
- No new top-level Test/t.Run, no physical overlay residue와 clean worktree
- normal/race/CGO0/vet/root CI와 separate exact-head evidence

위 feasibility 검증은 EVID-064/EVID-065로 완료됐습니다. Production facade/generator acceptance에는 별도 work와
명시적 API-freeze 결정이 필요합니다. EVID-065 뒤 작성되는 completion-documentation tree 자체 exact-head CI는
`not run/pending`이며 activation/implementation run을 그 later tree의 proof로 재사용하지 않습니다.
