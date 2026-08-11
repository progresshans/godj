---
id: GDJ-0032
status: active
updated: 2026-08-12
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "3d6612512e8887de8868a319650d54ad0721471b"
depends_on: ["GDJ-0031"]
contracts: ["Q-013", "Q-017"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "codegen/project_relation_facade.go"
  - "codegen/project_relation_facade_test.go"
  - "codegen/testdata/relation_facade/project.golden"
  - "codegen/project_relation_object.go"
  - "codegen/project_relation_object_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/README.md"
  - "conformance/relationdeleteproduct/product_test.go"
  - "conformance/relationdeleteproduct/project/zz_godj_relation_facade.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_facade/external_consumer.go.txt"
  - "internal/compiletest/testdata/relation_facade/project_facade_spike.go.txt"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0032-production-forward-project-facade-and-additive-first-publication.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md"
  - "work/0032-production-forward-project-facade-and-additive-first-publication.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Production Forward Project Facade and Additive First Publication

## 사용자에게 보이는 결과

이 work는 GDJ-0031에서 test-only overlay로만 확인한 forward relation facade를 project package의 실제 generated
companion 한 파일로 처음 게시하는 bounded product packet입니다. 목표 경험은 backend/session을 project에 한 번
연결한 뒤 query root가 반환한 project-owned pointer wrapper에서 required/nullable forward relation을 같은 API로
읽는 것입니다.

Activation 뒤 Gate 0에서 아래 exact public surface를 구현 기준으로 고정했습니다. ADR-0032 전체는 hosted
implementation proof 전까지 Proposed이지만, source lane은 이 이름과 signature를 임의로 바꾸지 않습니다.

```go
models, err := project.Using(backend)
post, found, err := models.BlogPost.First(ctx)
author, err := post.Author(ctx)
reviewer, present, err := post.Reviewer(ctx)
```

Eager query도 별도 결과 타입을 노출하지 않고 같은 source wrapper와 accessor를 사용합니다.

```go
posts, err := models.BlogPost.
    SelectRelated(models.BlogPost.Related.Author).
    All(ctx)

author, err = posts[0].Author(ctx) // warm cache, 추가 query 0회
```

정본 implementation surface는 `Backend`, `Using(Backend) (Models, error)`, singular aggregate fields
`AuthorsAuthor`/`BlogPost`, wrappers `AuthorsAuthor`/`BlogPost`, queries `AuthorsAuthorQuery`/`BlogPostQuery`,
`BlogPostRelationSelector(s)`, `BlogPostEagerQuery`, `Unwrap`, ordered `First(ctx) (*Wrapper, bool, error)`와
`Limit(int) (Query, error)`입니다.

## 목표

- Existing generated exact 13 files, 26,140 bytes와 SHA-256
  `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`를 byte-for-byte 보존합니다.
- Project-only `zz_godj_relation_facade.go` companion 하나를 additive first-publication으로 생성·게시합니다.
- Project-local backend capability는 `db.Queryer + db.Mutator`의 structural composite이며 `db.RelationAtomic`을
  요구하지 않습니다.
- 모든 declared model에 project-owned pointer wrapper와 all-model query root를 생성합니다.
- Query root와 forward accessor가 대상 모델에 대해 같은 project wrapper 타입을 반환합니다.
- Required `Author`와 nullable `Reviewer`가 raw app model이나 low-level `Object`가 아니라 동일한 Author target
  project wrapper type을 반환하도록 합니다.
- Model별 common forward selector와 eager query wrapper가 선택 edge와 concrete low-level eager evaluation state를
  보존하도록 합니다.
- Existing low-level bind/query/object/select-related kernel을 위임해 typed predicate/order/limit, lazy cache와
  one-hop eager cache를 그대로 사용합니다.
- Generator determinism, namespace collision, first publication, same-file replacement failure와 last-good 보존을
  검증합니다.
- Test-only overlay를 제거하고 no-overlay external consumer와 actual product compile/runtime gate로 바꿉니다.

## 비목표

- Reverse manager, reverse chaining, multi-hop 또는 multiple-edge eager query를 구현하지 않습니다.
- 별도 materialization이나 반복 relation 접근에서 생성된 target wrapper의 stable pointer identity를 보장하지
  않습니다.
- Target wrapper의 downstream relation cache나 identity-map 의미를 정하지 않습니다.
- REL-002 FK assignment/save/cache invalidation, setter, unsaved-related-target 정책을 구현하지 않습니다.
- Write/delete facade, recursive/CASCADE delete, DDL, migration codec 또는 non-SQLite backend를 추가하지 않습니다.
- `godj generate` CLI, coordinated multi-file upgrade/rename/deprecation/repair 또는 일반 generated upgrade를
  구현하지 않습니다.
- Wrapper JSON, scalar promotion, 사용자 model method 또는 callback 이후 session expiry의 deterministic failure를
  보장하지 않습니다.
- Existing 13 generated files, `conformance/relationdeleteproduct/observer.go`, relation manifest/oracle/static fixture,
  `db/**`, `orm/**`, `query/**`, `go.mod`, `go.sum` 또는 `Makefile`을 변경하지 않습니다.

## 고정 설계 경계

### Project-local capability

Facade origin은 query와 향후 mutation-capable project surface를 같은 backend/session에 결합하기 위해
`db.Queryer`와 `db.Mutator`를 모두 요구합니다. `db.RelationAtomic`은 이 read-only forward packet의 입력 계약이
아닙니다.

- Concrete backend와 callback-local session이 composite를 만족하면 positive입니다.
- 정적 타입이 `db.Queryer`뿐인 값은 concrete dynamic value가 Mutator여도 compile-time negative입니다.
- Queryer+Mutator만 구현한 최소 fake와 `db.Session`은 `db.RelationAtomic`/`db.RelationMutator` 없이도 positive여야
  합니다. 이 두 추가 capability를 우연히 요구하는 구현은 거부합니다.
- Construction 순서는 `BindObjects` 성공 확인 뒤 backend nil-like 검증입니다. Binder failure와 nil/typed-nil
  backend가 동시에 있으면 original binder error/cause가 우선하고, binding이 valid할 때 nil/typed-nil backend는
  `backend_error/invalid_plan`입니다. 두 경로 모두 backend I/O 0입니다.
- Invalid input의 category/code는 stable하지만 detail message는 contract가 아닙니다.
- Origin은 wrapper/query가 공유하지만 global default backend는 만들지 않습니다.

### 모든 모델의 project wrapper

모든 declared model은 raw app model 및 기존 low-level `*...Object`와 구별되는 project-owned pointer wrapper를
가집니다. `authors.Author`와 `blog.Post` 모두 포함합니다.

- Target model의 all-model query root와 모든 해당 forward accessor는 같은 target wrapper type을 반환합니다.
  현재 Author와 Reviewer는 둘 다 Author query root가 반환하는 project wrapper type을 사용합니다.
- Required edge의 모양은 `(*TargetWrapper, error)`입니다.
- Nullable edge의 모양은 `(*TargetWrapper, bool, error)`이고 SQL `NULL`은 `nil, false, nil`입니다.
- Raw model 접근은 exact `Unwrap` clone method를 통해서만 제공합니다.
- Existing `BlogPostObject` 같은 low-level `Object` namespace와 새 application wrapper namespace는 겹치거나 alias가
  되어서는 안 됩니다.
- Nil/zero/dereference-copy wrapper와 zero Models/query root는 `query_error/invalid_plan`으로 backend I/O 전에
  거부합니다. Detail message는 contract가 아닙니다.

Target wrapper가 필요한 것과 stable wrapper identity를 보장하는 것은 별개입니다. 동일 raw target을 별도로
materialize한 결과나 반복 accessor 결과에 pointer equality를 요구하지 않습니다.

### Common selector와 eager state

Author 전용 special case를 만들지 않습니다. `Author`와 `Reviewer`가 같은 model-specific sealed selector 표현과
eager query wrapper를 사용해야 합니다.

- Query 하나는 정확히 하나의 direct one-hop forward edge만 선택합니다.
- Filter/OrderBy/Limit 뒤에도 선택 edge와 facade query type을 잃지 않습니다.
- Author와 Reviewer 모두 `SelectRelated`를 Filter/OrderBy/Limit 전후 어느 위치에서 호출해도 같은 선택 edge를
  보존합니다. Required present, nullable present와 nullable NULL을 각각 검증합니다.
- Eager wrapper는 `All` 때마다 low-level eager query를 다시 만들지 않고 선택된 concrete eager query/evaluation
  state를 저장합니다.
- 같은 eager query의 value copy와 반복 `All`은 하나의 evaluation state를 공유하고, Filter/OrderBy/Limit으로 만든
  derived chain은 독립 evaluation state를 가집니다.
- Eager 결과는 low-level source object pointer의 warm relation cache를 잃지 않고 같은 source project wrapper로
  감쌉니다.
- Gate 0는 exported sealed `BlogPostRelationSelector`, `BlogPostRelationSelectors` aggregate와 unexported
  discriminator representation을 고정했습니다. Public typing이 일부 misuse를 compile-time에 막더라도 internal
  adversarial seam은 nil/typed-nil/zero/cross-model sealed selector와
  zero eager query를 모두 구성해 panic 없이 `query_error/invalid_plan`, backend I/O 0을 확인해야 합니다. Detail
  message는 contract가 아닙니다.

Source relation cache는 source wrapper-scoped입니다. 같은 source wrapper의 반복 accessor와 그 wrapper를 만든 eager
result는 기존 low-level source-object cache를 공유하지만, 별도로 materialize한 source/target wrapper나 derived query
사이의 cache/identity 공유를 주장하지 않습니다.

### Generator fixture breadth

Current two-model product fixture만 통과하는 generator를 허용하지 않습니다. Focused generator/compile fixtures는
다음을 포함합니다.

- 관계가 없는 unrelated model도 query root/project wrapper를 얻는 project
- 여러 app과 여러 model이 섞이고 app/model 입력 순서를 바꾼 project
- Target model이 다른 forward relation의 source이기도 한 project
- Self-referential forward edge가 있는 project

이 fixture들은 deterministic declarations, namespace/collision, all-model wrapper와 one-hop accessor construction만
검증합니다. Target-also-source/self-edge가 컴파일된다는 사실은 multi-hop eager/runtime chaining support가 아닙니다.

Existing project relation-object prerequisite가 relation에 참여하지 않는 model의 `BindModel` 결과를 사용하지 않아
generated Go compile이 실패하는 경우에는 그 bind 결과만 명시적으로 consume하도록 최소 수정합니다. 이 수정은
binding validation이나 공개 surface를 줄이지 않고 current product exact 13 bytes를 변경하지 않아야 합니다.

### Origin과 session lifetime

Facade/query/wrapper는 생성 origin의 유효기간 안에서만 사용합니다. `db.Session` 또는 `db.RelationSession`에서
만든 facade는 callback 안에서만 지원합니다. Callback 밖 사용은 계약 위반이지만 현재 interface만으로 항상
탐지할 수 있다고 주장하지 않습니다. 이미 warm한 cache는 backend I/O 없이 성공할 수 있으므로 callback 이후
항상 실패한다는 test도 만들지 않습니다. 더 강한 lifetime capability는 별도 core work/ADR 대상입니다.

### Deterministic name decision

Implicit English pluralization을 도입하지 않습니다. Gate 0는 explicit alias prefix + model Go name의 singular
surface를 사용하고 exact exported 이름을 다음처럼 고정했습니다.

- Project capability/entry/aggregate: `Backend`, `Using`, `Models`
- Model wrapper/root: `AuthorsAuthor`/`AuthorsAuthorQuery`, `BlogPost`/`BlogPostQuery`
- Forward eager surface: `BlogPostRelationSelector`, `BlogPostRelationSelectors`, `BlogPostEagerQuery`
- Raw clone unwrap: `Unwrap`

`BlogPostRelationSelector`는 package 밖에서 구현할 수 없는 sealed interface이고 `BlogPostRelationSelectors`가
`Author`와 `Reviewer` 값을 제공합니다. 내부 unexported discriminator와 eager query는 두 edge를 같은 표현으로
처리하되 query 하나당 정확히 하나만 선택합니다. 다음 namespace를 canonical ordering으로 충돌 검사합니다.

- Existing binding/query/object/reverse/prefetch/select-related/delete declarations
- New backend capability, entrypoint, aggregate, all-model wrapper/query/eager/selector declarations
- App alias, model Go name, relation field/accessor와 generator version/input hash declarations

Alias/model boundary ambiguity, suffix collision, handwritten project source collision과 invalid Go identifier를 bytes
publication 전에 거부합니다. Handwritten source까지 schema만으로 완전히 알 수 없으므로 exact candidate-union compile을
최종 방어선으로 둡니다.

### Additive first publication

이번 packet은 새 project companion 한 파일의 첫 게시와 같은 파일 재생성만 다룹니다.

- Check mode는 expected bytes와 existing target의 동일성만 확인하고 Verify를 실행했다고 주장하지 않습니다.
- Write mode는 same-directory temp write/sync, whole candidate package Verify 성공 뒤 단일 rename으로 게시합니다.
- Missing-target Verify 실패는 target을 만들지 않고, replacement 실패는 previous last-good bytes를 보존합니다.
- Directory fsync, 여러 generated 파일의 atomic upgrade, rename/deprecation/repair는 별도 work입니다.
- Generator version과 canonical input hash는 drift evidence이며 runtime compatibility handshake가 아닙니다.

## 구현 단계

1. Gate 0에서 잠근 exact public-name/selector representation과 capability/error precedence를 그대로 구현합니다.
2. `codegen/project_relation_facade.go`와 focused test/golden으로 deterministic companion bytes를 만듭니다.
3. Unrelated-model prerequisite의 unused bind regression을 고치고 current exact 13 byte identity를 재확인합니다.
4. Existing exact 13을 재생성 전후 byte 비교하고 product project에 새 companion 하나를 first-publish합니다.
5. Product test에서 all-model wrapper/query root, required/nullable accessor, lazy/eager cache와 error boundary를 검증합니다.
6. Compiletest의 virtual overlay를 제거하고 production no-overlay consumer, `db.Queryer` negative와 session composite
   assignability를 검증합니다. AST/source gate는 `Save`/`Delete`, reverse symbol과 low-level `Object` type의
   application-facing 재노출을 거부합니다.
7. Protocol/CI inventory를 실제 새 file/test inventory로 다시 측정하고 literal self-check와 함께 갱신합니다.
8. Normal/race/CGO-disabled/vet/root CI와 independent audit 뒤에만 status를 Implemented/Verified로 전환합니다.

## 완료 조건

- [x] Exact exported names와 selector representation을 Proposed ADR의 implementation decision으로 고정하고 frozen
  typed-nil/binding-error precedence를 구현
- [x] Existing generated exact 13 bytes/digest가 구현 전후 byte-identical
- [x] Exactly one project facade companion의 deterministic generator/golden/first-publication 구현
- [x] All declared model query root와 project-owned pointer wrapper 구현
- [x] Query root와 required/nullable forward accessor target wrapper type 동일성 검증
- [x] Raw app model/low-level Object 반환 금지와 wrapper namespace disjoint compile gate
- [x] Project-local Queryer+Mutator positive, static `db.Queryer` negative, RelationAtomic 비요구 검증
- [x] Minimal Queryer+Mutator fake/`db.Session` positive가 RelationAtomic/RelationMutator 없이 성립
- [x] Nil/typed-nil backend, binder precedence, stable category/code와 I/O 0 검증
- [x] Author/Reviewer common selector와 selected eager state 보존 검증
- [x] Author/Reviewer SelectRelated before/after Filter/OrderBy/Limit, required/present/NULL 검증
- [x] Lazy cache, eager 추가-query 0, copied/repeated eager shared evaluation과 derived-chain independence 검증
- [x] Nil/typed-nil/zero/cross-model selector와 zero Models/root/eager의 structured pre-I/O failure 검증
- [x] Nil/zero/copied wrapper pre-I/O failure와 explicit raw-model unwrap 검증
- [x] Source cache wrapper-scope와 target pointer/downstream cache 비계약 검증
- [x] Callback-local session origin positive와 post-callback lifetime 비계약 경계 고정
- [x] Reverse manager, target pointer identity, downstream target cache를 제품 지원으로 오인할 surface가 없음
- [x] Missing/drift/check/write/Verify failure에서 target absence 또는 last-good 보존 검증
- [x] Overlay fixture 제거, production no-overlay external compile과 typed negative gate 통과
- [x] External AST/source gate가 Save/Delete/reverse와 low-level Object re-exposure를 거부
- [x] Unrelated/multi-app/multi-model/target-source/self-edge generator fixture가 deterministic compile을 통과
- [x] Unrelated-model object prerequisite unused-bind regression과 current exact 13 byte identity 검증
- [x] Physical/generated/test inventory와 workflow self-check를 실제 값으로 갱신
- [x] Focused normal/race/CGO-disabled/vet, root CI와 independent P0-P3 audit 통과
- [ ] Activation, implementation, completion documentation과 terminal evidence를 서로 다른 exact head로 증명

## 진행 기록

- [x] GDJ-0031 terminal exact-head `3d661251...`을 EVID-067/run `31533890720`에서 clean baseline으로 확인
- [x] GDJ-0032 active work와 Proposed ADR-0032 경계 작성
- [x] Exact public-name and selector-representation decision gate
- [x] Generator/product/compile/runtime implementation
- [x] Inventory/workflow/protocol transition
- [x] Local verification and independent P0-P3 audit
- [ ] Implementation exact-head hosted verification
- [ ] Completion documentation and terminal evidence

## 미결정과 blocker

외부 blocker는 없습니다. Gate 0 이름, no-pluralization collision diagnostic, generator version과 canonical input
hash encoding은 이 implementation tree에서 결정·검증됐습니다. 남은 blocker가 아닌 절차는 implementation exact-head
hosted CI와 그 뒤 completion/terminal documentation evidence입니다.

## 테스트 증거

- Clean baseline: EVID-067 / hosted run `31533890720`, exact terminal head `3d661251...` only
- Activation proof: exact activation head `2399cc44f6da975f154806f91eeee06dcca3b5a8`의 hosted
  [run 31537726792](https://github.com/progresshans/godj/actions/runs/31537726792) attempt 1은 26/26 jobs와
  326/326 recorded steps를 통과했습니다. 이 run은 implementation tree proof로 재사용하지 않습니다.
- Product baseline: exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation 11/12;
  REL-002 `oracle_locked`
- Frozen old generated subset: exact 13, 26,140 bytes/SHA-256
  `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`
- New generated union: exact 14, 39,243 bytes/SHA-256
  `2a141f1962887a9c610dd2d0005f401ecd8759e4d0bf0ce5cde1c3f210d1ba5f`; physical fixture exact 17,
  94,439 bytes/SHA-256 `3fc7aba625cf231bc3521f3ad19270a05405e9bfea3d4799b36ca3dd907752fd`
- Workflow-equivalent top-level inventory: 697 run/697 pass/0 skip, 70,659 bytes/SHA-256
  `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`
- Local validation on 2026-08-12: focused and integrated normal/race/CGO-disabled/vet, Linux/386 compile-only,
  `make ci`, `git diff --check`, gofmt and three independent audits all PASS; final P0/P1/P2/P3=`0/0/0/0`.
- This implementation tree: exact-head hosted CI `not run/pending`; activation run `31537726792`를 그 proof로
  재사용하지 않음

## 위험과 rollback

가장 큰 위험은 test-only spike 이름을 검토 없이 제품 API로 굳히거나, 새 파일 하나의 first-publication을 general
generated upgrade로 과장하는 것입니다. Existing 13 generated bytes를 별도 legacy subset lock으로 유지하고 새
companion은 independent target으로 게시합니다. 구현이 실패하면 새 generator/test/product companion과 compiletest
전환만 되돌리며 기존 low-level kernel과 manifest/oracle bytes는 유지합니다.

## 다음 정확한 작업

통합 담당자는 이 동결된 implementation tree만 정확히 commit/push하고 고유 exact-head hosted CI를 terminal까지
감사합니다. 그 run을 activation proof와 분리해 기록한 뒤에만 ADR/work completion documentation과 terminal evidence를
별도 head로 닫습니다. REL-002 또는 reverse/write/general upgrade work는 이 packet에 섞지 않습니다.
