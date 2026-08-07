# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 구현 commit: `3e7c87839265e1b07b6d69f59f52e596623b1eb5`
  (`test: enforce Django scenario cleanup baseline`)
- remote: `https://github.com/progresshans/godj.git`, remote refs 없음
- 현재 단계: M2 write/migration reference contract 완료, 제품 수직 단면 준비
- 활성 작업: 없음
- 다음 ready 작업: [GDJ-0004 Write/Migration Walking Skeleton](../../work/0004-write-migration-walking-skeleton.md)

## 현재 checkout에서 확인된 사실

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- M1 범위의 Schema DSL, versioned IR, deterministic codegen, generated Article/FieldSet/
  descriptor, generic Manager/QuerySet, immutable Query AST와 SQLite compiler/executor가
  구현됐습니다. 한 app/한 model/제한 field와 read query만 해당합니다.
- QRY-001..010과 SCH-001의 M1 GoDj adapter는 protocol v2에서도 Django oracle 11개와
  일치하며 manifest 상태는 `passing`입니다.
- GDJ-0003은 별도 manifest에 MOD-001..007과 MIG-001..004를 고정했습니다. Manifest
  계약 11개는 `oracle_locked`, Django oracle suite는 `observed` 11개이고 GoDj 제품
  adapter는 아직 명시적 `not_implemented` 11개입니다.
- Protocol v2는 ordered contract ID/position, expected phase, profile과 payload dimension을
  suite에 결속하며 v1 artifact와 cross-set oracle을 거부합니다.
- Django runner는 자신이 구성한 disposable in-memory SQLite만 사용합니다. 외부 settings나
  파일 DB가 이미 구성돼 있으면 fail closed하며 파일 bytes/table/row 보존 regression이
  통과했습니다.
- Manifest별 default oracle 경로를 분리했고 unknown manifest는 명시적 `--output` 없이는
  실행하지 않아 M1 oracle 덮어쓰기를 막습니다.
- M1 v2 oracle SHA-256은
  `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`, M2 oracle은
  `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`입니다.
- M1 Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이며 Django reference
  SQLite 3.50.4와 별도 fingerprint로 관리합니다.
- Generation drift, 전체 Go/vet/race, `CGO_ENABLED=0`, Python portable/exact 27-test suite,
  checksum, M1 11 passing과 M2 expected 11 mismatch가
  [EVID-20260808-002](TEST_EVIDENCE.md#evid-20260808-002--gdj-0003-write-migration-compatibility-contracts)에서
  검증됐습니다.
- GitHub Actions workflow는 action SHA와 tool version을 고정했지만 push하지 않아
  hosted 실행 증거는 없습니다. 로컬 Django checkout은 수정하지 않았습니다.

## 확정된 결정

### M0/M1

- Exact Python/Django/SQLite/platform/locale profile과 uv lock/hash
- Schema DSL → IR → codegen → generic core → AST → SQLite read 수직 단면
- Generated zero-state descriptor와 nullable CharField read 표현 `*string`
- Typed/dynamic lookup의 동일 AST 합류와 construction-time structured error
- Pure-Go SQLite backend, context/cleanup/race와 codegen last-good 보존 gate

### GDJ-0003 contract 확장

- M0 protocol v1은 expected phase를 계약에 결속하는 protocol v2로 명시 승격
- M1 행동 계약/ID/passing 상태는 유지하고 write/migration은 별도 ordered set으로 관리
- `oracle_locked`는 Django reference 고정을, `red`는 실행 가능한 GoDj adapter mismatch를
  뜻하며 static `not_implemented` fixture만으로 red를 주장하지 않음
- Create omitted/explicit NULL의 수렴과 update unchanged/explicit NULL의 차이를 분리
- Successful reverse는 `commit`, 실제 transaction/error rollback은 `rollback` phase
- Migration state는 managed table inventory, schema, rows와 app/name recorder를 관찰
- 외부 DB 격리, output path 결속, rollback 기존 값 복원과 migration failure recovery를
  false-green 회귀로 고정
- 다음 제품 API와 core 경계는 [ADR-0009](../adr/0009-m2-explicit-write-change-state.md),
  [ADR-0010](../adr/0010-m2-migration-state-and-executor-boundary.md)의 Proposed 상태로 시작

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. 다음 결정은 아직 열려 있습니다.

1. Q-006의 정확한 exported create/patch/change API와 Manager/instance write 형태
2. Q-007 QuerySet result/error cache와 terminal operation 공유
3. Q-010 public CLI와 project library/generator version protocol
4. Q-011 QuerySet/transaction/hook의 전체 goroutine safety
5. Q-012 public migration file, data callback ABI, dependency graph, lock와 crash recovery

## 다음 정확한 작업

1. Clean checkout에서 GDJ-0004를 `active`로 바꾸고 이 handoff commit을 baseline으로 기록합니다.
2. ADR-0009의 두 write API 후보를 external positive/negative compile fixture로 비교합니다.
3. ADR-0010의 ProjectState/Operation/Executor dependency graph와 SQLite transaction 경계를
   compile/runtime failure spike로 검증합니다.
4. 결과에 따라 ADR-0009/0010을 Accepted로 올리거나 대안과 실패 근거를 기록한 뒤
   mutation plan과 product code를 구현합니다.

## 작업 재개 체크포인트

- 공개 framework API: M1 read subset만 검증됐고 write/migration API는 아직 Proposed
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 추측: locked Django oracle이 존재한다는 이유로 GoDj write/migration 제품
  코드가 구현됐거나 M2 11개가 passing이라고 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
