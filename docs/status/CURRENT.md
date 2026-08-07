# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 machine artifact commit:
  `6f1aab78a6e365e62f5a3b59b040b90b981b4978`
  (`feat: implement queryset evaluation cache`)
- 기준 제품 commit:
  `6f1aab78a6e365e62f5a3b59b040b90b981b4978`
  (`feat: implement queryset evaluation cache`)
- 활성 작업 baseline: `6f1aab78a6e365e62f5a3b59b040b90b981b4978`
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: Migration dependency/applied-state planner reference 계약 설계·관찰
- 완료 작업: [GDJ-0008 QuerySet Evaluation and Cache Product Slice](../../work/0008-queryset-evaluation-cache-product-slice.md)
- 활성 작업: [GDJ-0009 Migration Planning Compatibility Contracts](../../work/0009-migration-planning-compatibility-contracts.md)
- 다음 ready 작업: 없음 — GDJ-0009 결과로 product slice와 ADR 경계를 확정

## 현재 checkout에서 확인된 사실

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- Schema DSL과 normalized IR v2가 `AutoField`, `CharField`, `BooleanField`, nullable
  CharField와 typed scalar default의 존재/zero value를 보존합니다.
- Deterministic codegen `godj-codegen-m2-v3`는 `Article`, FieldSet, descriptor,
  `CloneModel`, immutable create/patch builder, nullable deep clone, explicit-key constructor와
  Save option helper를 생성하고 schema hash/generator version/last-good drift gate를 유지합니다.
- Generic Manager/QuerySet, typed/dynamic lookup, immutable read/mutation AST와 SQLite
  compiler/executor가 구현됐습니다. QuerySet은 direct value copy가 공유하는 별도 평가
  state를 가지며 성공한 empty/non-empty `All`을 cache합니다. Chain과 `Fresh`는 독립
  state를 받고 `Count`, `Exists`, `At`, `First`, `Iterate` terminal을 제공합니다.
- Write 제품 단면은 one-row create/partial update/delete, callback-bound transaction과
  mutable instance Save를 제공합니다. Save는 fully loaded default field 전체,
  typed/dynamic named/empty mask, force mode와 explicit PK UPDATE→INSERT를 구분합니다.
- `Manager[M].Save`는 caller-owned `db.Mutator`/transaction session을 사용하고 generated
  key를 instance에 기록합니다. Outer rollback은 DB를 복원하지만 Go instance field/key를
  되돌리지 않습니다.
- Migration 제품 단면은 versioned `ProjectState`, typed `CreateModel`/nullable no-default
  `AddField`, preflighted Executor와 같은 SQLite transaction의 editor/recorder입니다.
- SQLite table rebuild가 필요한 default AddField, indexed/CHECK/generated/view/trigger/FK
  dependency drop은 silent fallback 없이 구조화된 capability error입니다.
- Protocol v2의 네 ordered set, M1 read/metadata 11개, M2 write/migration 11개,
  Save lifecycle 12개와 QuerySet evaluation/cache 11개가 모두 Django oracle과 의미상
  0-diff이며 총 45개 manifest 상태가 `passing`입니다.
- QuerySet static fixture가 내는 ordered 11개 `not_implemented` mismatch는 구현 전 상태가
  통과하지 않음을 보존하는 false-green baseline이며 현재 제품 actual이 아닙니다.
- 네 set의 contract ID/scenario는 전역으로 유일하고, 모든 12개 ordered cross-binding이
  validation에서 거부됩니다.
- M1 oracle SHA-256은
  `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`, M2 oracle은
  `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`, Save oracle은
  `05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb`입니다.
- QuerySet evaluation/cache oracle은 56,426 bytes이며 SHA-256은
  `d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`입니다.
- QuerySet manifest는 8,987 bytes, SHA-256
  `35f808e361d85228fe3048ae2510cf296f3127bee5572ce3ed9e66c6fd3eb3e2`입니다.
- 두 독립 GoDj QuerySet actual은 각각 56,283 bytes로 서로 byte-identical하며 SHA-256은
  `c7ccad635a13e3e071cba4d46b79d3110e24b2e9501a1ca95054ded520b0fa92`입니다. Django
  oracle과는 canonical byte가 아니라 protocol의 normalized 의미로 0-diff입니다.
- 두 독립 GoDj Save actual은 11,743 bytes로 byte-identical하며 SHA-256은
  `bc129818165d1ea147afa39a083964bcad710f744b341d4f083fdac2581dd225`입니다.
- Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이며 Django reference SQLite
  3.50.4와 별도 fingerprint로 관리합니다.
- GDJ-0008의 full/race/CGO=0/vet, 100회 state test, codegen/compile, Python exact,
  네 set 45 differential과 mutation/hardcode audit는
  [EVID-20260808-007](TEST_EVIDENCE.md#evid-20260808-007--gdj-0008-queryset-evaluation-and-cache-product-slice)에
  기록했습니다.
- GitHub Actions workflow는 push하지 않아 hosted 실행 증거가 없습니다. 로컬 Django
  checkout은 수정하지 않았습니다.

## 확정된 결정

### M0/M1

- Exact Python/Django/SQLite/platform/locale profile과 uv lock/hash
- Schema DSL → IR → codegen → generic core → AST → SQLite read 수직 단면
- Generated zero-state descriptor와 nullable CharField read 표현 `*string`
- Typed/dynamic lookup의 동일 AST 합류와 construction-time structured error
- Pure-Go SQLite backend, context/cleanup/race와 codegen last-good 보존 gate

### M2 write/migration

- Generated immutable create/patch와 `Change[T]`/`NullableChange[T]`는 ADR-0009 Accepted
- Auto key 값과 존재 상태를 분리하고 invalid mutation/custom input alias를 I/O 전에 거부
- Parameterized one-row writes와 callback 밖에서 만료되는 transaction-bound session
- Schema IR 기반 immutable ProjectState, typed operation과 full state preflight
- SQLite DDL과 app/name recorder는 한 pinned connection/transaction에서 commit/rollback
- Native DROP COLUMN/table rebuild 한계는 capability error로 드러냄
- Migration core는 generated/current model package에 의존하지 않음

### M2 Save lifecycle

- [ADR-0011](../adr/0011-m2-save-lifecycle-orchestration.md)에 따라 authoritative API는
  `Manager[M].Save(ctx, db.Mutator, *M, ...SaveOption[M])`입니다.
- Concrete immutable `SaveOption[M]`과 sealed `WritableField[M]`가 model별 typed mask를
  보존하고, dynamic field name은 같은 metadata/plan 경로로 수렴합니다.
- Generated instance `Save` method는 field-name 충돌 때문에 만들지 않고 explicit-key와
  option helper만 생성합니다.
- Default Save는 dirty-only가 아니라 fully loaded writable field 전체를 저장합니다.
  Empty mask는 zero-I/O, force missing row는 `not_updated`, default explicit-key 0행은
  같은 key를 포함한 INSERT fallback입니다.
- SQLite primary-key 충돌은 문자열이 아니라 structured extended result code로 분류하고
  원 driver cause를 보존합니다.

### QuerySet evaluation/cache

- QRY-011..021은 성공한 full result cache, stale snapshot, chain/fresh 독립성,
  Count/Exists/iterator/index/First와 실패 재시도를 step별 SELECT count로 고정합니다.
- SQL 문자열, Django private `_result_cache`와 Python object identity는 계약하지 않습니다.
- GDJ-0007에서 oracle을 잠근 뒤 GDJ-0008이 실제 public API adapter로 11개를 모두
  `passing`으로 올렸습니다. Static fixture의 ordered 11 mismatch는 계속 보존합니다.
- Direct value copy는 평가 state를 공유하고 chain/`Fresh`는 새 state를 받습니다. 성공한
  empty/non-empty 결과만 cache하며 실패와 owner cancellation은 재시도할 수 있습니다.
- 같은 state의 concurrent `All`은 singleflight하고 waiter cancellation은 owner를
  취소하지 않습니다. Generated `CloneModel`은 cached slice와 nullable pointer alias를
  caller mutation에서 격리합니다.
- Cold `Count`는 O(N) row drain, `At`은 offset 없이 O(index) 순회하는 첫 단면의 명시적
  성능 제한입니다. SQL aggregate/offset 최적화는 후속 query breadth에 남습니다.

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 아직 열려 있습니다.

1. Q-011: request/transaction/hook 범위의 goroutine·lifetime 정책
2. Q-010: public CLI와 project library/generator version protocol
3. Q-012: migration planning/graph, public file, data callback ABI, lock와 crash recovery
4. Q-013: cross-app relation type/import/reverse loader 경계

## 다음 정확한 작업

1. Pinned Django 6.1 commit의 migration executor/graph/loader provenance와 public
   behavior를 확인하고 MIG-005..MIG-016 후보를 disposable exact probe로 실행합니다.
2. Graph 삽입 순서, forward/backward target, applied pruning과 missing/cycle/
   inconsistent history의 관찰 경계를 정해 12개 이하 계약으로 잠급니다.
3. 다섯 번째 manifest/oracle/static fixture를 별도 set으로 연결하고 기존 네 set과의
   20개 ordered cross-binding, payload mutation과 two-process determinism을 검증합니다.
4. Product `migrations/**`는 변경하지 않고 GDJ-0010의 planner/graph ADR 입력만
   기록합니다.

## 작업 재개 체크포인트

- 공개 framework API: M1 read, M2 제한 write/migration/Save와 QuerySet cache/terminal
  subset만 검증됨
- 활성 baseline: `main@6f1aab78a6e365e62f5a3b59b040b90b981b4978`; 인수인계 문서만
  후속 docs commit으로 추가됨
- GDJ-0009은 contract-only이며 `migrations/**` 제품 source를 바꾸지 않음
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 추측: Django graph의 map/삽입 순서를 안정 계약으로 오인하거나 migration
  file ABI와 public CLI를 semantic identity/dependency보다 먼저 고정하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
