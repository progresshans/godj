# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 machine artifact commit:
  `9050b4d7d2a1ed961da5e7bdefde8f4c8653eb33`
  (`test: lock queryset evaluation cache contracts`)
- 기준 제품 commit:
  `af0bc7992cc156f118f75b04f658162ae5dbbb07`
  (`feat: implement save lifecycle orchestration`)
- 활성 작업 baseline: `9050b4d7d2a1ed961da5e7bdefde8f4c8653eb33`
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: QuerySet evaluation/cache 제품 API와 ownership 설계·구현
- 완료 작업: [GDJ-0007 QuerySet Evaluation and Cache Compatibility Contracts](../../work/0007-queryset-evaluation-cache-compatibility-contracts.md)
- 활성 작업: [GDJ-0008 QuerySet Evaluation and Cache Product Slice](../../work/0008-queryset-evaluation-cache-product-slice.md)
- 다음 ready 작업: 없음 — GDJ-0008 완료 뒤 다음 수직 단면을 선택

## 현재 checkout에서 확인된 사실

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- Schema DSL과 normalized IR v2가 `AutoField`, `CharField`, `BooleanField`, nullable
  CharField와 typed scalar default의 존재/zero value를 보존합니다.
- Deterministic codegen은 `Article`, FieldSet, descriptor, immutable create/patch builder,
  nullable deep clone, explicit-key constructor와 Save option helper를 생성하고 schema
  hash/generator version/last-good drift gate를 유지합니다.
- Generic Manager/QuerySet, typed/dynamic lookup, immutable read/mutation AST와 SQLite
  compiler/executor가 구현됐습니다. QuerySet은 아직 의도적으로 uncached이며 현재
  `All(ctx)` 호출마다 backend query를 실행합니다.
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
- Protocol v2의 기존 세 ordered set, M1 read/metadata 11개, M2 write/migration 11개와
  Save lifecycle 12개가 모두 Django oracle과 0-diff이며 총 34개 manifest 상태가
  `passing`입니다.
- 네 번째 QuerySet evaluation/cache set의 QRY-011..021은 exact Django oracle과
  provenance가 `oracle_locked`입니다. 현재 GoDj QuerySet은 intentionally uncached이며
  static fixture가 내는 11개 mismatch는 제품 actual이 아니라 false-green baseline입니다.
- 네 set의 contract ID/scenario는 전역으로 유일하고, 모든 12개 ordered cross-binding이
  validation에서 거부됩니다.
- M1 oracle SHA-256은
  `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`, M2 oracle은
  `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`, Save oracle은
  `05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb`입니다.
- QuerySet evaluation/cache oracle은 56,426 bytes이며 SHA-256은
  `d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`입니다.
- 두 독립 GoDj Save actual은 11,743 bytes로 byte-identical하며 SHA-256은
  `bc129818165d1ea147afa39a083964bcad710f744b341d4f083fdac2581dd225`입니다.
- Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이며 Django reference SQLite
  3.50.4와 별도 fingerprint로 관리합니다.
- GDJ-0007의 전체/uncached/race/CGO=0, Python portable/exact 44-test, 네 set validation,
  기존 34 differential, deterministic oracle과 mutation/hardcode audit는
  [EVID-20260808-006](TEST_EVIDENCE.md#evid-20260808-006--gdj-0007-queryset-evaluation-and-cache-compatibility-contracts)에
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

### QuerySet evaluation/cache reference

- QRY-011..021은 성공한 full result cache, stale snapshot, chain/fresh 독립성,
  Count/Exists/iterator/index/First와 실패 재시도를 step별 SELECT count로 고정합니다.
- SQL 문자열, Django private `_result_cache`와 Python object identity는 계약하지 않습니다.
- Query-cache oracle은 `oracle_locked`일 뿐 제품 `passing`이 아니며, static fixture와
  unsupported adapter가 false green 없이 실패해야 합니다.
- Direct value copy, cached alias와 concurrent evaluation은 Django oracle만으로 결정하지
  않고 GDJ-0008의 Proposed ADR-0012와 Go-native race/cancellation test에서 확정합니다.

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 아직 열려 있습니다.

1. Q-007: locked cache/terminal 의미를 만족하는 Go public API와 cached result alias 정책
2. Q-011: value-copy QuerySet cache ownership, 동시 평가와 waiter cancellation 계약
3. Q-010: public CLI와 project library/generator version protocol
4. Q-012: public migration file, data callback ABI, graph/lock와 crash recovery
5. Q-013: cross-app relation type/import/reverse loader 경계

## 다음 정확한 작업

1. `orm.QuerySet[M]`의 immutable plan과 evaluation state ownership 후보를 compile/runtime/
   race spike로 비교합니다.
2. [ADR-0012](../adr/0012-queryset-evaluation-cache-ownership.md)에 direct value copy,
   chain/fresh, success/error/cancellation, cached alias와 terminal API를 기록하고 Accepted로
   올립니다.
3. 성공/빈 결과 cache, 실패 재시도와 concurrent same-query/waiter cancellation을 fake
   backend test-first로 구현합니다.
4. Count/Exists/iterator/First/Fresh와 SQLite integration을 연결한 뒤 QRY-011..021 실제
   GoDj adapter를 작성합니다.

## 작업 재개 체크포인트

- 공개 framework API: M1 read와 M2 제한 write/migration/Save subset만 검증됨
- 활성 baseline: `main@9050b4d7d2a1ed961da5e7bdefde8f4c8653eb33`, 시작 시 work/status
  인수인계 문서만 미커밋
- GDJ-0008은 제품 slice이며 첫 public/API 변경 전에 ADR-0012와 focused ownership test 필요
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 추측: 값 타입 QuerySet에 mutex/cache를 직접 넣거나, direct copy/chain/
  fresh와 waiter cancellation의 공유 범위를 테스트 없이 암묵적으로 확정하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
