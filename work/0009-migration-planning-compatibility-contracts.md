---
id: GDJ-0009
status: active
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

## 고정할 계약 후보

| ID | 잠글 동작 |
|---|---|
| MIG-005 | 빈 적용 기록에서 linear target까지 dependency를 forward 순서로 계획 |
| MIG-006 | 일부 dependency가 적용됐으면 미적용 suffix만 forward plan에 포함 |
| MIG-007 | target과 필요한 dependency가 모두 적용됐으면 empty plan |
| MIG-008 | 이전 same-app target으로 되돌릴 때 target은 남기고 적용된 descendant를 backward 순서로 계획 |
| MIG-009 | app의 zero state target은 해당 app의 적용 migration과 필요한 dependent를 backward 계획 |
| MIG-010 | cross-app dependency가 target migration보다 먼저 오도록 forward 계획 |
| MIG-011 | cross-app dependent가 dependency보다 먼저 제거되도록 backward 계획 |
| MIG-012 | 여러 target이 공유하는 dependency를 한 번만 포함하고 deterministic 순서 유지 |
| MIG-013 | 적용된 target의 same-app child까지만 되돌리고 불필요한 다른 branch는 보존 |
| MIG-014 | 적용 migration의 dependency가 미적용인 inconsistent history를 구조화된 오류로 거부 |
| MIG-015 | 존재하지 않는 dependency/target node를 구조화된 오류로 거부 |
| MIG-016 | dependency cycle을 구조화된 오류로 거부 |

`zero state`는 migration이 하나도 적용되지 않은 목표를 가리키는 이 문서의 관찰
용어이며 protocol 또는 Go public API 이름이 아닙니다. 최종 ID별 fixture는 disposable
probe 결과를 검토한 뒤 manifest에 고정하며, Django private class/object identity나 오류
문구 전체를 계약하지 않습니다.

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

- [ ] exact disposable probe가 12개 후보의 안정된 observable을 두 번 동일하게 생성
- [ ] MIG-005..016 ID/order/phase/comparison/provenance가 fifth manifest에 고정
- [ ] Django oracle이 두 independent process에서 byte-identical하고 checksum 기록
- [ ] explicit GoDj `not_implemented` actual이 정확히 12개 ordered mismatch
- [ ] 다섯 set의 전역 ID/scenario uniqueness와 20개 ordered cross-binding 거부
- [ ] plan order/direction/target/applied/error/zero-mutation 변경이 false green 없이 실패
- [ ] 기존 45개 GoDj differential이 계속 0-diff
- [ ] portable/exact Python, full/vet/race/CGO=0/checksum gate 통과
- [ ] provenance/license/NOTICE 검토와 CURRENT/matrix/evidence/work 일치
- [ ] 후속 migration-planning 제품 work item이 exact scope와 완료 gate를 갖고 `ready`

## 알려진 위험

- `MigrationGraph`의 traversal 순서를 그대로 public API로 복제하면 fixture insertion
  detail을 제품 계약으로 오인할 수 있습니다. 사용자에게 보이는 dependency-valid
  deterministic plan만 고정하고 독립 순서 교란 probe를 둡니다.
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

이 작업의 oracle과 false-green gate가 완료되면 후속 GDJ-0010에서 GoDj의 immutable
migration node/graph, applied-state-aware planner와 structured planning error를 별도 ADR로
결정하고 MIG-005..016 actual adapter를 구현합니다. Public file/CLI, data callback과
process lock은 그 제품 단면에서도 Q-012의 후속 범위로 남깁니다.
