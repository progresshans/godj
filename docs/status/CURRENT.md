# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 제품·conformance commit:
  `af0bc7992cc156f118f75b04f658162ae5dbbb07`
  (`feat: implement save lifecycle orchestration`)
- Save reference contract commit:
  `138581da38bfbb6ba89ea5ca82752dfd3d76df02`
  (`test: lock save lifecycle compatibility contracts`)
- 활성 작업 baseline: `af0bc7992cc156f118f75b04f658162ae5dbbb07`
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: QuerySet evaluation/cache 호환 계약 확장
- 완료 작업: [GDJ-0006 Save Lifecycle Product Slice](../../work/0006-save-lifecycle-product-slice.md)
- 활성 작업: [GDJ-0007 QuerySet Evaluation and Cache Compatibility Contracts](../../work/0007-queryset-evaluation-cache-compatibility-contracts.md)
- 다음 ready 작업: 없음 — GDJ-0007 결과로 GDJ-0008 제품 API/ownership을 결정

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
- Protocol v2의 세 ordered set, M1 read/metadata 11개, M2 write/migration 11개와 Save
  lifecycle 12개가 모두 Django oracle과 0-diff이며 총 34개 manifest 상태가 `passing`입니다.
- 각 static not-implemented fixture는 구현 전 상태가 false pass하지 않도록 set별
  11/11/12개의 ordered status mismatch를 계속 냅니다. 현재 제품 actual이 아닙니다.
- M1 oracle SHA-256은
  `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`, M2 oracle은
  `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`, Save oracle은
  `05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb`입니다.
- 두 독립 GoDj Save actual은 11,743 bytes로 byte-identical하며 SHA-256은
  `bc129818165d1ea147afa39a083964bcad710f744b341d4f083fdac2581dd225`입니다.
- Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이며 Django reference SQLite
  3.50.4와 별도 fingerprint로 관리합니다.
- GDJ-0006의 전체/uncached/race/CGO=0, generation, compile, Python portable/exact 36-test,
  34 differential, deterministic actual과 mutation audit는
  [EVID-20260808-005](TEST_EVIDENCE.md#evid-20260808-005--gdj-0006-save-lifecycle-product-slice)에
  기록됐습니다.
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

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 아직 열려 있습니다.

1. Q-007: QuerySet result/error cache와 Count/Exists/iterator/fresh clone 의미
2. Q-011: value-copy QuerySet cache ownership과 동시 평가/waiter cancellation 계약
3. Q-010: public CLI와 project library/generator version protocol
4. Q-012: public migration file, data callback ABI, graph/lock와 crash recovery
5. Q-013: cross-app relation type/import/reverse loader 경계

## 다음 정확한 작업

1. Pinned Django commit의 QuerySet caching public docs/source/tests를 provenance로 확인합니다.
2. QRY-011..020 후보를 disposable SQLite에서 독립 실행해 result/error와 step별 query
   count를 두 번 canonicalize합니다.
3. Repeated/empty/stale snapshot, chain, Count/Exists/iterator/partial/failure retry/fresh clone을
   8~12개 ordered contract로 고정합니다.
4. Fourth manifest/Django runner/oracle/static fixture와 four-set uniqueness/cross-binding/
   payload mutation gate를 추가합니다.

## 작업 재개 체크포인트

- 공개 framework API: M1 read와 M2 제한 write/migration/Save subset만 검증됨
- 활성 baseline: `main@af0bc7992cc156f118f75b04f658162ae5dbbb07`, 시작 시 clean
- GDJ-0007은 contract-only: `orm/**`, `query/**`, `db/**`, `codegen/**` 제품 source 수정 금지
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 추측: 현재 uncached GoDj 결과만 보고 Django cache와 같다고 판단하거나,
  값 타입 QuerySet에 mutex/cache를 직접 넣고 copy/chain/concurrency 의미를 암묵적으로
  확정하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
