# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 구현 commit: `de099f31738c1df0dcc4c6ffd609d0fb4f0d4683`
  (`fix: harden M2 write and migration boundaries`)
- 기준 conformance commit: `84d50f3` (`test: pass M2 write and migration contracts`)
- 활성 작업 baseline: `de099f31738c1df0dcc4c6ffd609d0fb4f0d4683`
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: M2 첫 write/migration 수직 단면 완료, Save lifecycle 계약 확장
- 완료 작업: [GDJ-0004 Write/Migration Walking Skeleton](../../work/0004-write-migration-walking-skeleton.md)
- 활성 작업: [GDJ-0005 Save Lifecycle Compatibility Contracts](../../work/0005-save-lifecycle-compatibility-contracts.md)
- 다음 ready 작업: 없음 — GDJ-0005 계약 결과로 후속 제품 범위를 결정

## 현재 checkout에서 확인된 사실

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- Schema DSL과 normalized IR v2가 `AutoField`, `CharField`, `BooleanField`, nullable
  CharField와 typed scalar default의 존재/zero value를 보존합니다.
- Deterministic codegen은 `Article`, FieldSet, descriptor, immutable create/patch builder와
  nullable deep clone을 생성하고 schema hash/generator version/last-good drift gate를
  유지합니다.
- Generic Manager/QuerySet, typed/dynamic lookup, immutable read/mutation AST와 SQLite
  compiler/executor가 구현됐습니다. Write는 one-row create/explicit partial update/instance
  delete와 callback-bound transaction 단면입니다.
- Migration 제품 단면은 versioned `ProjectState`, typed `CreateModel`/nullable no-default
  `AddField`, preflighted Executor와 같은 SQLite transaction의 schema editor/recorder를
  제공합니다.
- SQLite table rebuild가 필요한 default AddField, indexed/CHECK/generated/view/trigger/FK
  dependency drop은 silent fallback 없이 구조화된 capability error입니다.
- Protocol v2에서 M1 read/metadata 11개와 M2 write/migration 11개가 각각 Django oracle과
  0-diff이고 두 manifest 상태는 모두 `passing`입니다. Django oracle observation은
  `observed`, static not-implemented fixture는 set별 11개 mismatch를 계속 냅니다.
- M1 v2 oracle SHA-256은
  `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`, M2 oracle은
  `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`입니다.
- Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이며 Django reference SQLite
  3.50.4와 별도 fingerprint로 관리합니다.
- Generation drift, 전체 Go/vet/race, `CGO_ENABLED=0`, Python portable/exact 27-test,
  checksum과 두 set의 22개 passing은
  [EVID-20260808-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton)에
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

### M2 첫 제품 단면

- Generated immutable create/patch와 `Change[T]`/`NullableChange[T]`는 ADR-0009 Accepted
- Auto key 값과 존재 상태를 분리하고 invalid mutation/custom input alias를 I/O 전에 거부
- Parameterized one-row writes와 callback 밖에서 만료되는 transaction-bound session
- Schema IR 기반 immutable ProjectState, typed operation과 full state preflight
- SQLite DDL과 app/name recorder는 한 pinned connection/transaction에서 commit/rollback
- Native DROP COLUMN/table rebuild 한계는 capability error로 드러냄
- Migration core는 generated/current model package에 의존하지 않음
- M2 11개를 실제 adapter로 통과한 뒤에만 manifest를 `passing`으로 전환

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 아직 열려 있습니다.

1. Q-006 후속: mutable instance `Save()`와 loaded/new/dirty 의미
2. Q-007: QuerySet result/error cache와 terminal operation 공유
3. Q-010: public CLI와 project library/generator version protocol
4. Q-011: QuerySet/transaction/hook의 전체 goroutine safety
5. Q-012: public migration file, data callback ABI, graph/lock와 crash recovery
6. Q-013: cross-app relation type/import/reverse loader 경계

## 다음 정확한 작업

1. GDJ-0005의 MOD-008..017 후보를 Django 6.1 public 문서와 upstream test에 연결합니다.
2. Disposable exact SQLite에서 new/loaded save, `update_fields`, force flag, explicit PK와
   rollback instance/DB state를 두 번 canonical probe합니다.
3. 안정적으로 독립 관찰되는 8~12개만 세 번째 manifest에 고정합니다.
4. Django oracle, explicit not-implemented fixture와 세 set cross-binding false-green gate를
   만든 뒤 다음 제품 work를 활성화합니다.

## 작업 재개 체크포인트

- 공개 framework API: M1 read와 M2 제한 write/migration subset만 검증됨
- 활성 baseline: `main@de099f31738c1df0dcc4c6ffd609d0fb4f0d4683`, 시작 시 clean
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 추측: Django가 기본 instance save에서 dirty tracking을 한다고 가정하거나,
  이번 제한 migration core를 public file/graph/lock 지원으로 확대 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
