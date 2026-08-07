---
id: GDJ-0010
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "9fc3df42f17b61b0a0202f21d3d99190c0db2d28"
depends_on: ["GDJ-0009"]
contracts: ["MIG-005..MIG-016"]
allowed_paths: ["migrations/executor.go", "migrations/planner.go", "migrations/planner_graph.go", "migrations/planner_test.go", "migrations/external_test.go", "conformance/contracts/migration-planning-manifest.json", "conformance/runners/godj/migration_planning_scenarios.go", "conformance/runners/godj/runner.go", "conformance/runners/godj/runner_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/internal/protocol/migration_planning_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "Makefile", ".github/workflows/ci.yml", "conformance/README.md", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Immutable Migration Graph and Applied-State Planner Product Slice

## 사용자에게 보이는 결과

사용자는 migration identity/dependency와 현재 applied migration 목록, ordered target을
Go 값으로 제공해 database I/O 없이 deterministic forward/backward `PlanStep`을 얻습니다.
잘못된 target, dependency graph, cycle와 inconsistent history는 panic이나 문자열 추측이
아니라 structured planning error로 받습니다.

이 작업이 끝나면 GDJ-0009의 MIG-005..016을 실제 GoDj public planner 경로로 실행하고
Django 6.1 oracle과 의미상 0-diff인지 확인할 수 있습니다.

## 목표

- immutable migration identity/dependency graph와 별도 immutable applied state 구현
- caller-ordered named/zero target의 dependency-valid deterministic plan 구현
- ProjectState/operation/backend와 분리된 zero-I/O public planner 경계 구현
- graph/history/target structured error와 deterministic diagnostics 구현
- MIG-005..016 actual adapter를 연결해 12개를 `passing`으로 전환

## 비목표

- migration file/source ABI, generator와 CLI/loader
- data migration callback, autodetector/rename, optimizer
- merge/squash/replacement, `fake`/`fake-initial`
- recorder read/list, `ExecutePlan`, 여러 migration transaction/failure orchestration
- process lock, crash recovery, concurrent apply와 multi-DB
- SQLite/backend/schema editor/DDL capability 변경
- applied history에서 historical `ProjectState`를 재구성하는 기능
- Django mutable graph/Python object/traversal stack 복제

## 선행 조건과 기준 상태

- 기준 machine artifact: `main@9fc3df42f17b61b0a0202f21d3d99190c0db2d28`
- 기준 product: `6f1aab78a6e365e62f5a3b59b040b90b981b4978`
- [ADR-0010](../docs/adr/0010-m2-migration-state-and-executor-boundary.md)의
  ProjectState/one-migration Executor 경계는 보존
- [ADR-0013](../docs/adr/0013-immutable-migration-planner.md)의 identity graph/applied-state
  경계를 구현
- [GDJ-0009](0009-migration-planning-compatibility-contracts.md)의 exact oracle 12개는
  `oracle_locked`; 제품 planner/adapter는 아직 없음
- 보존 대상: Django checkout, locked oracle와 static not-implemented fixture bytes,
  `migrations/backend/**`, `db/sqlite/**`

## Django Reference / Contract

Exact profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
CPython 3.14.3, SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`입니다.

| ID | 제품이 만족할 동작 |
|---|---|
| MIG-005 | linear dependency를 target보다 먼저 forward |
| MIG-006 | applied prefix pruning과 fully-applied empty plan |
| MIG-007 | unknown named target를 structured error로 거부 |
| MIG-008 | prior same-app target은 남기고 applied descendant를 backward |
| MIG-009 | app zero target에서 cross-app dependent를 먼저 backward |
| MIG-010 | cross-app dependency-first forward |
| MIG-011 | cross-app dependent-first backward |
| MIG-012 | caller target order 보존과 shared dependency 1회 |
| MIG-013 | same-app child에서 rollback을 시작하고 유효한 다른 branch 보존 |
| MIG-014 | traversal 전 inconsistent applied history 거부 |
| MIG-015 | graph construction의 missing dependency 거부 |
| MIG-016 | graph construction의 dependency cycle 거부 |

SQL 문자열, Django private DFS와 incomparable sibling order, Python exception message와
graph object identity는 계약하지 않습니다. GoDj는 dependency가 정하지 않는 sibling에
동시에 eligible한 `MigrationKey` 중 ascending key를 emit하는 canonical tie-break를
사용합니다. Named-applied child와 app-zero root는 ascending seed 순서로 **각 closure를
순차 처리**하며 여러 closure를 union/global sort하지 않습니다.

## 설계

Public shape는 ADR-0013을 따릅니다.

```go
type MigrationKey struct { App, Name string }
type AppliedState struct { /* immutable */ }
type Target struct { /* named or app-zero */ }
type PlanStep struct { Key MigrationKey; Direction Direction }
type Planner struct { /* immutable identity graph */ }

func NewAppliedState(...MigrationKey) (AppliedState, error)
func NamedTarget(MigrationKey) Target
func ZeroTarget(string) Target
func NewPlanner(...Migration) (Planner, error)
func (Planner) Plan(AppliedState, ...Target) ([]PlanStep, error)
```

기존 `Migration`에는 prerequisite 방향의 `Dependencies []MigrationKey`와 `Key()`를
추가합니다. Planner는 operation을 보관하지 않고 copied/sorted key adjacency만 가집니다.
`Plan`은 local working applied set을 target 순서대로 갱신하며 global cache/I/O가 없습니다.

기존 execution `*Error` 대신 별도 `*PlanningError`를 사용합니다. Locked category/code는
`migration_plan_error/target_not_found`,
`migration_history_error/inconsistent_applied_history`,
`migration_graph_error/dependency_not_found`,
`migration_graph_error/dependency_cycle`입니다.

Construction error precedence는 invalid node → duplicate node → invalid/duplicate edge →
missing dependency → cycle이며, 같은 종류에서는 lexicographically first node/edge/SCC를
선택합니다. Applied history violation도 `(child, parent)` 순으로 선택해 input permutation이
diagnostics를 바꾸지 않습니다. Self-dependency는 singleton cycle로 분류합니다.

Graph input은 `migration_graph_error`의 `invalid_node`, `duplicate_node`,
`invalid_dependency`, `duplicate_dependency`를, AppliedState input은
`migration_history_error`의 `invalid_applied_state`, `duplicate_applied`를, target
representation은 `migration_plan_error/invalid_target`을 사용합니다. Locked error는 ADR의
Node=child/Related=parent 또는 target/SCC member field 의미를 따릅니다. AppliedState도
invalid-before-duplicate와 lexicographic 선택을 보장합니다.

No target과 unknown app zero target은 empty plan, zero-value/empty target은
`invalid_target`, duplicate target은 sequential no-op가 될 수 있습니다. Mixed
forward/backward plan은 pure Planner가 반환할 수 있으며 실행 시 거부/분할하는 orchestrator는
이번 범위가 아닙니다.

Zero `Planner`와 zero `AppliedState`는 각각 empty graph/empty applied set으로 유효하며
empty constructor 결과와 같은 동작을 합니다.

## 구현 단계

1. MigrationKey/AppliedState/Target/PlanStep와 deterministic graph validation을 unit test로
   먼저 고정합니다.
2. Forward/backward/zero/multi-target planner와 consistency preflight를 구현합니다.
3. Input alias/permutation, random DAG, concurrent shared Planner와 external compile gate를
   추가합니다.
4. GoDj migration-planning adapter의 plan/error는 public API 결과에서 normalize하고,
   logical before/after state와 zero-I/O metrics는 공통 scenario applied-key snapshot에서
   산출하도록 연결합니다. Contract ID별 oracle-shaped 상수는 사용하지 않습니다.
5. Manifest status를 `passing`으로 올리고 두 actual determinism, 12-contract differential,
   static mismatch와 전체 회귀 gate를 검증합니다.
6. CURRENT/matrix/evidence/work/ADR 결과를 실제 checkout과 일치시킵니다.

## 완료 조건

- [ ] MIG-005..016 table test와 public adapter가 동일 제품 경로를 실행
- [ ] target order reversal, graph/dependency/applied permutation과 shared dedup 검증
- [ ] 생성 뒤 caller dependency slice mutation이 Planner를 바꾸지 않음
- [ ] missing/duplicate/cycle/history error가 deterministic structured error
- [ ] no-target/unknown-zero/invalid/duplicate/mixed target 경계가 문서 정책과 일치
- [ ] zero Planner/AppliedState와 invalid/duplicate input taxonomy가 external API에서 안전
- [ ] random DAG에서 forward parent-before-child/backward child-before-parent invariant
- [ ] external package compile과 ProjectState/backend 무변경 검증
- [ ] planner source의 DB/backend import 부재와 repeated Plan input 불변을 durable gate로 검증
- [ ] 같은 Planner/AppliedState concurrent Plan race test 통과
- [ ] 두 독립 Go actual byte-identical, Django oracle과 12-contract semantic 0-diff
- [ ] fixture mutation이 adapter result/DB state/metrics에 전파되어 하드코딩을 거부
- [ ] static fixture의 ordered 12 mismatch와 unknown scenario fail-closed 유지
- [ ] 다섯 set 총 57개 `passing`, 20 ordered cross-binding과 checksum 유지
- [ ] full/vet/race/CGO=0, 100회 shuffle와 20회 focused race 통과
- [ ] 상태·evidence·work와 Q-012의 남은 범위가 현재 checkout과 일치

## 진행 기록

- [x] GDJ-0009 exact contract/provenance와 false-green baseline
- [x] ADR-0013 public boundary 결정
- [ ] planner unit-first 구현
- [ ] conformance adapter와 status 전환
- [ ] 최종 검증과 인수인계

## 수정 파일

아직 제품 수정 없음. 실제 변경을 완료할 때 파일과 역할을 기록합니다.

## 결정된 사항

- ProjectState와 AppliedState를 분리합니다.
- Public mutable graph를 노출하지 않고 Planner construction에서 identity/dependency를
  deep-copy합니다.
- Target input order는 보존하지만 dependency가 정하지 않는 sibling order는 GoDj
  lexicographic policy이며 Django private traversal 호환이 아닙니다.
- Existing Executor/backend는 이번 단면에서 바꾸지 않습니다.

## 미결정/Blocker

외부 blocker는 없습니다. File ABI, loader/CLI, data callback, multi-plan execution과
lock/crash recovery는 Q-012의 후속 work가 필요합니다.

## 테스트 증거

- Evidence ID: GDJ-0010 완료 시 새 항목 추가
- Command: 아직 실행 전
- Result: 제품 `Not started`; reference 12개만 `oracle_locked`
- Not run: planner unit/compile/race/differential 전체

## 위험과 rollback

- Migration operation이나 caller slice를 graph가 보관하면 alias/race가 생길 수 있습니다.
- Applied history consistency를 plan 뒤에 검사하면 잘못된 partial plan이 노출될 수 있습니다.
- Target order와 sibling tie-break를 혼동하면 Django private traversal을 public ABI로 고정할
  수 있습니다.
- 복수 validation 오류의 선택 규칙을 구현하지 않으면 input permutation에 따라 diagnostics가
  흔들릴 수 있습니다.
- Rollback은 planner 신규 파일과 Migration dependency field/status adapter만 되돌리며,
  locked oracle/static fixture는 변경하지 않습니다.

## 다음 정확한 작업

`migrations/planner_test.go`에 MIG-005 linear forward, MIG-015 missing dependency와
Migration dependency-slice alias case를 먼저 작성한 뒤 최소 public type/constructor를
구현합니다.

## 결과와 인수인계

GDJ-0009 machine baseline 뒤 활성화됨. 현재 제품 code는 아직 없고 locked Django oracle,
static 12 mismatch와 기존 45 product passing을 기준으로 시작합니다.
