---
id: GDJ-0014
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "b6af5056bb67fc1d2d32b2163cb7091d700b1e7e"
depends_on: ["GDJ-0013"]
contracts: ["MIG-027..MIG-036", "Q-012"]
allowed_paths: ["migrations/backend/history.go", "migrations/history.go", "migrations/history_test.go", "migrations/planner.go", "migrations/planner_test.go", "migrations/external_test.go", "db/sqlite/migration_history.go", "db/sqlite/migration_history_test.go", "db/sqlite/backend.go", "conformance/contracts/migration-restart-manifest.json", "conformance/runners/godj/migration_restart_scenarios.go", "conformance/runners/godj/runner.go", "conformance/runners/godj/runner_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/internal/protocol/migration_restart_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "Makefile", ".github/workflows/ci.yml", "conformance/README.md", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Recorder-backed Restart Planning Product Slice

## 사용자에게 보이는 결과

GoDj가 새 process/backend에서 durable migration recorder를 read-only로 읽고, known history를
검증한 뒤 이미 완료한 migration을 건너뛰어 target까지 남은 plan을 계산할 수 있게 합니다.
Recorder table이 없는 새 database는 read만으로 table을 만들지 않습니다.

## 목표

- 별도 backend-neutral `AppliedMigrationReader`와 raw transport 구현
- `LoadAppliedState`의 copy/validation/error/context 경계 구현
- `Planner.CheckHistory` explicit preflight 추가, 기존 `Plan` validation 보존
- SQLite recorder absent/empty/record/unrecord read-only 구현
- Fresh file backend와 서로 다른 database isolation 검증
- Unknown legacy row 보존, known inconsistent history 거부
- MIG-027..036 GoDj live adapter와 locked oracle 10개 0-diff
- Existing `AtomicBackend`/`Transaction`, Planner와 ExecutePlan source compatibility 보존
- ADR-0015를 구현 증거에 따라 Accepted 여부 결정

## 비목표

- `Load + CheckHistory + Plan + ExecutePlan` 통합 API
- Recorder history에서 historical `ProjectState` 재구성
- Multi-process lock, revision token, read/execution session binding과 concurrent writer 직렬화
- Crash repair와 schema/recorder reconciliation
- Migration file/source loader, public `godj migrate`/`showmigrations`/`makemigrations`
- `AppliedState.Keys()` listing accessor와 alias registry/router
- Data migration callback ABI, replacement/squash/merge/fake/optimizer
- PostgreSQL/MySQL/MariaDB/Oracle backend
- Locked Django oracle/static fixture payload 또는 bytes 변경
- 기존 여섯 product set과 DEV-0001 의미 변경

## 선행 조건과 기준 상태

- Baseline machine commit:
  `main@b6af5056bb67fc1d2d32b2163cb7091d700b1e7e`
- Product baseline commit:
  `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`
- [GDJ-0013](0013-recorder-backed-restart-planning-compatibility-contracts.md)은
  MIG-027..036을 10 `oracle_locked`/10 `observed`/10 static `not_implemented`로 고정했습니다.
- 현재 제품 분류는 `63 passing + 4 deviation`이고 reference 총계는 77개입니다.
- [ADR-0013](../docs/adr/0013-immutable-migration-planner.md)은 caller-supplied
  `AppliedState`와 zero-I/O Planner를, [ADR-0014](../docs/adr/0014-migration-plan-execution-atomic-reverse.md)는
  preplanned `ExecutePlan`을 소유합니다.
- [ADR-0015](../docs/adr/0015-recorder-backed-applied-state.md)는 Proposed이며 구현/검증 전
  제품 지원을 뜻하지 않습니다.

## Django Reference / Contract

Exact profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
CPython 3.14.3, SQLite 3.50.4, UTC/C locale입니다.

- MIG-027/028: absent/existing-empty recorder read
- MIG-029/030: record/unrecord 뒤 fresh reader
- MIG-031: 서로 다른 database의 recorder isolation
- MIG-032/033: applied prefix tail과 fully-applied empty plan
- MIG-034: unknown legacy row 보존과 known plan
- MIG-035: explicit history preflight가 planning 전에 known inconsistency 거부
- MIG-036: 중간 실패의 durable prefix에서 fresh restart tail

모든 계약은 `evaluation` phase입니다. SQL text, SELECT count, physical row order, timestamp,
private cache/object identity는 비교하지 않습니다. Read/planning capture는 recorder/schema
before/after가 같고 DDL/write/기타 non-SELECT statement가 0이어야 합니다.

## 설계와 가설

검증할 public shape는 다음과 같습니다.

```go
// migrations/backend
type AppliedMigration struct { App, Name string }
type AppliedMigrationReader interface {
    ReadAppliedMigrations(context.Context) ([]AppliedMigration, error)
}

// migrations
func LoadAppliedState(context.Context, backend.AppliedMigrationReader) (AppliedState, error)
func (Planner) CheckHistory(AppliedState) error
```

Data flow는 다음 하나로 고정합니다.

```text
[]backend.AppliedMigration
→ []migrations.MigrationKey
→ NewAppliedState
→ Planner.CheckHistory
→ Planner.Plan
```

`AppliedMigrationReader`는 `Recorder`/`Transaction`/`AtomicBackend`에 embed하지 않습니다.
SQLite는 `godj_migrations`를 직접 SELECT하고 정확한 missing-table 오류만 empty로 정규화하며
table/column corruption과 scan/rows 오류는 보존합니다. Alias는 string 인자가 아니라 선택된
backend instance로 격리합니다.

Recorder I/O failure는 `migration_recorder_error/read_failed` 후보와 cause-preserving error로
분류합니다. Raw invalid/duplicate key와 history/target 오류는 기존 planning taxonomy를
유지합니다. 실제 구현 결과에 따라 [ADR-0015](../docs/adr/0015-recorder-backed-applied-state.md)를
Accepted 또는 수정합니다.

## 구현 단계

1. Backend DTO/read port와 source compatibility compile test를 먼저 추가합니다.
2. `LoadAppliedState` context/nil/typed-nil/error/raw-key validation test를 작성합니다.
3. `Planner.CheckHistory`를 기존 graph validation에 연결하고 `Plan`의 중복 방어를 보존합니다.
4. SQLite direct SELECT, exact missing-table normalization과 rows lifecycle을 구현합니다.
5. Absent/empty/fresh/alias/concurrent reader integration과 mutation gates를 추가합니다.
6. MIG-027..036 live adapter를 locked input fixture에서 scenario-driven으로 연결합니다.
7. Manifest status만 10 `passing`으로 전환하고 oracle/static fixture bytes를 보존합니다.
8. Two-process actual, full/race/CGO=0/vet/make check와 documentation handoff를 완료합니다.

## 완료 조건

- [ ] Recorder table absent read가 table을 만들지 않고 empty state 반환
- [ ] Existing empty, record/unrecord와 fresh file backend semantics 검증
- [ ] 서로 다른 두 database/reader state 격리
- [ ] Raw invalid/duplicate identity 거부와 unknown legacy identity 보존
- [ ] Explicit `CheckHistory`가 planning 전에 known inconsistency를 거부
- [ ] Applied prefix/fully-applied/failure-restart plan이 locked result와 일치
- [ ] Context/cause/rows-close 오류와 typed-nil reader fail-closed
- [ ] Concurrent read race-safe, concurrent writer/TOCTOU는 비보장으로 명시
- [ ] Existing `AtomicBackend`/`Transaction` fake가 수정 없이 compile
- [ ] `migrations/backend`이 top-level `migrations`를 import하지 않음
- [ ] MIG-027..036 GoDj actual이 Django oracle과 10개 semantic 0-diff
- [ ] Product actual 두 process가 byte-identical하고 unknown scenario가 exit 2/no output
- [ ] Static fixture ordered 10 mismatch와 seven-set 42 cross-binding 유지
- [ ] Existing `63 passing + 4 deviation` 회귀 없음; 완료 후 `73 passing + 4 deviation`
- [ ] Locked oracle/static fixture bytes와 기존 artifact checksum 유지
- [ ] Full Go/race/CGO=0/vet, portable/exact Python, `make check` 통과
- [ ] ADR/work/CURRENT/matrix/evidence가 같은 checkout을 가리킴

## 진행 기록

- [x] GDJ-0013 exact reference와 false-green baseline 완료
- [x] Package ownership/read-check-plan boundary read-only design
- [ ] Backend/core public API test-first implementation
- [ ] SQLite reader와 live product adapter
- [ ] Full verification과 handoff

## 수정 파일

아직 제품 source는 수정하지 않았습니다. 예상 경로는 frontmatter `allowed_paths`에 한정하며,
실제 변경 파일과 역할은 완료 시 기록합니다.

## 결정된 사항

- 2026-08-08: Recorder read port는 transaction write interface와 분리합니다.
- 2026-08-08: Backend-neutral raw DTO는 `migrations/backend`, semantic validation은
  top-level `migrations`가 소유하는 방향을 Proposed ADR-0015에 기록했습니다.
- 2026-08-08: Read/check/plan을 ExecutePlan과 합치지 않고 TOCTOU/ProjectState reconstruction을
  후속 범위로 남깁니다.

## 미결정/Blocker

외부 blocker는 없습니다. Error type의 최종 public name과 SQLite missing-table 분류 seam은
test-first 구현에서 확정합니다. Multi-process lock, lifecycle/CLI와 ProjectState reconstruction은
이 작업을 확장하지 않고 별도 work/ADR로 남깁니다.

## 테스트 증거

- Baseline:
  [EVID-20260808-012](../docs/status/TEST_EVIDENCE.md#evid-20260808-012--gdj-0013-recorder-backed-restart-planning-compatibility-contracts)
- Not run: GDJ-0014 product/API/live adapter — active work 시작 전

## 위험과 rollback

- Reader를 transaction interface에 embed하면 모든 backend/fake를 불필요하게 깨뜨립니다.
- Missing-table 외 오류까지 empty로 삼키면 corrupt recorder가 false green이 됩니다.
- Raw records를 map에 직접 넣으면 invalid/duplicate validation을 우회합니다.
- Reader 결과와 execution을 한 호출로 감싸면 lock 없는 snapshot을 atomic lifecycle처럼 보이게 합니다.
- Product rollback은 GDJ-0014 source/adapter/status만 되돌리고 locked GDJ-0013 oracle/static
  artifact를 보존합니다.

## 다음 정확한 작업

`migrations/backend/history.go`의 transport/read port와 compile test를 먼저 추가합니다. 그 다음
fake reader로 `LoadAppliedState`의 nil/context/cause/raw-key copy를 고정하고 SQLite source를
수정하기 전에 `Planner.CheckHistory` timing test를 통과시킵니다.

## 결과와 인수인계

GDJ-0013의 exact reference를 입력으로 package-cycle과 lifecycle 과장을 피한 최소 제품 경계를
활성화했습니다. 다음 담당자는 public migrate/loader/lock을 먼저 만들지 말고 read-only port,
core validation과 live ten-contract adapter만 구현해야 합니다.
