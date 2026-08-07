---
id: GDJ-0013
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "3bcd25ce557cfddc2d73652f9154b6db0fd0b065"
depends_on: ["GDJ-0012"]
contracts: ["MIG-027..MIG-036", "Q-012"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "NOTICE.md", "conformance/contracts/migration-restart-manifest.json", "conformance/runners/django/runner.py", "conformance/runners/django/migration_restart_scenarios.py", "conformance/runners/django/tests/test_migration_restart_scenarios.py", "conformance/runners/django/tests/test_runner_safety.py", "conformance/runners/django/tests/test_scenarios.py", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json", "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS", "conformance/fixtures/godj-migration-restart-not-implemented.json", "conformance/internal/protocol/migration_restart_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/README.md", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Migration Recorder Read와 Restart Planning Compatibility Contracts

## 사용자에게 보이는 결과

프로세스 또는 executor가 다시 만들어진 뒤 실제 DB recorder의 durable applied migration을
읽어 이미 완료한 migration을 다시 실행하지 않고 남은 tail만 계획하는 의미를 Django 6.1
reference로 고정합니다. Recorder table이 없으면 읽기만으로 table을 만들지 않고 empty
applied state로 취급하며, known dependency가 빠진 history는 실행 전에 거부해야 합니다.

## 목표

- MIG-027..036 exact Django/SQLite disposable probe
- Recorder table absent/empty, record/unrecord와 fresh reader 의미 고정
- DB alias별 recorder isolation 고정
- Fresh executor의 applied-prefix tail, fully-applied empty plan 고정
- Graph에 없는 legacy record와 known inconsistent history의 구분 고정
- 이전 executor가 중간 실패한 뒤 durable prefix에서 재계획하는 restart 의미 고정
- Seventh manifest/oracle/static fixture와 provenance 구축
- Read/planning capture가 schema/recorder를 변경하지 않는지 검증
- Seven-set global ID/scenario uniqueness와 42 ordered cross-binding 거부
- 기존 `63 passing + 4 deviation` 제품 결과 보존

## 비목표

- GoDj recorder read 또는 restart planning 제품 API 구현
- `migrations/**`, `migrations/backend/**`, `db/sqlite/**` 제품 source 변경
- Go migration file/source encoding과 module/directory discovery
- public `godj migrate`, `showmigrations`, `makemigrations`
- data migration callback과 historical model mutation ABI
- replacement, squash, merge, conflict, optimizer, fake/fake-initial
- multi-process lock, 실제 process kill, crash repair와 schema/recorder reconciliation
- PostgreSQL, multi-DB router와 backend 확대
- 기존 여섯 manifest/oracle/static/deviation 의미 변경

## 기준 상태

- Baseline product commit:
  `main@3bcd25ce557cfddc2d73652f9154b6db0fd0b065`
- [GDJ-0012](0012-migration-plan-execution-orchestrator.md)은
  `ExecutePlan`, per-migration commit, first-failure stop과 same-transaction reverse를
  구현해 `63 passing + 4 deviation`을 검증했습니다.
- 현재 SQLite recorder는 `RecordApplied`/`RecordUnapplied` write 경계만 제품에 있고,
  durable applied rows를 새 Planner input으로 읽는 제품 경계는 없습니다.
- [ADR-0013](../docs/adr/0013-immutable-migration-planner.md)의 Planner는 caller-supplied
  `AppliedState`만 받고 backend I/O를 하지 않습니다.
- Exact profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  CPython 3.14.3, SQLite 3.50.4, UTC/C locale입니다.

## 잠근 계약

모든 계약은 protocol v2의 `evaluation` phase를 사용합니다. SQL 문자열, SELECT 횟수,
row physical order, applied timestamp와 Django private cache/object identity는 계약하지
않습니다.

| ID | 잠근 외부 동작 |
|---|---|
| MIG-027 | Recorder table이 없으면 applied set은 empty이고 읽기가 table을 생성하지 않음 |
| MIG-028 | Recorder table은 있지만 row가 없으면 empty applied set |
| MIG-029 | Record 뒤 fresh recorder/executor가 app/name identity를 읽음 |
| MIG-030 | Unrecord 뒤 fresh reader에서 identity가 사라짐 |
| MIG-031 | 서로 다른 database alias의 recorder state가 격리됨 |
| MIG-032 | Fresh executor가 applied prefix를 읽고 target까지 남은 tail만 계획 |
| MIG-033 | Target까지 모두 applied이면 fresh executor plan이 empty |
| MIG-034 | Graph에 없는 legacy record는 보존하지만 known target plan을 막지 않음 |
| MIG-035 | Known child만 기록되고 dependency가 없으면 `inconsistent_applied_history` |
| MIG-036 | Forward 중간 실패 뒤 fresh executor가 durable prefix를 읽고 실패 step과 tail만 재계획 |

MIG-036은 process kill/crash 계약이 아닙니다. 이전 executor instance를 버리고 같은 durable
database의 recorder에서 새 instance를 구성하는 restart까지만 관찰합니다.

## 관찰 payload

- 정렬된 applied `(app, name)` identity
- recorder table 존재 여부
- ordered plan `(app, name, direction)`
- known/unknown graph facts
- 구조화된 error category/code
- before/after recorder와 managed schema inventory
- capture 중 DDL/write/기타 non-SELECT statement count 0과 state unchanged

Recorder SELECT의 정확한 횟수나 SQL text는 backend/compiler 구현을 불필요하게 고정하므로
comparison payload에 넣지 않습니다.

## 구현 순서

1. Pinned Django source의 recorder/loader/executor와 관련 upstream test provenance를 확인합니다.
2. Disposable SQLite probe로 열 계약을 각각 독립 실행하고 payload 경계를 조정합니다.
3. Fresh instance가 setup object/cache를 재사용하지 않는지 assertion을 먼저 만듭니다.
4. Seventh manifest, runner registry, oracle, static fixture를 연결합니다.
5. Two-process random-hashseed 결정성과 exact checksum을 고정합니다.
6. Applied key, plan order/direction, unknown record, error taxonomy와 state mutation gate를
   추가합니다.
7. Product `godjcheck`는 새 scenario를 exit 2/no actual output으로 fail-closed하게 둡니다.
8. 기존 여섯 product conformance, full/race/CGO=0/vet와 문서 상태를 재검증합니다.

## 완료 조건

- [x] MIG-027..036 exact disposable probe와 payload review
- [x] Contract ID/scenario/provenance/phase/comparison dimension 10개 잠금
- [x] Two-process random-hashseed oracle byte identity와 SHA-256 checksum
- [x] Recorder absent read가 table을 만들지 않고 모든 read/planning capture가 zero-mutation
- [x] Fresh recorder/executor instance가 durable state만 읽는다는 회귀
- [x] Static fixture가 MIG-027..036 ordered `not_implemented` mismatch 정확히 10개
- [x] Product adapter 없음이 exit 2/no actual output으로 드러남
- [x] Seven set global ID/scenario uniqueness와 42 ordered cross-binding 거부
- [x] Applied/plan/error/state semantic mutation이 comparator mismatch를 냄
- [x] Existing 63 passing + 4 deviation 제품 conformance 유지
- [x] 완료 상태를 `63 passing + 4 deviation + 10 oracle_locked`, reference 총 77개로
  구분하고 77개 전체를 제품 통과로 표현하지 않음
- [x] Portable/exact Python, full Go/vet/race/CGO=0, checksum과 Markdown gate 통과
- [x] CURRENT/matrix/evidence/work가 같은 checkout을 가리킴

## 진행 기록

- [x] GDJ-0012 제품 execution/atomic-reverse 검증 완료
- [x] Recorder write-only와 caller-supplied AppliedState 사이 restart gap 식별
- [x] Pinned source/provenance와 exact one-off probe
- [x] Seventh machine artifact와 false-green gate
- [x] Full verification과 handoff

## 수정 파일

- `conformance/contracts/migration-restart-manifest.json`: MIG-027..036 10개 ordered
  `oracle_locked` contract와 pinned provenance
- `conformance/runners/django/migration_restart_scenarios.py`와 test: fresh recorder/loader,
  explicit consistency preflight, restart tail과 zero-mutation observation
- `conformance/runners/django/runner.py`와 safety/registry test: seventh registry/default oracle,
  exact set completeness와 output pairing
- `conformance/oracles/**/migration-restart-oracle.json`, `SHA256SUMS`: 10 observed locked
  Django result와 일곱 oracle checksum
- `conformance/fixtures/godj-migration-restart-not-implemented.json`: ordered explicit
  not-implemented baseline
- `conformance/internal/protocol/**`, `conformance/cmd/godjcheck/main_test.go`: 77 identity,
  42 cross-binding, semantic mutation과 product fail-closed gates
- `Makefile`: seventh reference contract/oracle check와 regeneration command; product
  `godj-conformance`는 기존 여섯 set 유지

## 결정과 미결정

- Contract와 제품 API를 분리합니다. GDJ-0013에서는 reference가 제품 shape를 따라가는
  false green을 막기 위해 Go recorder-read API를 만들지 않습니다.
- Unknown legacy record는 sorted applied identity에 보존하고, known graph history consistency만
  검사한 뒤 known target을 정상 계획합니다.
- MIG-035는 executor가 암묵적으로 거부한다고 주장하지 않고 migrate-style explicit
  `check_consistent_history`가 plan 호출 전에 실패하는 경계로 고정합니다.
- Product ownership은 후속 [ADR-0015](../docs/adr/0015-recorder-backed-applied-state.md)와
  [GDJ-0014](0014-recorder-backed-restart-planning-product-slice.md)에서 별도 read port로 검증합니다.
- 외부 blocker는 없습니다.

## 테스트 증거

- Baseline:
  [EVID-20260808-011](../docs/status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)
- Result:
  [EVID-20260808-012](../docs/status/TEST_EVIDENCE.md#evid-20260808-012--gdj-0013-recorder-backed-restart-planning-compatibility-contracts)
- Machine artifact commit: `b6af5056bb67fc1d2d32b2163cb7091d700b1e7e`
- Manifest: 10,225 bytes, SHA-256
  `93e25d02208a765001760f76715ff6e9642451c5823efc62cc40b1d249dbd42b`
- Oracle: 33,888 bytes, SHA-256
  `90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`
- Static fixture: 1,715 bytes, SHA-256
  `31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`
- `make check`, full Go/race/CGO=0/vet, portable Python 94 pass/9 exact-only skip,
  exact Python 94/94, two-process byte identity와 독립 P0–P3 audit가 통과했습니다.
- Not run: GitHub-hosted workflow — push하지 않았습니다.

## 다음 정확한 작업

[GDJ-0014](0014-recorder-backed-restart-planning-product-slice.md)에서
`migrations/backend`의 별도 raw read port와 `LoadAppliedState`/`Planner.CheckHistory`를
test-first로 구현합니다. `AtomicBackend`/`Transaction`에 read 메서드를 embed하거나 public
migrate/lock/ProjectState reconstruction으로 범위를 넓히지 않습니다.

## 결과와 인수인계

GDJ-0012 제품 commit을 기준으로 restart gap을 contract-first로 분리해 machine artifact commit
`b6af5056bb67fc1d2d32b2163cb7091d700b1e7e`로 잠갔습니다. 새 set은 제품 통과가 아니라
10 `oracle_locked`이고 기존 제품 결과는 `63 passing + 4 deviation`입니다. 다음 담당자는
GDJ-0014의 read/check/plan 경계만 구현하고 locked oracle/static bytes와 외부 Django checkout을
보존해야 합니다.
