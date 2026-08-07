---
id: GDJ-0009
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "6f1aab78a6e365e62f5a3b59b040b90b981b4978"
depends_on: ["GDJ-0008"]
contracts: ["MIG-005..MIG-016"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "NOTICE.md", "conformance/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Migration Planning Compatibility Contracts

## 사용자에게 보이는 결과

사용자가 migration target을 선택했을 때 아직 적용하지 않은 dependency가 어떤 순서로
forward plan에 들어가고, 이미 적용된 migration을 어느 순서로 되돌리며, 잘못된 graph나
적용 기록을 어떤 오류로 거부하는지 Django 6.1의 관찰 가능한 동작으로 고정합니다.

이 작업은 GoDj migration planner 제품을 구현하지 않습니다. 기존 MIG-001..004의
in-memory single-migration executor에서 public file/CLI나 graph API로 바로 확장하기 전에,
후속 제품 단면이 만족해야 할 deterministic plan·error·zero-mutation 계약을 만드는
contract-only 작업입니다.

## 고정한 계약

| ID | 잠글 동작 |
|---|---|
| MIG-005 | 빈 적용 기록에서 linear target까지 dependency를 forward 순서로 계획 |
| MIG-006 | 일부 dependency가 적용됐으면 suffix만 계획하고 fully-applied target은 empty plan |
| MIG-007 | 존재하지 않는 named target을 state 변경 없이 구조화된 오류로 거부 |
| MIG-008 | 이전 same-app target으로 되돌릴 때 target은 남기고 적용된 descendant를 backward 순서로 계획 |
| MIG-009 | app의 zero state target은 해당 app의 적용 migration과 필요한 dependent를 backward 계획 |
| MIG-010 | cross-app dependency가 target migration보다 먼저 오도록 forward 계획 |
| MIG-011 | cross-app dependent가 dependency보다 먼저 제거되도록 backward 계획 |
| MIG-012 | caller-ordered target이 공유하는 dependency를 한 번만 포함하고 target 순서를 보존 |
| MIG-013 | same-app child에서 rollback을 시작해 descendant를 제거하고 유효한 다른 branch는 보존 |
| MIG-014 | public planning preflight가 inconsistent applied history를 구조화된 오류로 거부 |
| MIG-015 | graph construction이 존재하지 않는 dependency node를 구조화된 오류로 거부 |
| MIG-016 | graph construction이 dependency cycle을 traversal 순서 노출 없이 거부 |

`zero state`는 migration이 하나도 적용되지 않은 목표를 가리키는 이 문서의 관찰
용어이며 protocol 또는 Go public API 이름이 아닙니다. MIG-005..014 phase는
`evaluation`, graph construction 자체가 실패하는 MIG-015/016은 `construction`입니다.
성공 계약은 result/DB state/metrics, 오류 계약 MIG-007/014/015/016은
error/DB state/metrics를 비교합니다. Django private class/object identity, 오류 문구와
cycle traversal 순서는 계약하지 않습니다.

## 목표 흐름

```text
pinned Django 6.1 migration fixtures + disposable SQLite recorder state
→ target/applied-state별 planning probe
→ ordered app/name/direction 또는 structured error normalization
→ fifth manifest + deterministic exact oracle
→ explicit GoDj not_implemented fixture
→ 후속 migration-planner 제품 work/ADR 입력
```

## 제한 범위

- 기존 exact Django 6.1 / CPython 3.14.3 / SQLite 3.50.4 profile
- 두세 app의 작고 고정된 linear/branched/cross-app dependency graph
- target과 적용 기록에 따른 forward/backward plan의 ordered `(app, name, direction)`
- empty/shared-dependency plan과 graph/history 오류의 category/code
- planning 전후 recorder/schema inventory가 같고 DDL/DML side effect가 0임을 확인
- protocol v2의 독립 fifth contract set과 provenance/license 기록

## 비목표

- GoDj migration graph/planner/executor 제품 코드 구현 또는 MIG-001..004 core 변경
- public migration file encoding, Go data callback ABI, generated migration source
- `godj makemigrations`, `migrate`, `showmigrations` CLI 표면
- migration autodetector, rename prompt, optimizer, merge/squash/replacement 정책
- `fake`/`fake-initial`, multi-process lock, crash recovery 또는 concurrent apply
- 실제 schema forward/backward 실행, table rebuild나 backend DDL capability 확대
- PostgreSQL/MySQL/Oracle 및 multi-DB router
- Django `MigrationGraph`/`MigrationExecutor`의 Python 내부 객체 구조 복제

## Django reference와 provenance

- Pinned source commit:
  `fe0a859f537d4238cf49fca39073513206f83122`
- 공개 의미: `docs/topics/migrations.txt`, `docs/ref/django-admin.txt`의 dependency,
  target/plan 설명
- 실행/계획 근거: `django/db/migrations/executor.py`의 `migration_plan()`과
  `tests/migrations/test_executor.py`의 forward, backward, applied-target 사례
- graph/error 근거: `django/db/migrations/graph.py`, `loader.py`와
  `tests/migrations/test_graph.py`, `test_loader.py`의 cross-app order, missing node,
  cycle, inconsistent history 사례
- 각 manifest entry는 exact source line/symbol, 분류(`upstream-derived` 또는
  `independent`)와 Django BSD 고지를 기록합니다. Upstream fixture/source를 통째로
  복사하지 않고 최소 독립 fixture와 normalized observation을 작성합니다.

## 시작 전 probe

1. 로컬 Django checkout을 수정하지 않고 임시 migration module과 disposable SQLite DB로
   MIG-005..016 후보를 각각 두 번 독립 실행합니다.
2. Graph construction/recorder setup은 capture window 밖에서 수행하고, plan 요청 중
   schema/recorder DDL·DML이 0인지 별도 instrumentation으로 확인합니다.
3. Forward/backward plan은 `(app, name, direction)`만 normalize하고 target, initial applied
   records, final records와 schema inventory를 함께 기록합니다.
4. Multiple target/branch ordering이 fixture 삽입 순서나 hash seed에 흔들리지 않는지
   `PYTHONHASHSEED=random`인 별도 process와 순서 교란 probe로 확인합니다.
5. Missing node/cycle/inconsistent history는 Python exception class나 전체 message 대신
   stable category/code와 원인 node/history payload로 표현 가능한지 확인합니다.

Probe가 후보 동작의 안정성을 지지하지 않으면 contract ID의 의미를 조정하고 근거를 이
문서에 남긴 뒤 manifest를 작성합니다. 관찰 전 가설을 oracle로 고정하지 않습니다.

## 구현 순서

1. 각 후보의 disposable probe, upstream provenance와 license 분류를 완료합니다.
2. MIG-005..016의 ID/order/phase/comparison dimension을 fifth manifest에 고정합니다.
3. Django scenario runner와 normalizer를 구현해 두 independent process에서 byte-identical한
   exact oracle을 생성합니다.
4. Static GoDj `not_implemented` fixture와 현재 제품 adapter의 fail-closed 경계를
   추가합니다. 제품 runner가 없는 상태를 `passing`으로 표시하지 않습니다.
5. 다섯 set 전체의 contract ID/scenario uniqueness와 모든 20개 ordered cross-binding을
   검증합니다.
6. Plan order/direction/target/applied state, error category/code와 zero-mutation payload를
   하나씩 바꾸는 mutation test로 comparator/adapter 하드코딩 false green을 확인합니다.
7. 기존 45개 GoDj differential, full/vet/race/CGO=0, portable/exact Python과 checksum
   gate를 유지하고 상태·evidence·다음 제품 work를 갱신합니다.

## 완료 gate

- [x] exact disposable probe가 12개 후보의 안정된 observable을 두 번 동일하게 생성
- [x] MIG-005..016 ID/order/phase/comparison/provenance가 fifth manifest에 고정
- [x] Django oracle이 두 independent process에서 byte-identical하고 checksum 기록
- [x] explicit GoDj `not_implemented` actual이 정확히 12개 ordered mismatch
- [x] 다섯 set의 전역 ID/scenario uniqueness와 20개 ordered cross-binding 거부
- [x] plan order/direction/target/applied/error/zero-mutation 변경이 false green 없이 실패
- [x] 기존 45개 GoDj differential이 계속 0-diff
- [x] portable/exact Python, full/vet/race/CGO=0/checksum gate 통과
- [x] provenance/license/NOTICE 검토와 CURRENT/matrix/evidence/work 일치
- [x] 후속 migration-planning 제품 work item이 exact scope와 완료 gate를 갖고 `active`

## 알려진 위험

- `MigrationGraph`의 traversal 순서를 그대로 public API로 복제하면 fixture insertion
  detail을 제품 계약으로 오인할 수 있습니다. 사용자에게 보이는 dependency-valid
  deterministic plan만 고정하고 독립 순서 교란 probe를 둡니다.
- MIG-012는 dependency-required precedence, caller target order와 shared dependency
  deduplication만 고정합니다. Dependency가 정하지 않는 incomparable sibling의 Django
  private DFS tie-break는 계약하지 않고 GDJ-0010의 Go deterministic policy로 남깁니다.
- Plan 결과만 비교하면 recorder/schema를 변경하는 잘못된 구현도 통과할 수 있습니다.
  Planning은 zero-mutation이어야 하므로 DB state와 I/O metrics를 함께 비교합니다.
- 서로 다른 graph가 같은 최종 schema를 만들 수 있어 최종 DB state만으로 plan 방향과
  중복 제거 오류를 잡을 수 없습니다. Ordered plan item을 직접 payload에 둡니다.
- Django의 squashed/replacement migration 처리는 별도 의미가 크므로 첫 planning 단면에
  섞지 않습니다.
- Q-012 전체에는 file ABI, data callback, lock/crash recovery가 남습니다. 이 계약 set이
  migration 전체 설계를 해결했다고 표현하지 않습니다.

## 시작 기록

- 시작 checkout: `main@6f1aab78a6e365e62f5a3b59b040b90b981b4978`
- 시작 worktree: GDJ-0008 제품 commit 뒤 문서 인수인계 변경만 존재
- 외부 blocker: 없음
- 보존 대상: `/Users/hanhyeonjin/Documents/django` reference checkout은 read-only
- 첫 명령: exact profile에서 MIG-005 linear forward와 MIG-014 inconsistent history를
  disposable probe로 실행해 성공/오류 양쪽 normalization을 먼저 검증

## 다음 제품 작업

후속 [GDJ-0010](0010-immutable-migration-planner-product-slice.md)은 GoDj의 immutable
migration identity graph, applied-state-aware planner와 structured planning error를
[ADR-0013](../docs/adr/0013-immutable-migration-planner.md)에 따라 구현하고 MIG-005..016
actual adapter를 연결합니다. Public file/CLI, data callback과 process lock은 그 제품
단면에서도 Q-012의 후속 범위로 남깁니다.

## 수정 파일

- `conformance/contracts/migration-planning-manifest.json`: MIG-005..016 정본
- `conformance/runners/django/migration_planning_scenarios.py`: exact Django planner 관찰
- `conformance/oracles/**/migration-planning-oracle.json`: deterministic oracle
- `conformance/fixtures/godj-migration-planning-not-implemented.json`: 명시적 미구현 baseline
- `conformance/internal/protocol/*artifacts_test.go`: hash/status/cross-binding/mutation gate
- `conformance/runners/django/tests/**`, `conformance/cmd/godjcheck/main_test.go`: live runner,
  output mapping과 product fail-closed 회귀
- `Makefile`, `SHA256SUMS`: fifth set validation/regeneration/checksum 연결

제품 `migrations/**`, backend와 Django reference checkout은 변경하지 않았습니다.

## 결정된 사항

- MIG-006은 applied-prefix와 fully-applied empty 두 case를 한 contract에서 순서대로
  관찰하고, missing target을 별도 MIG-007 top-level error로 둡니다.
- Missing target, inconsistent history, missing dependency와 cycle은 각각 다른
  category/code와 phase를 사용합니다. MIG-014는 `migration_plan()` 내부 예외가 아니라
  public migrate 흐름의 `check_consistent_history()` preflight 의미입니다.
- DB snapshot은 physical recorder table 이름이 아니라 `recorder_present`, 정렬된 applied
  records와 managed schema inventory를 관찰합니다.
- Planning capture는 DDL/write/기타 non-SELECT statement가 모두 0이고 state가 같은지
  검증합니다. 모든 SELECT까지 0이라고 주장하지 않습니다.
- Cycle raw message/path와 incomparable sibling traversal은 비계약입니다.

## 테스트 증거

- Evidence ID:
  [EVID-20260808-008](../docs/status/TEST_EVIDENCE.md#evid-20260808-008--gdj-0009-migration-planning-compatibility-contracts)
- Machine artifact commit: `9fc3df42f17b61b0a0202f21d3d99190c0db2d28`
- `make check`, full/race/CGO=0/vet, portable/exact Python, focused scenario, checksum과
  false-green mutation gate 모두 통과
- Oracle two-process regeneration과 checked-in bytes가 모두 39,139 bytes, SHA-256
  `7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474`
- Manifest 10,623 bytes, SHA-256
  `7e8f0d19c8f227721e7cfe4254a4f39d1313e801f1ea0a759e14c46a3dbbe876`
- Static fixture 1,869 bytes, SHA-256
  `a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13`
- Static comparison은 예상 exit 1과 ordered 12 mismatch, 제품 `godjcheck`는 unsupported
  scenario exit 2와 actual output 미생성이 정상 결과

## 결과와 인수인계

다섯 번째 ordered set에 MIG-005..016을 `oracle_locked`로 고정했습니다. Django oracle은
12개 `observed`, static GoDj fixture는 12개 `not_implemented`이며, 기존 네 제품 set의
45개 `passing`과 구분합니다. 다섯 set의 ID/scenario는 전역으로 유일하고 모든 20개
ordered cross-binding이 거부됩니다.

새 reference 계약은 제품 planner 구현을 뜻하지 않습니다. 다음 담당자는 GDJ-0010에서
locked oracle/static fixture/Django runner를 보존하고 `migrations` pure planner와 GoDj
adapter만 추가해야 합니다.
