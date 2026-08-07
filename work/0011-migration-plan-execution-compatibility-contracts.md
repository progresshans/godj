---
id: GDJ-0011
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "31d264ad7c85a23b511a7549d698c1c3b0577e92"
depends_on: ["GDJ-0010"]
contracts: ["MIG-017..MIG-026"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "NOTICE.md", "conformance/README.md", "conformance/contracts/migration-execution-manifest.json", "conformance/fixtures/godj-migration-execution-not-implemented.json", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json", "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS", "conformance/runners/django/runner.py", "conformance/runners/django/scenarios.py", "conformance/runners/django/migration_execution_scenarios.py", "conformance/runners/django/tests/**", "conformance/internal/protocol/**", "conformance/cmd/contractcheck/**", "conformance/cmd/godjcheck/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Migration Plan Execution Compatibility Contracts

## 사용자에게 보이는 결과

여러 migration step을 forward/backward로 실행할 때 각 migration의 transaction과 recorder가
어디서 commit되고, 중간 실패 뒤 무엇이 남으며, 이후 step이 실행되는지를 Django 6.1
exact 결과로 먼저 고정합니다.

이 작업은 GoDj `ExecutePlan`을 구현하지 않습니다. 제품 orchestrator를 만들기 전에
plan 전체 atomic이라는 잘못된 가정, 실패 migration만 rollback되는 경계와 historical
state 전달 의미를 false-green 없이 분리합니다.

## 목표

- linear forward/backward multi-step 실행 순서와 최종 schema/record/state 고정
- applied prefix와 unrelated branch가 historical state 계산에 미치는 의미 고정
- operation/recorder 실패에서 앞선 commit, 실패 step rollback과 이후 step 미실행 구분
- mixed-direction plan의 실행 전 거부와 empty-plan no-op 고정
- reverse recorder failure의 실제 Django transaction 경계를 별도 characterization
- sixth manifest/oracle/static fixture와 6-set binding/mutation gate 구축
- 기존 다섯 제품 set 57개가 계속 0-diff임을 검증

## 비목표

- GoDj execution orchestrator, `ExecutePlan`, recorder read/list public API
- migration file/source encoding, loader/CLI와 generator
- data migration callback ABI, `atomic=False`, operation-level atomic 정책
- `fake`, `fake-initial`, squash/replacement/merge/optimizer
- process lock, crash recovery, concurrent execution과 multi-process ownership
- multi-DB/router와 PostgreSQL/기타 backend
- raw SQL 문자열 또는 setup/cleanup query count 동일성
- Go `context.Context`를 Django oracle처럼 가장하는 계약

## 선행 조건과 기준 상태

- 기준 제품/machine commit:
  `main@31d264ad7c85a23b511a7549d698c1c3b0577e92`
- [ADR-0010](../docs/adr/0010-m2-migration-state-and-executor-boundary.md)의 한 migration
  atomic Executor/editor/recorder와 [ADR-0013](../docs/adr/0013-immutable-migration-planner.md)의
  pure Planner 경계는 보존
- MIG-001..016을 포함한 다섯 제품 set 57개는 `passing`
- 새 계약은 Django reference만 잠그며 제품 `migrations/**`, `db/sqlite/**`를 변경하지 않음
- 기존 다섯 manifest/oracle/static fixture와 `/Users/hanhyeonjin/Documents/django` checkout은
  수정하지 않음

## Django Reference / Contract 후보

Exact profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
CPython 3.14.3, SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`입니다. Public migration file이나 CLI 없이
disposable `Migration`/fixture loader와 `MigrationExecutor` 실행 경로로 관찰합니다.

| ID | 동작 | Phase | Compare |
|---|---|---|---|
| MIG-017 | 빈 이력에서 3-step linear forward; operation/record 순서와 최종 state/schema/records | `commit` | result, DB state, metrics |
| MIG-018 | 모두 적용된 linear chain을 dependent-first backward; 최종 empty state/schema/records | `commit` | result, DB state, metrics |
| MIG-019 | applied prefix 뒤 tail만 실행하고 historical state를 step별 누적 | `commit` | result, DB state, metrics |
| MIG-020 | 한 branch를 backward한 뒤 unrelated applied branch의 state/schema/records 보존 | `commit` | result, DB state, metrics |
| MIG-021 | A1 commit 뒤 A2 forward operation 실패; A2 rollback, A3 미시작, A1 durable | `rollback` | error, DB state, metrics |
| MIG-022 | A3 unapply commit 뒤 A2 backward operation 실패; A2 rollback, A1 미시작 | `rollback` | error, DB state, metrics |
| MIG-023 | A1 commit 뒤 A2 forward recorder 실패; A2 schema/record rollback, A3 미시작 | `rollback` | error, DB state, metrics |
| MIG-024 | A3 unapply 성공 뒤 A2 reverse recorder 실패의 실제 schema/record commit 경계 | `commit` | error, DB state, metrics |
| MIG-025 | mixed backward/forward plan을 첫 step 전 거부하고 domain state 보존 | `evaluation` | error, DB state, metrics |
| MIG-026 | empty plan은 step/schema/record mutation 없는 no-op | `commit` | result, DB state, metrics |

MIG-024는 Django characterization입니다. Pinned source inspection과 아직 재현 스크립트로
고정하지 않은 exploratory observation은 Django가 A3 reverse schema/unrecord를 완료한 뒤
A2 reverse schema transaction을 commit하고, A2 unrecord 실패 뒤 schema에는 A1만 남지만
recorder에는 A1/A2를 남기는 경계를 가리킵니다. GoDj의 현재 same-transaction
editor/recorder 원칙은 실패한 A2 schema까지 rollback하므로 의미가 다를 수 있습니다.
이는 completion evidence가 아니며 machine runner가 독립적으로 재현해야 합니다. 후보 차이를
oracle에서 지우지 않고, 후속 제품 work가 ADR/deviation 없이 `passing`으로 가장하지 않습니다.

MIG-025의 normalize 후보는 `migration_execution_error/mixed_directions`입니다. Planner가
mixed plan을 유효하게 계산할 수 있고 실행기가 domain step 전에 거부하는 경계이므로 planning
오류로 분류하지 않습니다. Message와 Python exception type은 계약하지 않으며 최종 taxonomy는
exact artifact와 후속 제품 ADR에서 함께 확정합니다.

### Pinned provenance routing

Source와 test symbol은 모두 Django commit
`fe0a859f537d4238cf49fca39073513206f83122`에서 확인합니다.

- MIG-017/018/021/022: `django/db/migrations/executor.py`의
  `MigrationExecutor.migrate`, `_migrate_all_forwards`, `_migrate_all_backwards`,
  `apply_migration`, `unapply_migration`; `tests/migrations/test_executor.py`의
  `ExecutorTests.test_run`, `test_process_callback`; `docs/topics/migrations.txt`의
  `transactions`, `reversing-migrations`
- MIG-019/020: `MigrationExecutor._create_project_state`와
  `ExecutorTests.test_empty_plan`, `test_unrelated_applied_migrations_mutate_state`;
  `docs/topics/migrations.txt`의 `historical-models`
- MIG-023: `MigrationExecutor.apply_migration`, `record_migration`과
  `ExecutorTests.test_migrations_applied_and_recorded_atomically`
- MIG-024: `MigrationExecutor.unapply_migration`, `record_migration`; 직접 대응하는
  upstream test는 찾지 못했으므로 pinned source와 독립 exact runtime observation을 함께
  provenance로 기록
- MIG-025: `MigrationExecutor.migrate`와 `ExecutorTests.test_mixed_plan_not_supported`
- MIG-026: `MigrationExecutor.migrate`와 `ExecutorTests.test_migrate_skips_schema_creation`,
  `test_empty_plan`

로컬 `/Users/hanhyeonjin/Documents/django` HEAD는 Django 6.2 alpha이므로 실행 reference로
사용하지 않습니다. Source 확인은 locked commit의 `git show`, runtime은 `uv run --frozen`의
pinned Django 6.1 wheel만 사용합니다.

## 관찰 경계

- Result는 test-only ordered step trace와 최종 public `ProjectState` summary를 포함합니다.
  Public callback/result ABI 자체는 이번 work에서 정하지 않습니다.
- DB state는 managed schema inventory와 recorder rows의 before/after를 포함합니다.
- Metrics는 operation/editor/recorder/progress event 순서와 시작하지 않은 step을 구분합니다.
- Setup/cleanup DDL과 seed recorder write는 capture window 밖에서 수행합니다.
- SQL 문자열은 비교하지 않고 statement kind/count 또는 public event 의미만 비교합니다.
- Failure는 첫 오류에서 중단되어야 하며 앞선 committed migration, 실패 migration,
  후속 unstarted migration을 서로 다른 sentinel로 관찰합니다.

## Go-native cancellation 입력

Django에는 Go `context.Context`가 없으므로 cancellation을 MIG-017..026 oracle에 합성하지
않습니다. 후속 제품 work는 별도 Go-native gate로 다음을 검증해야 합니다.

- pre-canceled context는 plan/DB I/O 전에 `context.Canceled`
- 첫 migration commit 직후 cancellation은 다음 begin 전에 중단하고 앞선 commit 보존
- in-flight cancellation 뒤 `context.WithoutCancel` 계열 rollback cleanup과 primary/rollback
  cause 보존

이 항목은 이번 manifest contract ID가 아니며 exact Django `passing`으로 세지 않습니다.

## 구현 단계

1. Pinned Django source/test provenance와 public `MigrationExecutor.migrate(plan=...)` 경계를
   확인합니다.
2. Disposable SQLite에서 MIG-017/021/024를 먼저 독립 probe해 transaction/recorder 경계를
   사실로 확인합니다.
3. 10개 contract의 result/error/DB state/metrics shape와 phase를 최소 payload로 고정합니다.
4. Django runner/manifest/oracle/static fixture와 sixth-set registry를 연결합니다.
5. 6개 set의 전역 ID/scenario uniqueness와 모든 30 ordered cross-binding을 검증합니다.
6. Two-process exact determinism, payload mutation과 static ordered 10 mismatch를 검증합니다.
7. 기존 57 product differential과 full/vet/race/CGO=0 회귀를 실행합니다.
8. CURRENT/matrix/evidence/work를 실제 checkout과 일치시킵니다.

## 완료 조건

- [ ] MIG-017..026이 8~12개 bound와 unique ordered ID를 만족
- [ ] exact profile/provenance와 phase/comparison dimension이 manifest에 고정
- [ ] two-process/random hash-seed oracle이 byte-identical
- [ ] forward/backward success와 partial-applied state가 실제 schema/records에 결속
- [ ] operation/recorder 실패 index와 이후 step 미실행이 sentinel로 구분
- [ ] MIG-024의 Django reverse recorder failure 의미가 축소 없이 관찰
- [ ] mixed plan은 첫 domain step 전에 실패하고 empty plan은 no-op
- [ ] plan direction/order, step event, state/record/schema/error/phase 변이가 mismatch
- [ ] static GoDj fixture가 ordered 10 `not_implemented` mismatch
- [ ] 제품 adapter 미구현은 `godjcheck` exit 2/no actual-output으로 fail-closed
- [ ] six-set 30 ordered cross-binding과 기존 artifact checksum 유지
- [ ] 기존 다섯 제품 set 57개가 계속 semantic 0-diff
- [ ] portable/exact Python, full Go/vet/race/CGO=0과 documentation gate 통과
- [ ] 상태/evidence/work가 같은 checkout을 가리킴

## 진행 기록

- [x] GDJ-0010 immutable Planner 제품과 57 passing baseline
- [ ] Django multi-step executor provenance와 machine exact probe
- [ ] MIG-021 forward operation failure와 MIG-024 reverse recorder failure를 checked-in runner로 재현
- [ ] sixth contract/oracle/static set
- [ ] false-green/회귀 gate와 인수인계

## 수정 파일

아직 제품 수정 없음. Contract-only runner/artifact를 구현한 뒤 실제 파일과 역할을 기록합니다.

## 결정된 사항

- Plan 전체를 한 transaction으로 가정하지 않고 migration별 commit을 관찰합니다.
- 첫 실패 뒤 후속 step은 실행하지 않아야 합니다.
- Context cancellation은 Django differential과 Go-native gate를 분리합니다.
- MIG-024의 preliminary Django/GoDj 의미 차이는 machine oracle을 잠근 뒤 호환 구현 또는
  명시적 deviation으로 결정합니다.
- File/CLI/data callback ABI는 이 work에서 정하지 않습니다.

## 미결정/Blocker

외부 blocker는 없습니다. MIG-024 exploratory observation은 durable evidence가 아니며
checked-in machine runner/oracle이 최종 정본입니다. Product orchestrator API, public state
반환과 mixed-plan error taxonomy는 oracle 잠금 뒤 별도 ADR/work에서 결정합니다.

## 테스트 증거

- Evidence ID: GDJ-0011 완료 시 새 항목 추가
- Profile 확인:
  `LC_ALL=C TZ=UTC uv run --frozen python -c 'import django, sqlite3; print(django.get_version(), django.__file__); print(sqlite3.sqlite_version)'`
- Result: contract 후보 단계. 환경 fingerprint와 pinned source routing만 확인했고 기존 57
  product contract만 `passing`
- Exploratory note, not completion evidence: 일회성 inline probe는 forward failure 뒤 A1만,
  reverse recorder failure 뒤 schema A1/records A1·A2를 관찰했지만 재현 스크립트와 raw
  output을 보존하지 않았으므로 machine runner가 다시 증명해야 합니다.
- Not run: checked-in full exact runner/oracle, sixth-set/static baseline, execution product adapter

## 위험과 rollback

- Setup/seed statement를 capture에 섞으면 failure step과 transaction 수가 거짓으로 보입니다.
- 한 migration rollback과 앞선 migration commit을 하나의 `state_unchanged`로 합치면 false
  green이 됩니다.
- MIG-024를 현재 GoDj transaction 설계에 맞춰 정규화하면 실제 Django 차이를 잃습니다.
- Context cancellation을 Django error처럼 합성하면 존재하지 않는 oracle을 만들게 됩니다.
- Rollback은 sixth-set 신규 artifact/runner/docs만 되돌리며 제품/기존 5-set bytes는 보존합니다.

## 다음 정확한 작업

Pinned Django 6.1 runner에서 MIG-017..026 전체를 disposable SQLite로 구현하고,
MIG-021/024의 preliminary observation을 두 독립 process에서 재현해
event/schema/recorder state와 phase를 machine artifact로 잠급니다.

## 결과와 인수인계

GDJ-0010 product commit `31d264ad7c85a23b511a7549d698c1c3b0577e92` 뒤
contract-only 작업으로 활성화했습니다. 제품 실행 orchestrator를 구현하기 전에 Django의
multi-migration commit/rollback 경계를 잠그며, 기존 57 passing과 static false-green
baseline을 보존합니다.
